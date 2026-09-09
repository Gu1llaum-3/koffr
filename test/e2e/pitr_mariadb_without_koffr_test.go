package e2e_test

// PD-001 for the point-in-time recovery: from a backup and its archived binary
// logs, a database is brought back to the second before a DELETE, on a machine
// that has never heard of Koffr, using only the commands RESTORE.md contains.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/cli"
	"github.com/Gu1llaum-3/koffr/internal/manifest"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

func TestPointInTimeRecoveryWithoutKoffr(t *testing.T) {
	unavailable := testutil.EnsureDockerHost()
	skip, fatal := testutil.SkipOrFailWithoutDocker(unavailable)
	if fatal != "" {
		t.Fatal(fatal)
	}
	if skip {
		t.Skip("no container runtime: " + unavailable)
	}
	if !hasMariaDumpBinary() {
		testutil.SkipOrFailWithoutTool(t, "mariadb-dump", "the backup half of this test runs here")
	}
	testutil.SkipOrFailWithoutTool(t, "mariadb-binlog", "the archiving half of this test runs here")

	ctx := context.Background()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	spool := filepath.Join(dir, "spool")

	source := startMariaSourceWith(t, ctx, "--log-bin=binlog", "--server-id=1", "--max-binlog-size=65536")
	seedMaria(t, ctx, source)
	port := mariaSourcePort(t, ctx, source)

	identity, cfgPath := writeMariaConfigWith(t, dir, repo, port,
		"    schedule: \"@every 1h\"\n    binlog:\n      enabled: true\n",
		"binlog:\n  spool_dir: "+spool+"\n")

	// The daemon runs in the background for the whole test, archiving the
	// binary log as it would in production. It is the only Koffr here; the
	// restore side never sees it.
	dctx, stop := context.WithCancel(ctx)
	daemonDone := make(chan int, 1)
	// The daemon writes these from its own goroutine while the test reads
	// them for its failure messages; a plain strings.Builder is a data race
	// the -race run of make ci was the first to see.
	var daemonOut, daemonErr lockedBuffer
	go func() {
		daemonDone <- cli.Run(dctx, []string{"--config", cfgPath, "schedule"}, cli.Streams{Out: &daemonOut, Err: &daemonErr})
		close(daemonDone)
	}()
	// Stopping is idempotent: the body stops the daemon once the archive is
	// complete, and the cleanup stops it again on the failure paths. Waiting on
	// a channel that was already drained is how the first version of this test
	// hung for ten minutes.
	stopDaemon := func() {
		stop()
		select {
		case <-daemonDone:
		case <-time.After(20 * time.Second):
			t.Errorf("the daemon did not stop within 20s of being asked; stderr:\n%s", daemonErr.String())
		}
	}
	t.Cleanup(stopDaemon)
	// Ready means the receiver is up, which the daemon says on stderr. Taking a
	// backup a millisecond after starting the daemon on a catalog that does not
	// exist yet has both of them creating it at once, and SQLite answers
	// "database is locked" to the one that arrives second -- a race no
	// operator runs, and one the -race build of make ci hit twice.
	require.Eventually(t, func() bool {
		return strings.Contains(daemonErr.String(), "archiving the binary log of shop")
	}, 15*time.Second, 100*time.Millisecond, "the daemon never started the receiver; stderr:\n%s", daemonErr.String())
	t.Log("phase: daemon started")

	// 1. The backup: the anchor.
	code, out, errOut := runKoffr(t, "--config", cfgPath, "backup", "shop")
	require.Equal(t, cli.ExitOK, code, "stdout: %s stderr: %s", out, errOut)
	t.Log("phase: backup taken")

	doc, prefix := readRestoreDoc(t, repo)
	mf, err := os.Open(filepath.Join(repo, prefix, "manifest.json"))
	require.NoError(t, err)
	m, err := manifest.Decode(mf)
	require.NoError(t, mf.Close())
	require.NoError(t, err)
	require.NotNil(t, m.MariaDB, "a backup taken with the log on must carry its anchor")
	anchorFile := m.MariaDB.BinlogFile

	// 2. Work after the backup, which the recovery must bring back. The log is
	// rotated first so that this work lands in the file *after* the anchor's:
	// the replay then has to cross a file boundary, which is where the claim
	// "the start position applies to the first file only" is either true or
	// costs someone their data.
	runMariaSQL(t, ctx, source, mariaDatabase, "FLUSH BINARY LOGS")
	time.Sleep(1500 * time.Millisecond)
	runMariaSQL(t, ctx, source, mariaDatabase, "INSERT INTO orders SELECT seq, seq * 2 FROM seq_41_to_60")
	time.Sleep(1500 * time.Millisecond)
	target := time.Now().UTC()
	time.Sleep(1500 * time.Millisecond)

	// 3. The accident, and work after it that must not come back.
	runMariaSQL(t, ctx, source, mariaDatabase, "DELETE FROM items")
	runMariaSQL(t, ctx, source, mariaDatabase, "INSERT INTO orders VALUES (999, 1)")
	runMariaSQL(t, ctx, source, mariaDatabase, "FLUSH BINARY LOGS")
	t.Log("phase: delete done, log rotated")

	// Everything the server has closed must be in the archive before the
	// daemon stops: the anchor's file and the one after it, which holds the
	// DELETE.
	binlogDir := filepath.Join(repo, "sources", "shop", "binlog")
	require.Eventually(t, func() bool {
		entries, err := os.ReadDir(binlogDir)
		if err != nil {
			return false
		}
		for _, e := range entries {
			name := strings.TrimSuffix(e.Name(), ".zst.age")
			if name != e.Name() && name > anchorFile {
				return true
			}
		}
		return false
	}, 30*time.Second, 250*time.Millisecond, "the binary log never reached the repository; daemon stderr:\n%s", daemonErr.String())
	t.Log("phase: archive complete, stopping daemon")
	stopDaemon()
	t.Log("phase: daemon stopped")

	t.Logf("RESTORE.md:\n%s", doc)
	commands := shellBlocks(doc)
	require.NotEmpty(t, commands)
	for _, cmd := range commands {
		assert.NotContains(t, cmd, "pipefail")
	}
	assert.Contains(t, doc, "TZ=UTC", "the time zone trap has to be in the document, not only in the code")

	// The bare machine gets the backup and the archived binary logs the
	// document asks for: from the backup's own file onwards. An operator who
	// hands mariadb-binlog an earlier file replays the tables' creation a
	// second time, because --start-position applies to the first file given;
	// the first version of this test did exactly that and the replay failed on
	// "Table 'orders' already exists".
	t.Log("phase: starting the bare machine")
	bare := startBareMariaMachine(t, ctx)
	loadMariaBackup(t, ctx, bare, filepath.Join(repo, prefix), identity)
	files := loadBinlogs(t, ctx, bare, binlogDir, anchorFile)
	require.GreaterOrEqual(t, len(files), 2, "the replay must cross a file boundary")

	plain := make([]string, len(files))
	for i, f := range files {
		plain[i] = strings.TrimSuffix(f, ".zst.age")
	}
	for i, cmd := range commands {
		cmd = strings.ReplaceAll(cmd, "USER", mariaUser)
		cmd = strings.ReplaceAll(cmd, "PASSWORD", mariaPass)
		cmd = strings.ReplaceAll(cmd, "DBNAME", mariaRestored)
		cmd = strings.ReplaceAll(cmd, "BINLOG_FILES", strings.Join(files, " "))
		cmd = strings.ReplaceAll(cmd, "BINLOG_PLAIN", strings.Join(plain, " "))
		cmd = strings.ReplaceAll(cmd, "TARGET", target.Format("2006-01-02 15:04:05"))
		allowFailure := strings.Contains(cmd, "--force")
		runInMariaContainer(t, ctx, bare, fmt.Sprintf("step-%02d", i), cmd, allowFailure)
	}

	// The verdict: the rows written before the target are back, the DELETE
	// never happened, what came after it is absent, and the source is what it
	// is.
	assert.Equal(t, 60, mariaInt(t, ctx, bare, mariaDatabase, "SELECT COUNT(*) FROM orders"),
		"the twenty orders written after the backup must be back")
	assert.Equal(t, 120, mariaInt(t, ctx, bare, mariaDatabase, "SELECT COUNT(*) FROM items"),
		"the DELETE happened after the target, so the items must all be there")
	assert.Equal(t, 0, mariaInt(t, ctx, bare, mariaDatabase, "SELECT COUNT(*) FROM orders WHERE id = 999"),
		"nothing from after the target may come back")
	assert.Equal(t, 0, mariaInt(t, ctx, source, mariaDatabase, "SELECT COUNT(*) FROM items"),
		"the source is never touched")
}
