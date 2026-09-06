// Package sqlite stores the catalog in a local file.
//
// A cache, not the truth (DEC-004). The repository is authoritative, this is
// what makes listing a backup an operation against a local file rather than
// against object storage. Losing it costs a rebuild (EF-142), never data, and
// every choice here follows from that: refuse to guess, and let the operator
// delete the file when guessing would be the alternative.
//
// Two constraints inherited from the decision:
//
//   - One writer. A second scheduler would run every job twice, so serialising
//     access is not a limitation to work around, it is the shape we want.
//   - Never on NFS (ENF-033). SQLite's file locking is unreliable there and the
//     database will be corrupted. Configuration validation checks for it where
//     it can be detected; this package cannot.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure Go, so CGO_ENABLED=0 holds (ENF-030)

	"github.com/Gu1llaum-3/koffr/internal/catalog"
)

// SchemaVersion is the layout this build understands, kept in SQLite's own
// user_version pragma rather than a table of our own.
const SchemaVersion = 2

// migrations are applied in order; index i takes the schema from version i to
// i+1. They are never edited once released, only appended to.
var migrations = []string{
	`
CREATE TABLE backups (
    id           TEXT PRIMARY KEY,
    source_id    TEXT NOT NULL,
    kind         TEXT NOT NULL,
    parent_id    TEXT NOT NULL DEFAULT '',
    destination  TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL,
    started_at   INTEGER NOT NULL DEFAULT 0,
    finished_at  INTEGER NOT NULL DEFAULT 0,
    size_bytes   INTEGER NOT NULL DEFAULT 0,
    manifest_key TEXT NOT NULL DEFAULT '',
    -- Denormalised from the manifest so retention can walk chains and enforce
    -- the WAL guard without fetching manifests from storage (EF-063).
    start_lsn    TEXT NOT NULL DEFAULT '',
    end_lsn      TEXT NOT NULL DEFAULT '',
    binlog_file  TEXT NOT NULL DEFAULT '',
    binlog_pos   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_backups_source_time ON backups(source_id, started_at DESC);
CREATE INDEX idx_backups_parent      ON backups(parent_id);

CREATE TABLE jobs (
    id           TEXT PRIMARY KEY,
    source_id    TEXT NOT NULL,
    kind         TEXT NOT NULL,
    trigger      TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL,
    error_class  TEXT NOT NULL DEFAULT '',
    error_detail TEXT NOT NULL DEFAULT '',
    started_at   INTEGER NOT NULL DEFAULT 0,
    finished_at  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_jobs_source_time ON jobs(source_id, started_at DESC);

CREATE TABLE verifications (
    id          TEXT PRIMARY KEY,
    backup_id   TEXT NOT NULL,
    tier        INTEGER NOT NULL DEFAULT 0,
    status      TEXT NOT NULL,
    report      BLOB,
    started_at  INTEGER NOT NULL DEFAULT 0,
    finished_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_verifications_backup ON verifications(backup_id);
`,
	// v2: a backup exists once per destination it was written to (EF-044).
	//
	// The same backup on two destinations is two rows, because retention is
	// per destination: keeping seven days locally and twelve months offsite is
	// the whole point of writing to both. One row with a list would make
	// "delete this backup from main" a rewrite of a column rather than a
	// deletion, and every query would have to remember that.
	//
	// SQLite cannot alter a primary key, so the table is rebuilt. Existing rows
	// carry their destination already.
	`
CREATE TABLE backups_v2 (
    id           TEXT NOT NULL,
    source_id    TEXT NOT NULL,
    kind         TEXT NOT NULL,
    parent_id    TEXT NOT NULL DEFAULT '',
    destination  TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL,
    started_at   INTEGER NOT NULL DEFAULT 0,
    finished_at  INTEGER NOT NULL DEFAULT 0,
    size_bytes   INTEGER NOT NULL DEFAULT 0,
    manifest_key TEXT NOT NULL DEFAULT '',
    start_lsn    TEXT NOT NULL DEFAULT '',
    end_lsn      TEXT NOT NULL DEFAULT '',
    binlog_file  TEXT NOT NULL DEFAULT '',
    binlog_pos   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (id, destination)
);

INSERT INTO backups_v2 SELECT * FROM backups;
DROP TABLE backups;
ALTER TABLE backups_v2 RENAME TO backups;

CREATE INDEX idx_backups_source_time ON backups(source_id, started_at DESC);
CREATE INDEX idx_backups_parent      ON backups(parent_id);
CREATE INDEX idx_backups_id          ON backups(id);
`,
}

// Store is a catalog in a SQLite file.
type Store struct {
	db        *sql.DB
	closeOnce sync.Once
	closeErr  error
}

// Open opens or creates a catalog and brings its schema up to date.
// The context covers the migration, which is the part that can wait: SQLite
// serialises writers, so opening a catalog another Koffr is migrating blocks
// until it finishes. A backup job that was cancelled should not still be
// waiting on that.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("catalog/sqlite: no path given")
	}

	// busy_timeout covers the moments two goroutines contend anyway; WAL keeps
	// a reader from blocking the writer; foreign_keys is off by default in
	// SQLite and we would rather it were not.
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("catalog/sqlite: open %s: %w", path, err)
	}

	// One connection, deliberately. DEC-004 already limits Koffr to a single
	// writer, and a pool would only add SQLITE_BUSY retries to a workload of a
	// few thousand rows.
	db.SetMaxOpenConns(1)

	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	// The file holds no backup content, but it says which databases exist and
	// when each was last backed up.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("catalog/sqlite: secure %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {

	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("catalog/sqlite: read schema version: %w", err)
	}
	if version > len(migrations) {
		return fmt.Errorf(
			"catalog/sqlite: the catalog is at schema version %d, newer than the %d this build "+
				"understands; reading it would silently ignore what the newer version added. "+
				"Delete the file and run `koffr catalog sync` to rebuild it from the repository",
			version, len(migrations))
	}

	for i := version; i < len(migrations); i++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("catalog/sqlite: begin migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("catalog/sqlite: apply migration %d: %w", i+1, err)
		}
		// user_version takes no parameter binding, hence the concatenation of a
		// value that comes from a loop index and nowhere else.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("catalog/sqlite: record migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("catalog/sqlite: commit migration %d: %w", i+1, err)
		}
	}
	return nil
}

// Close releases the file.
func (s *Store) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.db.Close() })
	return s.closeErr
}

// RecordBackup writes or replaces a backup.
//
// Replacing rather than inserting is what a job needs: a backup is recorded
// when it starts and again when it finishes, and a retry records it a third
// time. Two rows for one backup would have retention count it twice and delete
// something to get back under the limit.
func (s *Store) RecordBackup(ctx context.Context, b catalog.Backup) error {
	// MariaDB reports a binlog position as unsigned; SQLite integers are
	// signed. Real positions are bounded by max_binlog_size and never come
	// close, so this cannot happen -- but a truncated position restores to the
	// wrong point in time, which is not a failure to discover during a restore.
	binlogPos, err := binlogPosOf(b)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO backups (id, source_id, kind, parent_id, destination, status,
		                     started_at, finished_at, size_bytes, manifest_key,
		                     start_lsn, end_lsn, binlog_file, binlog_pos)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id, destination) DO UPDATE SET
			source_id=excluded.source_id, kind=excluded.kind, parent_id=excluded.parent_id,
			status=excluded.status,
			started_at=excluded.started_at, finished_at=excluded.finished_at,
			size_bytes=excluded.size_bytes, manifest_key=excluded.manifest_key,
			start_lsn=excluded.start_lsn, end_lsn=excluded.end_lsn,
			binlog_file=excluded.binlog_file, binlog_pos=excluded.binlog_pos`,
		string(b.ID), b.SourceID, b.Kind, string(b.ParentID), b.Destination, string(b.Status),
		toUnix(b.StartedAt), toUnix(b.FinishedAt), b.SizeBytes, b.ManifestKey,
		b.StartLSN, b.EndLSN, b.BinlogFile, binlogPos)
	if err != nil {
		return fmt.Errorf("catalog/sqlite: record backup %s: %w", b.ID, err)
	}
	return nil
}

// RecordJob writes or replaces a job.
func (s *Store) RecordJob(ctx context.Context, j catalog.Job) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO jobs (id, source_id, kind, trigger, status, error_class,
		                  error_detail, started_at, finished_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			source_id=excluded.source_id, kind=excluded.kind, trigger=excluded.trigger,
			status=excluded.status, error_class=excluded.error_class,
			error_detail=excluded.error_detail,
			started_at=excluded.started_at, finished_at=excluded.finished_at`,
		j.ID, j.SourceID, j.Kind, string(j.Trigger), string(j.Status),
		string(j.ErrorClass), j.ErrorDetail, toUnix(j.StartedAt), toUnix(j.FinishedAt))
	if err != nil {
		return fmt.Errorf("catalog/sqlite: record job %s: %w", j.ID, err)
	}
	return nil
}

// RecordVerification writes or replaces a verification run.
func (s *Store) RecordVerification(ctx context.Context, v catalog.Verification) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO verifications (id, backup_id, tier, status, report, started_at, finished_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			backup_id=excluded.backup_id, tier=excluded.tier, status=excluded.status,
			report=excluded.report, started_at=excluded.started_at,
			finished_at=excluded.finished_at`,
		v.ID, string(v.BackupID), v.Tier, string(v.Status), v.Report,
		toUnix(v.StartedAt), toUnix(v.FinishedAt))
	if err != nil {
		return fmt.Errorf("catalog/sqlite: record verification %s: %w", v.ID, err)
	}
	return nil
}

const backupColumns = `id, source_id, kind, parent_id, destination, status,
	started_at, finished_at, size_bytes, manifest_key,
	start_lsn, end_lsn, binlog_file, binlog_pos`

// ListBackups returns matching backups, newest first.
func (s *Store) ListBackups(ctx context.Context, f catalog.BackupFilter) ([]catalog.Backup, error) {
	var where []string
	var args []any

	if f.SourceID != "" {
		where, args = append(where, "source_id = ?"), append(args, f.SourceID)
	}
	if f.Kind != "" {
		where, args = append(where, "kind = ?"), append(args, f.Kind)
	}
	if f.Destination != "" {
		where, args = append(where, "destination = ?"), append(args, f.Destination)
	}
	if f.Status != "" {
		where, args = append(where, "status = ?"), append(args, string(f.Status))
	}
	// Half-open window: Since is inclusive, Until is not. Retention walks these
	// boundaries, and an off-by-one deletes a backup meant to be kept.
	if !f.Since.IsZero() {
		where, args = append(where, "started_at >= ?"), append(args, toUnix(f.Since))
	}
	if !f.Until.IsZero() {
		where, args = append(where, "started_at < ?"), append(args, toUnix(f.Until))
	}

	// The fragments joined here are constants declared a few lines above, and
	// every value goes through a bound parameter. gosec cannot see that, so the
	// exemption is stated rather than the linter disabled.
	query := "SELECT " + backupColumns + " FROM backups"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ") //nolint:gosec // fragments are constants; values are bound
	}
	// Newest first: the CLI shows recent backups and retention starts from the
	// newest and works back. The id tiebreak keeps the order stable.
	query += " ORDER BY started_at DESC, id DESC"
	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("catalog/sqlite: list backups: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []catalog.Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog/sqlite: list backups: %w", err)
	}
	return out, nil
}

// Chain returns a backup and every ancestor it rests on, newest first.
//
// EF-062 depends on this: retention asks before deleting, because a full backup
// an incremental needs cannot go. A chain that cannot be proven whole is an
// error rather than a short list, since a caller reading "nothing depends on
// this" would delete exactly the wrong thing.
func (s *Store) Chain(ctx context.Context, id catalog.ID) ([]catalog.Backup, error) {
	var chain []catalog.Backup
	seen := map[catalog.ID]bool{}

	for current := id; current != ""; {
		if seen[current] {
			return nil, fmt.Errorf("catalog/sqlite: backup %s is part of a parent cycle", current)
		}
		seen[current] = true

		row := s.db.QueryRowContext(ctx,
			"SELECT "+backupColumns+" FROM backups WHERE id = ?", string(current))
		b, err := scanBackup(row)
		if errors.Is(err, sql.ErrNoRows) {
			if current == id {
				return nil, fmt.Errorf("catalog/sqlite: no backup %s", current)
			}
			return nil, fmt.Errorf(
				"catalog/sqlite: backup %s names parent %s, which is not in the catalog; "+
					"the chain cannot be proven complete. Run `koffr catalog sync` to rebuild "+
					"from the repository", chain[len(chain)-1].ID, current)
		}
		if err != nil {
			return nil, err
		}
		chain = append(chain, b)
		current = b.ParentID
	}
	return chain, nil
}

// Overview answers the question monitoring actually asks: how old is the last
// thing we know to be good (EF-134).
func (s *Store) Overview(ctx context.Context) (catalog.Overview, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH sources AS (
			SELECT source_id FROM backups
			UNION
			SELECT source_id FROM jobs
		),
		latest_job AS (
			SELECT j.source_id, j.status, j.error_class, j.finished_at, j.started_at
			FROM jobs j
			JOIN (SELECT source_id, MAX(started_at) AS started_at
			      FROM jobs GROUP BY source_id) m
			  ON m.source_id = j.source_id AND m.started_at = j.started_at
		)
		SELECT
			s.source_id,
			COALESCE((SELECT MAX(finished_at) FROM backups
			          WHERE source_id = s.source_id AND status = 'completed'), 0),
			COALESCE((SELECT MAX(v.finished_at) FROM verifications v
			          JOIN backups b ON b.id = v.backup_id
			          WHERE b.source_id = s.source_id AND v.status = 'completed'), 0),
			COALESCE((SELECT COUNT(*) FROM backups
			          WHERE source_id = s.source_id AND status = 'completed'), 0),
			COALESCE((SELECT SUM(size_bytes) FROM backups
			          WHERE source_id = s.source_id AND status = 'completed'), 0),
			COALESCE((SELECT MIN(started_at) FROM backups
			          WHERE source_id = s.source_id AND status = 'completed'), 0),
			COALESCE((SELECT status FROM latest_job WHERE source_id = s.source_id), ''),
			COALESCE((SELECT error_class FROM latest_job WHERE source_id = s.source_id), ''),
			COALESCE((SELECT finished_at FROM latest_job WHERE source_id = s.source_id), 0)
		FROM sources s
		ORDER BY s.source_id`)
	if err != nil {
		return catalog.Overview{}, fmt.Errorf("catalog/sqlite: overview: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var overview catalog.Overview
	for rows.Next() {
		var (
			so                catalog.SourceOverview
			lastSuccess       int64
			lastVerified      int64
			oldestRestorable  int64
			latestJobStatus   string
			latestJobClass    string
			latestJobFinished int64
		)
		if err := rows.Scan(&so.SourceID, &lastSuccess, &lastVerified,
			&so.BackupCount, &so.TotalSizeBytes, &oldestRestorable,
			&latestJobStatus, &latestJobClass, &latestJobFinished); err != nil {
			return catalog.Overview{}, fmt.Errorf("catalog/sqlite: overview: %w", err)
		}

		so.LastSuccessfulAt = fromUnix(lastSuccess)
		so.LastVerifiedAt = fromUnix(lastVerified)
		so.OldestRestorableAt = fromUnix(oldestRestorable)
		// Only the most recent job counts. A failure followed by a successful
		// retry is not a standing failure, and reporting it as one would keep
		// an alert lit over something already fixed.
		if catalog.Status(latestJobStatus) == catalog.StatusFailed {
			so.LastFailureAt = fromUnix(latestJobFinished)
			so.LastFailureClass = catalog.ErrorClass(latestJobClass)
		}
		overview.Sources = append(overview.Sources, so)
	}
	if err := rows.Err(); err != nil {
		return catalog.Overview{}, fmt.Errorf("catalog/sqlite: overview: %w", err)
	}
	return overview, nil
}

// scanner is what QueryRow and Rows have in common.
type scanner interface{ Scan(dest ...any) error }

func scanBackup(row scanner) (catalog.Backup, error) {
	var (
		b                     catalog.Backup
		id, parentID, status  string
		startedAt, finishedAt int64
		binlogPos             int64
	)
	if err := row.Scan(&id, &b.SourceID, &b.Kind, &parentID, &b.Destination, &status,
		&startedAt, &finishedAt, &b.SizeBytes, &b.ManifestKey,
		&b.StartLSN, &b.EndLSN, &b.BinlogFile, &binlogPos); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return catalog.Backup{}, err
		}
		return catalog.Backup{}, fmt.Errorf("catalog/sqlite: read backup: %w", err)
	}

	b.ID = catalog.ID(id)
	b.ParentID = catalog.ID(parentID)
	b.Status = catalog.Status(status)
	b.StartedAt = fromUnix(startedAt)
	b.FinishedAt = fromUnix(finishedAt)
	if binlogPos < 0 {
		return catalog.Backup{}, fmt.Errorf(
			"catalog/sqlite: backup %s has a negative binlog position %d, so the catalog is corrupt; "+
				"run `koffr catalog sync` to rebuild it from the repository", id, binlogPos)
	}
	b.BinlogPos = uint64(binlogPos)
	return b, nil
}

// Times are stored as Unix nanoseconds.
//
// Not as text: a formatted timestamp carries a zone, and a catalog written in
// one zone and read in another would misreport how stale a backup is and shift
// every retention boundary. An instant has no zone to get wrong.
func toUnix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func fromUnix(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

var _ catalog.MetadataStore = (*Store)(nil)

// binlogPosOf narrows a binlog position to what SQLite can hold.
//
// MariaDB reports it unsigned; SQLite integers are signed. Real positions are
// bounded by max_binlog_size and never come close -- but a truncated position
// restores to the wrong point in time, and that is not a thing to discover
// during a restore.
func binlogPosOf(b catalog.Backup) (int64, error) {
	if b.BinlogPos > math.MaxInt64 {
		return 0, fmt.Errorf("catalog/sqlite: backup %s has binlog position %d, which does not fit",
			b.ID, b.BinlogPos)
	}
	return int64(b.BinlogPos), nil
}

// ForgetBackup removes a backup's row for one destination.
//
// One destination, not the backup: a copy pruned from local storage is still
// on the offsite one, and forgetting both would hide a backup that exists.
//
// Idempotent: retention may be re-run after an interrupted pass, and a backup
// already forgotten is the state that pass was trying to reach.
func (s *Store) ForgetBackup(ctx context.Context, id catalog.ID, destination string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM backups WHERE id = ? AND destination = ?`,
		string(id), destination); err != nil {
		return fmt.Errorf("catalog/sqlite: forget backup %s on %s: %w", id, destination, err)
	}
	return nil
}
