package mariadb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"

	"github.com/Gu1llaum-3/koffr/internal/executor"
)

// BinlogStatus is what the server says about its binary log.
type BinlogStatus struct {
	// Enabled is log_bin. Without it there is nothing to archive and a
	// point-in-time recovery is impossible, whatever else is configured.
	Enabled bool
	// File and Position are where the server is writing right now.
	File     string
	Position uint64
	// GTID is gtid_binlog_pos, recorded for the operator and for a future
	// replica; the replay itself keys on File and Position.
	GTID string
	// Files lists what the server still holds, oldest first. A file the archive
	// does not have and the server no longer has is a gap nothing can close.
	Files []BinlogFile
	// MaxSize is max_binlog_size: how large a file grows before the server
	// rotates on its own, which bounds the RPO of a busy database and says
	// nothing about a quiet one.
	MaxSize uint64
}

// BinlogFile is one entry of SHOW BINARY LOGS.
type BinlogFile struct {
	Name string
	Size uint64
}

// Binlog reports the server's binary-log state (EF-032, EF-031).
func (c Config) Binlog(ctx context.Context, ex executor.Executor) (BinlogStatus, error) {
	db, err := c.Connect(ctx, ex)
	if err != nil {
		return BinlogStatus{}, err
	}
	defer func() { _ = db.Close() }()
	return binlogStatus(ctx, db)
}

func binlogStatus(ctx context.Context, db *sql.DB) (BinlogStatus, error) {
	var st BinlogStatus

	var name, value string
	if err := db.QueryRowContext(ctx, "SHOW VARIABLES LIKE 'log_bin'").Scan(&name, &value); err != nil {
		return st, fmt.Errorf("mariadb: read log_bin: %w", err)
	}
	st.Enabled = strings.EqualFold(value, "ON")
	if !st.Enabled {
		return st, nil
	}

	if err := db.QueryRowContext(ctx, "SHOW VARIABLES LIKE 'max_binlog_size'").Scan(&name, &value); err == nil {
		st.MaxSize, _ = strconv.ParseUint(value, 10, 64)
	}

	var err error
	st.File, st.Position, err = masterStatus(ctx, db)
	if err != nil {
		return st, err
	}

	// gtid_binlog_pos is MariaDB's; on a server without GTID it is empty, not
	// an error.
	_ = db.QueryRowContext(ctx, "SELECT @@gtid_binlog_pos").Scan(&st.GTID)

	files, err := db.QueryContext(ctx, "SHOW BINARY LOGS")
	if err != nil {
		return st, fmt.Errorf("mariadb: list the binary logs: %w", err)
	}
	defer func() { _ = files.Close() }()
	fcols, _ := files.Columns()
	for files.Next() {
		vals := make([]sql.NullString, len(fcols))
		ptrs := make([]any, len(fcols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := files.Scan(ptrs...); err != nil {
			return st, fmt.Errorf("mariadb: read a binary log entry: %w", err)
		}
		var f BinlogFile
		for i, col := range fcols {
			switch strings.ToLower(col) {
			case "log_name":
				f.Name = vals[i].String
			case "file_size":
				f.Size, _ = strconv.ParseUint(vals[i].String, 10, 64)
			}
		}
		st.Files = append(st.Files, f)
	}
	return st, files.Err()
}

// ErrNoReload says the account may not rotate the binary log.
var ErrNoReload = errors.New("mariadb: FLUSH BINARY LOGS needs the RELOAD privilege")

// RotateBinlog asks the server to close its current binary log and start the
// next, so that what it holds becomes archivable.
//
// Optional and off by default (decided 2026-09-08): it touches the server and
// needs RELOAD. Without it, a quiet database keeps its newest changes in an
// open file that never fills, so the recovery point sits on one disk for as
// long as that takes. `koffr check` says how long, in figures, so the choice
// is made with the number in view rather than discovered after a loss.
func (c Config) RotateBinlog(ctx context.Context, ex executor.Executor) error {
	db, err := c.Connect(ctx, ex)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, "FLUSH BINARY LOGS"); err != nil {
		var me *mysql.MySQLError
		// 1227: access denied, you need the RELOAD privilege.
		if errors.As(err, &me) && me.Number == 1227 {
			return ErrNoReload
		}
		return fmt.Errorf("mariadb: rotate the binary log: %w", err)
	}
	return nil
}

// ReceiverArgs builds the command line that streams binary logs from the
// server as raw files, starting at the named log.
//
// -R --raw --stop-never is the documented archiving mode (EF-032): the client
// connects as a replica would, writes each log under its own name, and waits
// for more. The server id has to be stable and unique per source: two clients
// sharing one are disconnected in turn by the server, which looks like a link
// that keeps dropping and is nothing of the kind.
func (c Config) ReceiverArgs(sess *Session, spoolDir, startFile string, serverID uint32) []string {
	return []string{
		sess.DefaultsFile(),
		"--protocol=TCP",
		"--read-from-remote-server",
		"--raw",
		"--stop-never",
		"--stop-never-slave-server-id=" + strconv.FormatUint(uint64(serverID), 10),
		// A trailing separator makes it a directory: the client appends the
		// log's own name, so the spool holds files named as the server does.
		"--result-file=" + strings.TrimSuffix(spoolDir, "/") + "/",
		startFile,
	}
}

// masterStatus reads the file and position the server is writing. It owns its
// rows and closes them before returning: the pool behind db is a single
// connection, and a result set left open while the next statement waits for a
// connection is a deadlock, not a leak -- the first version of this hung
// `koffr check` for ten minutes.
func masterStatus(ctx context.Context, db *sql.DB) (file string, pos uint64, err error) {
	// SHOW MASTER STATUS has a variable number of columns across versions, so
	// the row is read by name rather than by position.
	rows, err := db.QueryContext(ctx, "SHOW MASTER STATUS")
	if err != nil {
		return "", 0, fmt.Errorf("mariadb: read the binary log position: %w", err)
	}
	defer func() { _ = rows.Close() }()
	cols, _ := rows.Columns()
	if rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return "", 0, fmt.Errorf("mariadb: read the binary log position: %w", err)
		}
		for i, col := range cols {
			switch strings.ToLower(col) {
			case "file":
				file = vals[i].String
			case "position":
				pos, _ = strconv.ParseUint(vals[i].String, 10, 64)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", 0, fmt.Errorf("mariadb: read the binary log position: %w", err)
	}
	return file, pos, nil
}
