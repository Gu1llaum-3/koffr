package mariadb_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcmariadb "github.com/testcontainers/testcontainers-go/modules/mariadb"

	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/executor/local"
	"github.com/Gu1llaum-3/koffr/internal/source"
	"github.com/Gu1llaum-3/koffr/internal/source/mariadb"
	"github.com/Gu1llaum-3/koffr/internal/source/sourcetest"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

const (
	// The container module creates appUser and, because a password is given,
	// sets the root password to the same value. Asking it for "root" directly
	// leaves the entrypoint in a state where root is denied from outside, which
	// cost half an hour to find; naming an application user is the supported
	// path and yields an unprivileged account the privilege tests need anyway.
	adminUser = "root"
	appUser   = "koffr"
	// The container's own password is the sentinel, so every test in this
	// package proves ENF-021 by construction rather than only the one that
	// looks for it.
	adminPass = testutil.SecretSentinel
	database  = "shop"
)

// mariaImage lets the milestone gate walk the supported majors. ENF-041 fixes
// them at 10.6, 11 and 12; CI pins one, because "supports 10.6 to 12" is a
// claim and a claim needs a run behind it.
func mariaImage() string {
	if img := os.Getenv("KOFFR_MARIADB_IMAGE"); img != "" {
		return img
	}
	return "mariadb:11.4"
}

var shared struct {
	host      string
	port      int
	container testcontainers.Container
	skipWhy   string
}

func TestMain(m *testing.M) {
	os.Exit(func() int {
		if why := testutil.EnsureDockerHost(); why != "" {
			shared.skipWhy = why
		}
		if shared.skipWhy == "" && !hasDumpBinary() {
			// CT-001: Koffr shells out to client binaries it cannot embed.
			shared.skipWhy = "neither mariadb-dump nor mysqldump is on PATH"
		}
		if shared.skipWhy == "" {
			if err := startMariaDB(); err != nil {
				shared.skipWhy = fmt.Sprintf("mariadb container unavailable: %v", err)
			}
		}
		if _, fatal := testutil.SkipOrFailWithoutDocker(shared.skipWhy); fatal != "" {
			fmt.Fprintln(os.Stderr, fatal)
			return 1
		}
		defer func() {
			if shared.container != nil {
				_ = testcontainers.TerminateContainer(shared.container)
			}
		}()
		return m.Run()
	}())
}

func hasDumpBinary() bool {
	for _, name := range []string{"mariadb-dump", "mysqldump"} {
		if _, err := exec.LookPath(name); err == nil {
			return true
		}
	}
	return false
}

func startMariaDB() error {
	ctx := context.Background()
	container, err := tcmariadb.Run(ctx, mariaImage(),
		tcmariadb.WithDatabase(database),
		tcmariadb.WithUsername(appUser),
		tcmariadb.WithPassword(adminPass),
		// The binary log is off by default in the official image, and without it
		// the code that captures the dump's position is never exercised: the
		// test that covers it would skip, quietly, for ever.
		testcontainers.WithCmd("mariadbd", "--log-bin=binlog", "--server-id=1"),
		// No wait strategy of our own: the module waits for the line the
		// entrypoint prints once it has restarted the server with the
		// configured credentials. A port check fires while the temporary
		// bootstrap server is still up, which answers and then denies access.
	)
	if err != nil {
		return err
	}
	shared.container = container

	host, err := container.Host(ctx)
	if err != nil {
		return err
	}
	port, err := container.MappedPort(ctx, "3306/tcp")
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(port.Port())
	if err != nil {
		return err
	}
	shared.host, shared.port = host, n
	return waitReady(ctx)
}

// waitReady closes the gap between "the port is listening" and "the server
// answers queries", which a port check does not cover.
func waitReady(ctx context.Context) error {
	var lastErr error
	for range 60 {
		db, err := sql.Open("mysql", adminDSN())
		if err == nil {
			err = db.PingContext(ctx)
			_ = db.Close()
			if err == nil {
				return nil
			}
		}
		lastErr = err
		// A delay, because without one the loop exhausts itself in
		// milliseconds and reports "not ready" for a server that simply had
		// not finished starting.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return lastErr
}

// adminDSN is built rather than formatted.
//
// A MySQL DSN is not a URL: the driver takes the password literally and does no
// percent-decoding, so escaping it the way a PostgreSQL DSN needs corrupts it --
// which shows up as "access denied" and sends you looking at the account. The
// driver's own Config knows the format; using it removes the question.
func adminDSN() string {
	cfg := mysql.NewConfig()
	cfg.User = adminUser
	cfg.Passwd = adminPass
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(shared.host, strconv.Itoa(shared.port))
	cfg.DBName = database
	return cfg.FormatDSN()
}

func adminDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", adminDSN())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func skipUnlessReady(t *testing.T) {
	t.Helper()
	if shared.skipWhy != "" {
		t.Skip(shared.skipWhy)
	}
}

func localExec(t *testing.T) executor.Executor {
	t.Helper()
	ex := local.New()
	t.Cleanup(func() { _ = ex.Close() })
	return ex
}

func baseConfig() mariadb.Config {
	return mariadb.Config{
		Host:       shared.host,
		Port:       shared.port,
		User:       adminUser,
		Password:   adminPass,
		Database:   database,
		TLS:        "disable",
		ToolRunner: local.New(),
	}
}

func newSource(t *testing.T, cfg mariadb.Config) *mariadb.Logical {
	t.Helper()
	s, err := mariadb.NewLogical(cfg)
	require.NoError(t, err)
	return s
}

// execSQL runs statements as the admin, for tests that need a particular shape
// of database.
func execSQL(t *testing.T, statements ...string) {
	t.Helper()
	db := adminDB(t)
	for _, s := range statements {
		_, err := db.ExecContext(t.Context(), s)
		require.NoError(t, err, "statement: %s", s)
	}
}

// cleanupSQL is execSQL for a t.Cleanup, and it exists because the two cannot
// share a context.
//
// t.Context() is cancelled when the test body returns, which is before cleanups
// run: a DROP issued from a cleanup with that context fails as "context
// cancelled", and the table it was meant to remove survives into the next test.
// Found the hard way -- a MyISAM table left behind made three later tests fail
// for a reason none of them was about.
func cleanupSQL(t *testing.T, statements ...string) {
	t.Helper()
	t.Cleanup(func() {
		db, err := sql.Open("mysql", adminDSN())
		if err != nil {
			t.Errorf("cleanup: open: %v", err)
			return
		}
		defer func() { _ = db.Close() }()
		for _, s := range statements {
			if _, err := db.ExecContext(context.Background(), s); err != nil {
				t.Errorf("cleanup: %s: %v", s, err)
			}
		}
	})
}

// The contract every source.Source implementation must satisfy. Not one test is
// rewritten for MariaDB, which is the whole return on writing it.
func TestContract(t *testing.T) {
	skipUnlessReady(t)
	execSQL(t, "CREATE TABLE IF NOT EXISTS contract (id INT PRIMARY KEY, v TEXT) ENGINE=InnoDB",
		"INSERT IGNORE INTO contract VALUES (1, 'one'), (2, 'two')")

	sourcetest.Suite(t, sourcetest.Target{
		Engine: source.EngineMariaDB,
		Kind:   source.KindLogical,
		New: func(t *testing.T, tools executor.Executor) source.Source {
			cfg := baseConfig()
			cfg.ToolRunner = tools
			return newSource(t, cfg)
		},
		Reach: func(t *testing.T) executor.Executor { return localExec(t) },
		WithoutClientBinary: func(t *testing.T) source.Source {
			cfg := baseConfig()
			cfg.BinDir = t.TempDir() // empty: mariadb-dump is not in it
			return newSource(t, cfg)
		},
	})
}

// nopExecutor satisfies the tool-runner requirement for tests that only render
// a command line and never run one.
type nopExecutor struct{}

func (nopExecutor) Dial(context.Context, string, string) (net.Conn, error) {
	return nil, fmt.Errorf("nopExecutor does not dial")
}

func (nopExecutor) Start(context.Context, executor.Command) (executor.Process, error) {
	return nil, fmt.Errorf("nopExecutor does not start processes")
}

func (nopExecutor) Capabilities() executor.Capabilities {
	return executor.Capabilities{CanExec: true, Direct: true}
}

func (nopExecutor) Close() error { return nil }
