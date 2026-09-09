package binlog_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/binlog"
	"github.com/Gu1llaum-3/koffr/internal/executor/local"
	"github.com/Gu1llaum-3/koffr/internal/manifest"
	"github.com/Gu1llaum-3/koffr/internal/restore"
	"github.com/Gu1llaum-3/koffr/internal/source"
	"github.com/Gu1llaum-3/koffr/internal/source/mariadb"
)

func count(t *testing.T, host string, port int, query string) int {
	t.Helper()
	db, err := sql.Open("mysql", adminDSN(host, port))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(), query).Scan(&n))
	return n
}

// The one this milestone exists for. A backup is taken; work happens; someone
// runs the DELETE; more work happens. The database is recovered to the second
// before the DELETE, on a fresh server: everything from before is there, the
// DELETE never happened, and what came after it is not there either. The
// source is never touched.
//
// It uses the real pieces end to end: the logical source takes the anchor with
// --master-data=2, the restore driver replays it onto the target, the
// supervisor archives the binary log, and the PITR plans and replays from the
// archive. Only the repository is in memory.
func TestPITR_RecoversToTheSecondBeforeTheDelete(t *testing.T) {
	skipUnlessReady(t)
	r := newRig(t)
	ex := local.New()

	// A clean slate on both servers, so an earlier test's rows do not leak in.
	execSQL(t, "DROP TABLE IF EXISTS orders", "DROP TABLE IF EXISTS churn")
	execSQL(t, "CREATE TABLE orders (id INT PRIMARY KEY, total DECIMAL(10,2)) ENGINE=InnoDB",
		"INSERT INTO orders SELECT seq, seq*1.5 FROM seq_1_to_1000")

	// The receiver runs throughout, as it would in production.
	sup := r.supervisor(t, shared.host, shared.port)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	defer func() { cancel(); <-done }()

	// 1. The backup: the logical source, exactly as `koffr backup` drives it.
	// Its Result is the anchor -- taken inside the dump's transaction.
	src, err := mariadb.NewLogical(r.config(shared.host, shared.port))
	require.NoError(t, err)
	stream, err := src.Open(t.Context(), ex, source.Request{Kind: source.KindLogical})
	require.NoError(t, err)
	dump, err := io.ReadAll(stream.Reader)
	require.NoError(t, err)
	require.NoError(t, stream.Close())
	anchor := stream.Result()
	require.NotEmpty(t, anchor.BinlogFile, "the anchor is what a recovery starts from")

	// 2. Life goes on: rows the recovery must bring back.
	time.Sleep(1500 * time.Millisecond) // --stop-datetime is cut at whole seconds
	execSQL(t, "INSERT INTO orders SELECT seq, seq*1.5 FROM seq_1001_to_1300")
	time.Sleep(1500 * time.Millisecond)
	before := time.Now()
	time.Sleep(1500 * time.Millisecond)

	// 3. The accident, and work after it that must not come back.
	execSQL(t, "DELETE FROM orders")
	execSQL(t, "INSERT INTO orders VALUES (999001, 1), (999002, 2), (999003, 3)")
	// Rotate so the file holding these events closes and can be archived. The
	// file to wait for is the one that was open just before the rotation: it
	// holds the DELETE, and everything the recovery needs is in it or before.
	open, err := r.config(shared.host, shared.port).Binlog(t.Context(), ex)
	require.NoError(t, err)
	require.NoError(t, r.config(shared.host, shared.port).RotateBinlog(t.Context(), ex))
	require.Eventually(t, func() bool {
		names, err := r.archive().Archived(t.Context())
		if err != nil {
			return false
		}
		return len(names) > 0 && names[len(names)-1].Seq >= mustSeq(t, open.File)
	}, 30*time.Second, 200*time.Millisecond, "the events never reached the archive")

	// Meanwhile the source is what it is: emptied, then three rows.
	require.Equal(t, 3, count(t, shared.host, shared.port, "SELECT COUNT(*) FROM orders"))

	// 4. Recover onto the fresh server, under another name, next to a
	// database that carries the source's name and must not be touched. The
	// events name the database they were written to; a replay that trusted
	// --database on the client would land in the namesake instead -- which,
	// on the source's own server, is the live database.
	copyName := database + "_copy"
	execOn(t, shared.targetHost, shared.targetPort,
		"DROP DATABASE IF EXISTS "+copyName,
		"DROP TABLE IF EXISTS orders",
		"CREATE TABLE orders (id INT PRIMARY KEY, total DECIMAL(10,2)) ENGINE=InnoDB",
		"INSERT INTO orders VALUES (1, 42)")
	targetCfg := r.config(shared.targetHost, shared.targetPort)
	driver := restore.MariaDB{Config: targetCfg}
	_, err = driver.Restore(t.Context(), ex, restore.MariaDBRequest{
		Database: copyName, Dump: bytesReader(dump),
	})
	require.NoError(t, err)
	inCopy := func(q string) int {
		return count(t, shared.targetHost, shared.targetPort, strings.ReplaceAll(q, "orders", copyName+".orders"))
	}
	require.Equal(t, 1000, inCopy("SELECT COUNT(*) FROM orders"),
		"the anchor alone brings back what the backup held")

	// 5. Then the log, to the second before the DELETE.
	pitr := restore.PITR{
		Config:  targetCfg,
		Archive: r.archive(),
		Opener:  r.opener,
		Workdir: t.TempDir(),
	}
	details := manifest.MariaDBDetails{BinlogFile: anchor.BinlogFile, BinlogPos: anchor.BinlogPos, GTID: anchor.GTID}
	target := restore.Target{Time: before}
	files, err := pitr.Plan(t.Context(), details, target)
	require.NoError(t, err)
	require.NotEmpty(t, files)
	// A client newer than the server may decode into SQL the server cannot
	// run; the preflight is what says so before anything is restored. On such
	// a rig the refusal is the correct outcome, and the rest cannot be tested.
	if err := pitr.Preflight(t.Context(), ex, details, files[0], copyName); err != nil {
		if errors.Is(err, restore.ErrReplayIncompatible) {
			t.Skipf("refused, correctly, on this client/server pair: %v", err)
		}
		require.NoError(t, err)
	}
	require.NoError(t, pitr.Replay(t.Context(), ex, details, target, files, database, copyName))

	// The verdict.
	assert.Equal(t, 1300, inCopy("SELECT COUNT(*) FROM orders"),
		"rows written after the backup and before the DELETE must be back")
	assert.Equal(t, 0, inCopy("SELECT COUNT(*) FROM orders WHERE id >= 999001"),
		"nothing from after the target may come back")
	assert.Equal(t, 1, count(t, shared.targetHost, shared.targetPort, "SELECT COUNT(*) FROM orders WHERE total = 42"),
		"the namesake next to the target is never touched by a recovery")
	assert.Equal(t, 3, count(t, shared.host, shared.port, "SELECT COUNT(*) FROM orders"),
		"the source is never touched by a recovery")
}

// A hole in the archive is a refusal, never a replay that stops short. A
// database rebuilt from a log with a file missing is a database that never
// existed, and "restored" would be the wrong word for it.
func TestPITR_RefusesToReplayAcrossAHole(t *testing.T) {
	skipUnlessReady(t)
	r := newRig(t)
	execSQL(t, "CREATE TABLE IF NOT EXISTS holes (id INT PRIMARY KEY) ENGINE=InnoDB")

	sup := r.supervisor(t, shared.host, shared.port)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	st, err := r.config(shared.host, shared.port).Binlog(t.Context(), local.New())
	require.NoError(t, err)
	anchor := manifest.MariaDBDetails{BinlogFile: st.File, BinlogPos: st.Position}
	anchorSeq := mustSeq(t, st.File)

	// Three rotations, so the archive holds the anchor's file and two after
	// it. The archive also holds every file from before the anchor -- the
	// shared server has been rotating for the other tests -- so the file to
	// knock out is chosen relative to the anchor, not from the middle of the
	// listing: a hole before the anchor is not on the replay's path, and the
	// first version of this test passed or failed with the test order.
	for range 3 {
		execSQL(t, "INSERT INTO holes VALUES (FLOOR(RAND()*1000000000))")
		require.NoError(t, r.config(shared.host, shared.port).RotateBinlog(t.Context(), local.New()))
	}
	require.Eventually(t, func() bool {
		names, err := r.archive().Archived(t.Context())
		return err == nil && len(names) > 0 && names[len(names)-1].Seq >= anchorSeq+2
	}, 30*time.Second, 200*time.Millisecond)
	cancel()
	<-done

	names, err := r.archive().Archived(t.Context())
	require.NoError(t, err)
	var middle binlog.Name
	for _, n := range names {
		if n.Seq == anchorSeq+1 {
			middle = n
		}
	}
	require.NotZero(t, middle.Seq, "the file after the anchor's must be archived")
	_, err = r.archive().Delete(t.Context(), middle)
	require.NoError(t, err)

	pitr := restore.PITR{Config: r.config(shared.targetHost, shared.targetPort), Archive: r.archive(), Opener: r.opener}
	_, err = pitr.Plan(t.Context(), anchor, restore.Target{Time: time.Now().Add(time.Hour)})
	require.ErrorIs(t, err, restore.ErrBinlogGap)
	assert.Contains(t, err.Error(), middle.String(), "the operator is told which file")
}

func TestPITR_RefusesATargetBeforeTheBackup(t *testing.T) {
	skipUnlessReady(t)
	r := newRig(t)
	pitr := restore.PITR{Config: r.config(shared.targetHost, shared.targetPort), Archive: r.archive(), Opener: r.opener}
	_, err := pitr.Plan(t.Context(),
		manifest.MariaDBDetails{BinlogFile: "binlog.000010", BinlogPos: 500},
		restore.Target{PositionFile: "binlog.000009", Position: 4})
	require.ErrorIs(t, err, restore.ErrTargetBeforeAnchor)
}

func TestParseTarget(t *testing.T) {
	tgt, err := restore.ParseTarget("2026-09-08T15:41:00Z")
	require.NoError(t, err)
	assert.Equal(t, 2026, tgt.Time.Year())

	tgt, err = restore.ParseTarget("mariadb-bin.000042:1234")
	require.NoError(t, err)
	assert.Equal(t, uint64(1234), tgt.Position)
	assert.Equal(t, "mariadb-bin.000042", tgt.PositionFile)

	for _, bad := range []string{"", "yesterday", "1234", "notalog:1234"} {
		_, err := restore.ParseTarget(bad)
		assert.Error(t, err, bad)
	}
}

func mustSeq(t *testing.T, name string) uint64 {
	t.Helper()
	n, err := parseName(name)
	require.NoError(t, err)
	return n
}

// The preflight runs the decoder's session preamble on the target first. A
// variable the target does not know is exactly the failure a newer client
// produces, and it must surface as a refusal with nothing restored.
func TestPITR_RefusesAPreambleTheTargetCannotRun(t *testing.T) {
	skipUnlessReady(t)
	r := newRig(t)
	pitr := restore.PITR{Config: r.config(shared.targetHost, shared.targetPort), Archive: r.archive(), Opener: r.opener}
	err := pitr.TryPreamble(t.Context(), local.New(), database,
		[]string{"SET @@session.foreign_key_checks=1, @@session.koffr_no_such_variable=0"})
	require.ErrorIs(t, err, restore.ErrReplayIncompatible)
	assert.Contains(t, err.Error(), "koffr_no_such_variable", "the operator is told which variable")
	assert.Contains(t, err.Error(), "Nothing was restored")

	require.NoError(t, pitr.TryPreamble(t.Context(), local.New(), database,
		[]string{"SET @@session.foreign_key_checks=1, @@session.sql_auto_is_null=0"}),
		"a preamble the target knows passes")
}
