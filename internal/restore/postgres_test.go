package restore_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/executor/local"
	"github.com/Gu1llaum-3/koffr/internal/restore"
	"github.com/Gu1llaum-3/koffr/internal/source/postgres"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

// fakeTools puts stand-ins for the PostgreSQL client binaries in a bin_dir and
// records how they were called.
//
// They are real programs run by the real executor rather than a mocked
// Executor: what is being tested is the command Koffr builds and the plumbing
// around it, and a mock would only confirm that the code calls what the test
// told it to expect.
func fakeTools(t *testing.T, exitCode int, stderr string) (binDir, recordDir string) {
	t.Helper()
	binDir, recordDir = t.TempDir(), t.TempDir()

	for _, name := range []string{"pg_restore", "psql"} {
		// Absolute paths throughout: the environment Koffr hands a client
		// binary carries no PATH, deliberately, so that pg_dumpall finds its
		// sibling pg_dump by path rather than by luck. A fake that needed PATH
		// would be testing a laxer environment than the real one.
		script := fmt.Sprintf(`#!/bin/sh
rec=%q/%s
printf '%%s\n' "$@" > "$rec.args"
/usr/bin/env > "$rec.env"
/bin/cat > "$rec.stdin"
printf '%%s' %q >&2
exit %d
`, recordDir, name, stderr, exitCode)
		require.NoError(t, os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o700))
	}
	return binDir, recordDir
}

func recorded(t *testing.T, recordDir, tool, what string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(recordDir, tool+"."+what))
	require.NoError(t, err, "%s was never run", tool)
	return string(b)
}

func targetConfig(t *testing.T, binDir string) postgres.Config {
	t.Helper()
	return postgres.Config{
		Host: "db.invalid", Port: 5432,
		User: "koffr", Password: testutil.SecretSentinel,
		Database: "template1", SSLMode: "verify-full",
		BinDir: binDir, ToolRunner: local.New(),
	}
}

func TestPostgres_RestoresIntoTheRequestedDatabase(t *testing.T) {
	binDir, rec := fakeTools(t, 0, "")
	r := restore.Postgres{Config: targetConfig(t, binDir)}

	_, err := r.Restore(context.Background(), local.New(), restore.PostgresRequest{
		Database: "shop_restored",
		Dump:     strings.NewReader("PGDMP archive bytes"),
	})
	require.NoError(t, err)

	args := recorded(t, rec, "pg_restore", "args")
	assert.Contains(t, args, "--dbname=shop_restored",
		"the target is the operator's choice, never the source's own name")
	assert.Equal(t, "PGDMP archive bytes", recorded(t, rec, "pg_restore", "stdin"))
}

// ENF-021, at the one place a password is genuinely needed by a child process.
func TestPostgres_NoCredentialInArgumentsOrEnvironment(t *testing.T) {
	binDir, rec := fakeTools(t, 0, "")
	r := restore.Postgres{Config: targetConfig(t, binDir)}

	_, err := r.Restore(context.Background(), local.New(), restore.PostgresRequest{
		Database: "shop",
		Globals:  strings.NewReader("CREATE ROLE app;"),
		Dump:     strings.NewReader("PGDMP"),
	})
	require.NoError(t, err)

	for _, tool := range []string{"pg_restore", "psql"} {
		testutil.AssertNoSecretLeak(t,
			recorded(t, rec, tool, "args"),
			recorded(t, rec, tool, "env"))
		assert.Contains(t, recorded(t, rec, tool, "env"), "PGPASSFILE=",
			"the password reaches the tool through a 0600 file, not an argument")
	}
}

// pg_restore --jobs needs to seek, so it refuses a pipe. Accepting the flag and
// dropping it would turn a restore an operator sized for eight workers into a
// single-threaded one, hours later than expected and with no warning.
func TestPostgres_RefusesParallelRestoreFromAStream(t *testing.T) {
	binDir, _ := fakeTools(t, 0, "")
	r := restore.Postgres{Config: targetConfig(t, binDir)}

	_, err := r.Restore(context.Background(), local.New(), restore.PostgresRequest{
		Database: "shop",
		Jobs:     8,
		Dump:     strings.NewReader("PGDMP"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--jobs")
	assert.Contains(t, err.Error(), "fetch", "the error has to name the way out, not just the wall")
}

// Roles live in the cluster. Replaying them before the dump is what makes the
// restored database's owners and grants resolve.
func TestPostgres_ReplaysGlobalsBeforeTheDump(t *testing.T) {
	binDir, rec := fakeTools(t, 0, "")
	r := restore.Postgres{Config: targetConfig(t, binDir)}

	_, err := r.Restore(context.Background(), local.New(), restore.PostgresRequest{
		Database: "shop",
		Globals:  strings.NewReader("CREATE ROLE app;"),
		Dump:     strings.NewReader("PGDMP"),
	})
	require.NoError(t, err)

	assert.Equal(t, "CREATE ROLE app;", recorded(t, rec, "psql", "stdin"))
	assert.NotContains(t, recorded(t, rec, "psql", "args"), "ON_ERROR_STOP",
		"a role that already exists is the normal case, not a failure")
}

// "pg_restore exited with status 1" is not a diagnosis. The tool said why on
// stderr and that is what an operator needs.
func TestPostgres_FailureCarriesTheToolsOwnMessage(t *testing.T) {
	binDir, _ := fakeTools(t, 1, "pg_restore: error: could not execute query: relation already exists")
	r := restore.Postgres{Config: targetConfig(t, binDir)}

	_, err := r.Restore(context.Background(), local.New(), restore.PostgresRequest{
		Database: "shop",
		Dump:     strings.NewReader("PGDMP"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "relation already exists")
}

// A failure replaying globals is reported without stopping: the dump is the
// part that carries the data, and refusing to restore it because a role already
// existed would be the wrong call in the middle of an incident.
func TestPostgres_GlobalsFailureIsAWarningNotAnError(t *testing.T) {
	binDir, _ := fakeTools(t, 3, "psql:<stdin>:1: ERROR:  role \"app\" already exists")
	r := restore.Postgres{Config: targetConfig(t, binDir)}

	res, err := r.Restore(context.Background(), local.New(), restore.PostgresRequest{
		Database: "shop",
		Globals:  strings.NewReader("CREATE ROLE app;"),
		Dump:     strings.NewReader("PGDMP"),
	})
	// pg_restore fails too here (same exit code), so only the warning is
	// asserted; what matters is that the globals step recorded rather than
	// aborted.
	_ = err
	require.NotEmpty(t, res.Warnings)
	assert.Contains(t, res.Warnings[0], "already exists")
}

func TestPostgres_RefusesAnEmptyTarget(t *testing.T) {
	binDir, _ := fakeTools(t, 0, "")
	_, err := restore.Postgres{Config: targetConfig(t, binDir)}.
		Restore(context.Background(), local.New(), restore.PostgresRequest{
			Dump: strings.NewReader("PGDMP"),
		})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database")
}
