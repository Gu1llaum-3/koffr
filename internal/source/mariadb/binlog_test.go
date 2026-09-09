package mariadb_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/source/mariadb"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

// The test container runs with --log-bin=binlog, so the base is "binlog".
var binlogName = regexp.MustCompile(`^binlog\.\d{6,}$`)

func TestBinlog_ReportsWhereTheServerIsWriting(t *testing.T) {
	skipUnlessReady(t)
	st, err := baseConfig().Binlog(t.Context(), localExec(t))
	require.NoError(t, err)

	require.True(t, st.Enabled, "the harness enables the binary log on purpose")
	assert.Regexp(t, binlogName, st.File)
	assert.Positive(t, st.Position, "a fresh file starts after its magic header, never at zero")
	assert.NotEmpty(t, st.Files, "SHOW BINARY LOGS lists what the server still holds")
	assert.Equal(t, st.File, st.Files[len(st.Files)-1].Name, "the newest listed file is the one being written")
	assert.Positive(t, st.MaxSize)
}

// Rotation is what turns a quiet database's open file into something
// archivable. It is optional and off by default; when asked for, it has to
// work and to be visible.
func TestRotateBinlog_AdvancesTheCurrentFile(t *testing.T) {
	skipUnlessReady(t)
	cfg := baseConfig()

	before, err := cfg.Binlog(t.Context(), localExec(t))
	require.NoError(t, err)
	require.NoError(t, cfg.RotateBinlog(t.Context(), localExec(t)))
	after, err := cfg.Binlog(t.Context(), localExec(t))
	require.NoError(t, err)

	assert.NotEqual(t, before.File, after.File, "FLUSH BINARY LOGS opens the next file")
	assert.Len(t, after.Files, len(before.Files)+1)
}

// Without RELOAD the rotation cannot happen, and the answer has to say which
// privilege rather than "access denied": it is the difference between a grant
// and an afternoon.
func TestRotateBinlog_SaysWhichPrivilegeIsMissing(t *testing.T) {
	skipUnlessReady(t)
	execSQL(t,
		"CREATE USER IF NOT EXISTS 'norotate'@'%' IDENTIFIED BY '"+testutil.SecretSentinel+"'",
		"GRANT SELECT, REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO 'norotate'@'%'")
	cleanupSQL(t, "DROP USER IF EXISTS 'norotate'@'%'")

	cfg := baseConfig()
	cfg.User, cfg.Password = "norotate", testutil.SecretSentinel
	err := cfg.RotateBinlog(t.Context(), localExec(t))
	require.ErrorIs(t, err, mariadb.ErrNoReload)
	testutil.AssertNoSecretLeak(t, err.Error())
}

// The receiver's command line is world-readable for as long as it runs, which
// for --stop-never is for ever.
func TestReceiverArgs_CarryNoCredentialAndTheArchivingFlags(t *testing.T) {
	cfg := mariadb.Config{
		Host: "db", User: "koffr", Password: testutil.SecretSentinel,
		Database: "shop", TLS: "disable", ToolRunner: nopExecutor{},
	}
	sess := mariadb.SessionForTest("/tmp/k/koffr.cnf")
	args := cfg.ReceiverArgs(sess, "/var/lib/koffr/spool", "binlog.000041", 4242)
	joined := strings.Join(args, " ")

	testutil.AssertNoSecretLeak(t, joined)
	assert.Equal(t, "--defaults-file=/tmp/k/koffr.cnf", args[0])
	for _, want := range []string{
		"--read-from-remote-server", "--raw", "--stop-never",
		"--stop-never-slave-server-id=4242", "--result-file=/var/lib/koffr/spool/",
		"--protocol=TCP",
	} {
		assert.Contains(t, joined, want)
	}
	assert.Equal(t, "binlog.000041", args[len(args)-1], "the log to start from comes last")
}
