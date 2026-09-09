package e2e_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	tcmariadb "github.com/testcontainers/testcontainers-go/modules/mariadb"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

func hasMariaDumpBinary() bool {
	for _, name := range []string{"mariadb-dump", "mysqldump"} {
		if _, err := exec.LookPath(name); err == nil {
			return true
		}
	}
	return false
}

func startMariaSource(t *testing.T, ctx context.Context) *tcmariadb.MariaDBContainer {
	t.Helper()
	return startMariaSourceWith(t, ctx)
}

// startMariaSourceWith starts the source with extra server arguments -- the
// binary log, for the point-in-time test.
func startMariaSourceWith(t *testing.T, ctx context.Context, serverArgs ...string) *tcmariadb.MariaDBContainer {
	t.Helper()
	opts := []testcontainers.ContainerCustomizer{
		tcmariadb.WithDatabase(mariaDatabase),
		tcmariadb.WithUsername("app"),
		tcmariadb.WithPassword(mariaPass),
	}
	if len(serverArgs) > 0 {
		opts = append(opts, testcontainers.WithCmd(append([]string{"mariadbd"}, serverArgs...)...))
	}
	c, err := tcmariadb.Run(ctx, mariadbImage(), opts...)
	require.NoError(t, err)
	//nolint:contextcheck // teardown outlives the test context by design
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	return c
}

func mariaSourcePort(t *testing.T, ctx context.Context, c *tcmariadb.MariaDBContainer) int {
	t.Helper()
	p, err := c.MappedPort(ctx, "3306/tcp")
	require.NoError(t, err)
	n, err := strconv.Atoi(p.Port())
	require.NoError(t, err)
	return n
}

// seedMaria builds a database with an account, two tables and a foreign key, so
// the restore has something to get wrong.
func seedMaria(t *testing.T, ctx context.Context, c *tcmariadb.MariaDBContainer) {
	t.Helper()
	stmts := []string{
		"CREATE TABLE orders (id INT PRIMARY KEY, total DECIMAL(10,2) NOT NULL) ENGINE=InnoDB",
		"CREATE TABLE items (id INT PRIMARY KEY, order_id INT NOT NULL, label VARCHAR(64), " +
			"FOREIGN KEY (order_id) REFERENCES orders(id)) ENGINE=InnoDB",
		"INSERT INTO orders SELECT seq, seq * 1.5 FROM seq_1_to_40",
		"INSERT INTO items SELECT seq, ((seq - 1) MOD 40) + 1, CONCAT('item-', seq) FROM seq_1_to_120",
		// An account whose only reason to exist is to prove grants.sql arrived.
		"CREATE USER 'shop_reader'@'%' IDENTIFIED BY 'not-in-the-backup'",
		"GRANT SELECT ON `" + mariaDatabase + "`.* TO 'shop_reader'@'%'",
	}
	for _, s := range stmts {
		runMariaSQL(t, ctx, c, mariaDatabase, s)
	}
}

func writeMariaConfig(t *testing.T, dir, repo string, port int) (identity, cfgPath string) {
	t.Helper()
	return writeMariaConfigWith(t, dir, repo, port, "", "")
}

// writeMariaConfigWith takes extra YAML for the source and for the top level.
func writeMariaConfigWith(t *testing.T, dir, repo string, port int, sourceExtra, topExtra string) (identity, cfgPath string) {
	t.Helper()
	identity, recipient := testutil.AgeIdentity(t)
	_, recovery := testutil.AgeIdentity(t)

	t.Setenv("KOFFR_IDENTITY", identity)
	t.Setenv("KOFFR_E2E_MARIA_PASSWORD", mariaPass)

	cfgPath = filepath.Join(dir, "koffr.yml")
	content := fmt.Sprintf(`version: 1
crypto:
  recipients:
    - %s
    - %s
  identity: env:KOFFR_IDENTITY
catalog:
  path: %s
%sdestinations:
  main:
    type: fs
    path: %s
sources:
  shop:
    engine: mariadb
    host: 127.0.0.1
    port: %d
    user: %s
    password: env:KOFFR_E2E_MARIA_PASSWORD
    database: %s
    sslmode: disable
    destinations: [main]
%s`, recipient, recovery, filepath.Join(dir, "catalog.db"), topExtra, repo, port, mariaUser, mariaDatabase, sourceExtra)
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0o600))
	return identity, cfgPath
}

func startBareMariaMachine(t *testing.T, ctx context.Context) testcontainers.Container {
	t.Helper()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context: filepath.Join("testdata", "mariadb"), KeepImage: true,
			},
			Env: map[string]string{"MARIADB_ROOT_PASSWORD": mariaPass},
			WaitingFor: wait.ForLog("port: 3306  mariadb.org binary distribution").
				WithStartupTimeout(180 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)
	//nolint:contextcheck // teardown outlives the test context by design
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	// If Koffr were reachable here the test would prove nothing.
	code, _, err := c.Exec(ctx, []string{"sh", "-c", "command -v koffr"})
	require.NoError(t, err)
	require.NotEqual(t, 0, code, "this machine is supposed to have no koffr")
	return c
}

// runInMariaContainer runs one command from the document, as written.
//
// Unlike the PostgreSQL machine there is no unprivileged database user to drop
// to: the mariadb image runs its server as mysql and gives the shell nothing to
// su into, so the commands run as root in a container that exists for one test.
func runInMariaContainer(
	t *testing.T, ctx context.Context, c testcontainers.Container,
	name, command string, exitStatusIsNotMeaningful bool,
) {
	t.Helper()
	script := "/restore/" + name + ".sh"
	require.NoError(t, c.CopyToContainer(ctx, []byte(command+"\n"), script, 0o755))

	code, out, err := c.Exec(ctx,
		[]string{"sh", "-c", "cd /restore && sh " + script}, tcexec.Multiplexed())
	require.NoError(t, err)
	output := read(out)
	if !exitStatusIsNotMeaningful {
		require.Equal(t, 0, code,
			"a command from RESTORE.md failed, which means the document is wrong:\n  %s\n%s",
			command, output)
	}
	if output != "" {
		t.Logf("$ %s\n%s", command, output)
	}
}

func runMariaSQL(t *testing.T, ctx context.Context, c interface {
	Exec(context.Context, []string, ...tcexec.ProcessOption) (int, io.Reader, error)
}, database, query string) string {
	t.Helper()
	code, out, err := c.Exec(ctx, []string{
		"mariadb", "-u", mariaUser, "-p" + mariaPass, "-D", database, "-N", "-B", "-e", query,
	}, tcexec.Multiplexed())
	require.NoError(t, err)
	body := read(out)
	require.Equal(t, 0, code, "query failed: %s\n%s", query, body)
	return strings.TrimSpace(body)
}

func mariaInt(t *testing.T, ctx context.Context, c interface {
	Exec(context.Context, []string, ...tcexec.ProcessOption) (int, io.Reader, error)
}, database, query string) int {
	t.Helper()
	var n int
	_, err := fmt.Sscanf(runMariaSQL(t, ctx, c, database, query), "%d", &n)
	require.NoError(t, err)
	return n
}

func mariaString(t *testing.T, ctx context.Context, c interface {
	Exec(context.Context, []string, ...tcexec.ProcessOption) (int, io.Reader, error)
}, database, query string) string {
	t.Helper()
	return runMariaSQL(t, ctx, c, database, query)
}

// loadMariaBackup puts the objects and the identity on the bare machine, the
// way an operator would after downloading them.
//
// Separate from loadBackup because that one hands ownership to the postgres
// user, which the MariaDB image does not have. The commands here run as root in
// a container that exists for one test, so nothing is chowned.
func loadMariaBackup(
	t *testing.T, ctx context.Context, c testcontainers.Container, prefixDir, identity string,
) {
	t.Helper()
	entries, err := os.ReadDir(prefixDir)
	require.NoError(t, err)

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(prefixDir, e.Name())) //nolint:gosec // a path this test just created
		require.NoError(t, err)
		require.NoError(t, c.CopyToContainer(ctx, body, "/restore/"+e.Name(), 0o644))
	}
	require.NoError(t, c.CopyToContainer(ctx,
		[]byte(identity+"\n"), "/restore/koffr-identity.txt", 0o600))
}

// loadBinlogs copies the archived binary log objects from the backup's own
// file onwards to the bare machine, in order, and returns their names -- what
// the document calls BINLOG_FILES. Files before the anchor are left behind on
// purpose: the document says "from <file> onwards", and this test follows it.
func loadBinlogs(t *testing.T, ctx context.Context, c testcontainers.Container, binlogDir, anchorFile string) []string {
	t.Helper()
	entries, err := os.ReadDir(binlogDir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".zst.age") {
			continue
		}
		if strings.TrimSuffix(e.Name(), ".zst.age") < anchorFile {
			continue
		}
		body, err := os.ReadFile(filepath.Join(binlogDir, e.Name())) //nolint:gosec // a path this test just created
		require.NoError(t, err)
		require.NoError(t, c.CopyToContainer(ctx, body, "/restore/"+e.Name(), 0o644))
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// lockedBuffer is a strings.Builder that can be written by one goroutine and
// read by another.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// mariadbImage is the server under test; make verify-mariadb-matrix walks the
// supported majors through it, CI pins one.
func mariadbImage() string {
	if img := os.Getenv("KOFFR_MARIADB_IMAGE"); img != "" {
		return img
	}
	return "mariadb:11.4"
}
