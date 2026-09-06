package e2e_test

// PD-001 for MariaDB: a backup restores on a machine that has never heard of
// Koffr, using only the commands the generated RESTORE.md contains.
//
// It matters more here than anywhere else. The MariaDB procedure was written
// during M1 and, until this test, had never been executed by anyone -- the same
// state the PostgreSQL one was in when it turned out to have been wrong since
// the day it was written.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/cli"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

const (
	mariaUser     = "root"
	mariaPass     = "restore-me-please"
	mariaDatabase = "shop"
	mariaRestored = "restored"
)

var mariaWanted = map[string]int{"orders": 40, "items": 120}

const mariaChecksum = `SELECT MD5(GROUP_CONCAT(CONCAT(id, ':', total) ORDER BY id SEPARATOR ',')) FROM orders`

func TestRestoreMariaDBWithoutKoffr(t *testing.T) {
	unavailable := testutil.EnsureDockerHost()
	skip, fatal := testutil.SkipOrFailWithoutDocker(unavailable)
	if fatal != "" {
		t.Fatal(fatal)
	}
	if skip {
		t.Skip("no container runtime: " + unavailable)
	}
	// CT-001: the backup half runs here and needs the client.
	if !hasMariaDumpBinary() {
		testutil.SkipOrFailWithoutTool(t, "mariadb-dump", "the backup half of this test runs here")
	}

	ctx := context.Background()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")

	source := startMariaSource(t, ctx)
	seedMaria(t, ctx, source)

	identity, cfgPath := writeMariaConfig(t, dir, repo, mariaSourcePort(t, ctx, source))

	// Koffr's only appearance. Everything after this happens without it.
	code, out, errOut := runKoffr(t, "--config", cfgPath, "backup", "shop")
	require.Equal(t, cli.ExitOK, code, "stdout: %s stderr: %s", out, errOut)

	doc, prefix := readRestoreDoc(t, repo)
	t.Logf("RESTORE.md:\n%s", doc)

	commands := shellBlocks(doc)
	require.NotEmpty(t, commands, "a procedure with no commands is not a procedure")

	// P-006 applies to any decompressor at the head of a pipe, not only to the
	// PostgreSQL one: a document whose commands set pipefail turns a success
	// into a failure.
	for _, cmd := range commands {
		assert.NotContains(t, cmd, "pipefail")
	}

	// A bare --password makes the client ask on the terminal, and the terminal
	// is carrying the dump. The document must not use it.
	for _, cmd := range commands {
		assert.NotRegexp(t, `--password(\s|$)`, cmd,
			"a bare --password cannot work in a pipeline; the credentials belong in a file")
	}

	target := startBareMariaMachine(t, ctx)
	loadMariaBackup(t, ctx, target, filepath.Join(repo, prefix), identity)

	for i, cmd := range commands {
		// The three substitutions the document itself asks for.
		cmd = strings.ReplaceAll(cmd, "USER", mariaUser)
		cmd = strings.ReplaceAll(cmd, "PASSWORD", mariaPass)
		cmd = strings.ReplaceAll(cmd, "DBNAME", mariaRestored)
		// The grants step reports an error for every account the server already
		// has and exits non-zero because of it; the document says so.
		allowFailure := strings.Contains(cmd, "--force")
		runInMariaContainer(t, ctx, target, fmt.Sprintf("step-%02d", i), cmd, allowFailure)
	}

	for table, want := range mariaWanted {
		got := mariaInt(t, ctx, target, mariaDatabase, "SELECT COUNT(*) FROM "+table)
		assert.Equal(t, want, got, "table %s came back with the wrong number of rows", table)
	}

	// Row counts alone would pass on a dump that lost every value.
	assert.Equal(t,
		mariaString(t, ctx, source, mariaDatabase, mariaChecksum),
		mariaString(t, ctx, target, mariaDatabase, mariaChecksum),
		"the restored data does not match the original")

	// Accounts live in the server, not in the database, so they arrive only if
	// grants.sql was replayed. Without them the restored database has grantees
	// that do not exist.
	assert.Equal(t, 1, mariaInt(t, ctx, target, "mysql",
		"SELECT COUNT(*) FROM mysql.user WHERE user = 'shop_reader'"),
		"the grants sidecar did not arrive, so the restored database's privileges are broken")

	// And the one thing that must never arrive.
	assert.NotContains(t, doc, "IDENTIFIED BY")
}
