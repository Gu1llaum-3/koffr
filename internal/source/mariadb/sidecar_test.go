package mariadb_test

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/source"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

// Databasus does not back up accounts or privileges at all, so a restore there
// yields tables whose owners do not exist. Koffr already produces globals.sql
// for PostgreSQL; this is the same idea, and the same reason.
func TestSidecars_GrantsAreBackedUp(t *testing.T) {
	skipUnlessReady(t)
	execSQL(t,
		"CREATE TABLE IF NOT EXISTS orders (id INT PRIMARY KEY) ENGINE=InnoDB",
		"CREATE USER IF NOT EXISTS 'reporting'@'%' IDENTIFIED BY 'a-password-nobody-should-keep'",
		"GRANT SELECT ON `"+database+"`.* TO 'reporting'@'%'")
	cleanupSQL(t, "DROP USER IF EXISTS 'reporting'@'%'", "DROP TABLE IF EXISTS orders")

	stream, err := newSource(t, baseConfig()).Open(t.Context(), localExec(t),
		source.Request{Kind: source.KindLogical})
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, stream.Reader)
	require.NoError(t, err)

	// Collected before Close, which is the ordering PostgreSQL got wrong first:
	// asked for afterwards, the connection is gone and the file comes back
	// empty, so the backup silently loses half of itself.
	sidecars, err := stream.Sidecars()
	require.NoError(t, err)
	require.NoError(t, stream.Close())

	grants, ok := sidecars["grants.sql"]
	require.True(t, ok, "a restore without accounts leaves tables nobody owns")
	body := string(grants)

	assert.Contains(t, body, "CREATE USER IF NOT EXISTS 'reporting'@'%'")
	assert.Contains(t, body, "GRANT SELECT ON `"+database+"`.* TO")
}

// A password hash sitting in a backup repository is a liability nobody asked
// for. This is the position already taken for PostgreSQL with
// --no-role-passwords, and it has to hold for every shape the server uses to
// write an authentication clause.
func TestSidecars_GrantsCarryNoPassword(t *testing.T) {
	skipUnlessReady(t)
	const plaintext = "a-password-nobody-should-keep"
	execSQL(t,
		"CREATE USER IF NOT EXISTS 'reporting'@'%' IDENTIFIED BY '"+plaintext+"'",
		"GRANT SELECT ON `"+database+"`.* TO 'reporting'@'%'")
	cleanupSQL(t, "DROP USER IF EXISTS 'reporting'@'%'")

	stream, err := newSource(t, baseConfig()).Open(t.Context(), localExec(t),
		source.Request{Kind: source.KindLogical})
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, stream.Reader)
	require.NoError(t, err)
	sidecars, err := stream.Sidecars()
	require.NoError(t, err)
	require.NoError(t, stream.Close())

	body := string(sidecars["grants.sql"])
	assert.NotContains(t, body, "IDENTIFIED", "no authentication clause reaches the repository")
	assert.NotContains(t, body, plaintext)
	// A MariaDB hash is a star followed by forty hex digits. Asserting on that
	// shape rather than on a bare star, which "*.*" contains legitimately.
	assert.NotRegexp(t, `\*[0-9A-Fa-f]{40}`, body, "no password hash reaches the repository")
	assert.NotContains(t, body, "ON *.*", "a server-wide grant must not be replayable from a backup")
	assert.Contains(t, body, "set them again after restoring",
		"the operator has to be told the passwords are not there")
	testutil.AssertNoSecretLeak(t, body)
}

// A role that cannot read mysql.user can still take a perfectly good backup of
// its own data. Failing would be wrong; saying nothing would be worse.
func TestSidecars_SaysSoWhenAccountsCannotBeRead(t *testing.T) {
	skipUnlessReady(t)
	execSQL(t,
		"CREATE USER IF NOT EXISTS 'limited'@'%' IDENTIFIED BY '"+testutil.SecretSentinel+"'",
		"GRANT SELECT, SHOW VIEW ON `"+database+"`.* TO 'limited'@'%'")
	cleanupSQL(t, "DROP USER IF EXISTS 'limited'@'%'")

	cfg := baseConfig()
	cfg.User = "limited"
	cfg.Password = testutil.SecretSentinel

	stream, err := newSource(t, cfg).Open(t.Context(), localExec(t),
		source.Request{Kind: source.KindLogical})
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, stream.Reader)
	require.NoError(t, err)
	sidecars, err := stream.Sidecars()
	require.NoError(t, err)
	require.NoError(t, stream.Close())

	body := string(sidecars["grants.sql"])
	assert.Contains(t, body, "cannot read mysql.user")
	assert.Contains(t, body, "by hand", "the operator is told what is now their job")
}

// The binlog coordinates are the one thing in a dump a later point-in-time
// recovery cannot recompute, and they are not recoverable after the fact.
// Databasus discards them outright; capturing them costs one flag.
func TestResult_CarriesTheBinlogPosition(t *testing.T) {
	skipUnlessReady(t)
	if !binlogEnabled(t) {
		t.Skip("this server keeps no binary log")
	}
	execSQL(t, "CREATE TABLE IF NOT EXISTS anchored (id INT PRIMARY KEY) ENGINE=InnoDB")
	cleanupSQL(t, "DROP TABLE IF EXISTS anchored")

	stream, err := newSource(t, baseConfig()).Open(t.Context(), localExec(t),
		source.Request{Kind: source.KindLogical})
	require.NoError(t, err)
	body, err := io.ReadAll(stream.Reader)
	require.NoError(t, err)
	require.NoError(t, stream.Close())

	// The position is written as a comment, never as an executable statement:
	// replaying a dump must not turn the restored server into a replica of the
	// one it came from.
	assert.NotContains(t, string(body), "\nCHANGE MASTER TO")

	res := stream.Result()
	assert.NotEmpty(t, res.BinlogFile, "without this a logical backup can never anchor a PITR")
	assert.Positive(t, res.BinlogPos)
}

// Asking for a position the user may not read makes mariadb-dump fail outright,
// which would turn a working backup into no backup at all. So it is checked
// rather than attempted -- and a backup role without RELOAD is the ordinary
// case, not the exotic one.
func TestResult_NoPositionWithoutTheReloadPrivilege(t *testing.T) {
	skipUnlessReady(t)
	execSQL(t,
		"CREATE USER IF NOT EXISTS 'noreload'@'%' IDENTIFIED BY '"+testutil.SecretSentinel+"'",
		"GRANT SELECT, SHOW VIEW ON `"+database+"`.* TO 'noreload'@'%'")
	cleanupSQL(t, "DROP USER IF EXISTS 'noreload'@'%'")

	cfg := baseConfig()
	cfg.User = "noreload"
	cfg.Password = testutil.SecretSentinel

	stream, err := newSource(t, cfg).Open(t.Context(), localExec(t),
		source.Request{Kind: source.KindLogical})
	require.NoError(t, err, "the backup must still be taken, just without an anchor")
	_, err = io.Copy(io.Discard, stream.Reader)
	require.NoError(t, err)
	require.NoError(t, stream.Close())

	assert.Empty(t, stream.Result().BinlogFile)
}

func binlogEnabled(t *testing.T) bool {
	t.Helper()
	var name, value string
	err := adminDB(t).QueryRowContext(t.Context(),
		"SHOW VARIABLES LIKE 'log_bin'").Scan(&name, &value)
	require.NoError(t, err)
	return strings.EqualFold(value, "ON")
}

// The escape hatch is only acceptable because it leaves a trace. An operator
// restoring a year from now will not have the configuration that allowed it,
// nor the conversation where someone judged the risk acceptable -- the backup
// itself has to say so.
func TestManifest_MarksAnInconsistentSnapshot(t *testing.T) {
	skipUnlessReady(t)
	execSQL(t,
		"DROP TABLE IF EXISTS legacy_marked",
		"CREATE TABLE legacy_marked (id INT PRIMARY KEY) ENGINE=MyISAM")
	cleanupSQL(t, "DROP TABLE IF EXISTS legacy_marked")

	cfg := baseConfig()
	cfg.AllowInconsistentSnapshot = true
	info, err := newSource(t, cfg).Probe(t.Context(), localExec(t))
	require.NoError(t, err)

	// The phrase is shared with internal/backup, which reads it back to set
	// the manifest's snapshot_consistent flag. Asserting on the constant is
	// what stops the two drifting apart.
	var found bool
	for _, r := range info.Restrictions {
		if strings.Contains(r, source.NotASnapshot) {
			found = true
		}
	}
	assert.True(t, found,
		"without this phrase the manifest records nothing and the backup looks trustworthy")
}
