package mariadb_test

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/source"
	"github.com/Gu1llaum-3/koffr/internal/source/mariadb"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

// dumpOf runs a backup and returns the SQL it produced.
func dumpOf(t *testing.T, cfg mariadb.Config, req source.Request) (string, *source.Stream) {
	t.Helper()
	stream, err := newSource(t, cfg).Open(t.Context(), localExec(t), req)
	require.NoError(t, err)
	body, err := io.ReadAll(stream.Reader)
	require.NoError(t, err)
	return string(body), stream
}

// --single-transaction is what makes a logical backup a snapshot, and it only
// covers transactional engines. One MyISAM table is enough for the dump to hold
// a state the database never had -- and nothing in the output says so, which is
// what makes it worth refusing rather than warning about.
func TestProbe_RefusesANonTransactionalTable(t *testing.T) {
	skipUnlessReady(t)
	execSQL(t,
		"DROP TABLE IF EXISTS legacy_notes",
		"CREATE TABLE legacy_notes (id INT PRIMARY KEY, body TEXT) ENGINE=MyISAM")
	cleanupSQL(t, "DROP TABLE IF EXISTS legacy_notes")

	_, err := newSource(t, baseConfig()).Probe(t.Context(), localExec(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "legacy_notes", "the operator must be told which table")
	assert.Contains(t, err.Error(), "MyISAM")
	assert.Contains(t, err.Error(), "allow_inconsistent_snapshot", "and how to proceed anyway")
	testutil.AssertNoSecretLeak(t, err.Error())
}

// The escape hatch exists because an all-MyISAM database is real and an
// imperfect dump beats none. It is explicit, and what it costs is recorded.
func TestProbe_TheEscapeHatchIsRecordedNotSilent(t *testing.T) {
	skipUnlessReady(t)
	execSQL(t,
		"DROP TABLE IF EXISTS legacy_notes",
		"CREATE TABLE legacy_notes (id INT PRIMARY KEY, body TEXT) ENGINE=MyISAM")
	cleanupSQL(t, "DROP TABLE IF EXISTS legacy_notes")

	cfg := baseConfig()
	cfg.AllowInconsistentSnapshot = true
	info, err := newSource(t, cfg).Probe(t.Context(), localExec(t))
	require.NoError(t, err)

	joined := strings.Join(info.Restrictions, "\n")
	assert.Contains(t, joined, "not consistent",
		"a backup taken this way must say so, or a restore surprises someone")
	assert.Contains(t, joined, "legacy_notes")
}

func TestProbe_ReportsDatabasesAndLogicalKind(t *testing.T) {
	skipUnlessReady(t)
	info, err := newSource(t, baseConfig()).Probe(t.Context(), localExec(t))
	require.NoError(t, err)
	assert.Equal(t, []string{database}, info.Databases)
	assert.Equal(t, []source.Kind{source.KindLogical}, info.Kinds)
	assert.Contains(t, info.ServerVersion, "MariaDB")
}

// A dump that carries its own CREATE DATABASE restores onto a blank server with
// no flag to guess, which is what PD-001 asks of the generated procedure.
func TestOpen_TheDumpCarriesItsOwnDatabase(t *testing.T) {
	skipUnlessReady(t)
	execSQL(t, "CREATE TABLE IF NOT EXISTS carried (id INT PRIMARY KEY) ENGINE=InnoDB")
	cleanupSQL(t, "DROP TABLE IF EXISTS carried")

	body, stream := dumpOf(t, baseConfig(), source.Request{Kind: source.KindLogical})
	require.NoError(t, stream.Close())

	assert.Contains(t, body, "CREATE DATABASE")
	assert.Contains(t, body, "USE `"+database+"`")
	assert.Contains(t, body, "CREATE TABLE `carried`")
}

func TestOpen_ExcludedTablesAreLeftOut(t *testing.T) {
	skipUnlessReady(t)
	execSQL(t,
		"CREATE TABLE IF NOT EXISTS kept (id INT PRIMARY KEY) ENGINE=InnoDB",
		"CREATE TABLE IF NOT EXISTS dropped (id INT PRIMARY KEY) ENGINE=InnoDB")
	cleanupSQL(t, "DROP TABLE IF EXISTS kept", "DROP TABLE IF EXISTS dropped")

	body, stream := dumpOf(t, baseConfig(), source.Request{
		Kind: source.KindLogical, ExcludeTables: []string{"dropped"},
	})
	require.NoError(t, stream.Close())

	assert.Contains(t, body, "CREATE TABLE `kept`")
	assert.NotContains(t, body, "CREATE TABLE `dropped`")
}

// The command line is what a `ps` on the database host shows for as long as the
// dump runs, so it is asserted directly rather than inferred.
func TestRenderCommand_CarriesNoCredentialAndTheRightFlags(t *testing.T) {
	cfg := mariadb.Config{
		Host: "db.internal", User: "koffr", Password: testutil.SecretSentinel,
		Database: "shop", TLS: "disable", ToolRunner: nopExecutor{},
	}
	src, err := mariadb.NewLogical(cfg)
	require.NoError(t, err)

	args, err := src.RenderCommand(source.Request{Kind: source.KindLogical},
		"/tmp/koffr-x/koffr.cnf", "SELECT", "TRIGGER", "EVENT")
	require.NoError(t, err)
	joined := strings.Join(args, " ")

	testutil.AssertNoSecretLeak(t, joined)
	assert.Equal(t, "--defaults-file=/tmp/koffr-x/koffr.cnf", args[0],
		"a later --defaults-file is an error, so this is the only position it can hold")
	for _, want := range []string{
		"--protocol=TCP", "--single-transaction", "--quick", "--routines",
		"--no-tablespaces", "--max-allowed-packet=1G", "--triggers", "--events",
		"--databases shop",
	} {
		assert.Contains(t, joined, want)
	}
}

// Triggers are on by default, so a role without the privilege does not get a
// dump without them -- mariadb-dump stops on SHOW TRIGGERS. The flag has to be
// chosen from what the role actually holds.
func TestRenderCommand_SkipsWhatThePrivilegesDoNotAllow(t *testing.T) {
	cfg := mariadb.Config{
		Host: "db.internal", User: "koffr", Password: testutil.SecretSentinel,
		Database: "shop", TLS: "disable", ToolRunner: nopExecutor{},
	}
	src, err := mariadb.NewLogical(cfg)
	require.NoError(t, err)

	args, err := src.RenderCommand(source.Request{Kind: source.KindLogical},
		"/tmp/c.cnf", "SELECT")
	require.NoError(t, err)
	joined := strings.Join(args, " ")

	assert.Contains(t, joined, "--skip-triggers")
	assert.NotContains(t, joined, "--events")
}

func TestOpen_RefusesAKindItCannotProduce(t *testing.T) {
	skipUnlessReady(t)
	_, err := newSource(t, baseConfig()).Open(t.Context(), localExec(t),
		source.Request{Kind: source.KindPhysical})
	require.ErrorContains(t, err, "not a logical backup")
}

// EF-019: never a partial dump. A role that cannot read the tables is refused
// before mariadb-dump starts, not discovered by it stopping halfway through and
// leaving objects nothing points at.
func TestProbe_RefusesARoleThatCannotRead(t *testing.T) {
	skipUnlessReady(t)
	execSQL(t,
		"CREATE USER IF NOT EXISTS 'blind'@'%' IDENTIFIED BY '"+testutil.SecretSentinel+"'",
		// SHOW VIEW and not USAGE: a role with USAGE alone cannot even open the
		// database, so the server refuses the connection and Koffr's own
		// pre-check never runs. This one connects and still cannot read a row,
		// which is the case the check exists for.
		"GRANT SHOW VIEW ON `"+database+"`.* TO 'blind'@'%'")
	cleanupSQL(t, "DROP USER IF EXISTS 'blind'@'%'")

	cfg := baseConfig()
	cfg.User = "blind"
	cfg.Password = testutil.SecretSentinel

	_, err := newSource(t, cfg).Probe(t.Context(), localExec(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SELECT")
	assert.Contains(t, err.Error(), "EF-019", "the requirement is cited where a reader would ask why")
	testutil.AssertNoSecretLeak(t, err.Error())
}

// SHOW VIEW is asked for only when the schema holds a view. Demanding it
// everywhere rejects an account perfectly able to do the job.
func TestProbe_AcceptsARoleWithoutShowViewWhenThereAreNoViews(t *testing.T) {
	skipUnlessReady(t)
	execSQL(t,
		"CREATE USER IF NOT EXISTS 'reader'@'%' IDENTIFIED BY '"+testutil.SecretSentinel+"'",
		"GRANT SELECT ON `"+database+"`.* TO 'reader'@'%'")
	cleanupSQL(t, "DROP USER IF EXISTS 'reader'@'%'")

	cfg := baseConfig()
	cfg.User = "reader"
	cfg.Password = testutil.SecretSentinel

	info, err := newSource(t, cfg).Probe(t.Context(), localExec(t))
	require.NoError(t, err)
	assert.Contains(t, info.Kinds, source.KindLogical)

	// And what it will be missing is said out loud rather than discovered later.
	joined := strings.Join(info.Restrictions, "\n")
	assert.Contains(t, joined, "triggers are not included")
}
