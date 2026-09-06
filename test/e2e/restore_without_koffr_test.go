// Package e2e_test holds the test that makes PD-001 true rather than
// declarative.
//
// Every other test in this repository exercises Koffr. This one exercises its
// absence: a backup is taken, and then restored on a machine that has never
// heard of Koffr, using only the commands the generated RESTORE.md contains.
//
// The commands are extracted from the document rather than written here. That
// is the whole point. A test that reimplemented equivalent commands would pass
// while the document was wrong, which is the failure mode PD-001 exists to
// prevent -- and the one that only shows up years later, when someone needs
// their data and Koffr is gone.
package e2e_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Gu1llaum-3/koffr/internal/cli"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

const (
	sourcePassword = "e2e-source-password"
	restoredDB     = "shop_restored"
)

// wanted is what the restored database must contain, exactly.
var wanted = map[string]int{
	"customers": 500,
	"orders":    2000,
	"items":     50,
}

func TestRestoreWithoutKoffr(t *testing.T) {
	unavailable := testutil.EnsureDockerHost()
	skip, fatal := testutil.SkipOrFailWithoutDocker(unavailable)
	if fatal != "" {
		t.Fatal(fatal)
	}
	if skip {
		t.Skip("no container runtime: " + unavailable)
	}
	// Koffr shells out to the client binaries and cannot embed them (CT-001),
	// so taking the backup half of this test needs pg_dump on this machine.
	testutil.SkipOrFailWithoutTool(t, "pg_dump", "the backup half of this test runs here")

	ctx := context.Background()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")

	source := startPostgres(t, ctx)
	seed(t, ctx, source)

	identity, cfgPath := writeConfig(t, dir, repo, mappedPort(t, ctx, source))

	// Koffr's only appearance. Everything after this happens without it.
	code, out, errOut := runKoffr(t, "--config", cfgPath, "backup", "shop")
	require.Equal(t, cli.ExitOK, code, "stdout: %s stderr: %s", out, errOut)

	doc, prefix := readRestoreDoc(t, repo)
	t.Logf("RESTORE.md:\n%s", doc)

	commands := shellBlocks(doc)
	require.NotEmpty(t, commands, "a procedure with no commands is not a procedure")

	// P-006, and a regression test on a measured finding: pg_restore stops
	// reading at the archive's end marker, so the decompressor is killed by
	// SIGPIPE and exits 141 on a restore that worked perfectly. A document whose
	// commands set pipefail would turn every success into a failure.
	//
	// The commands, not the document: the prose is expected to mention pipefail,
	// because warning the reader off it is the whole point.
	for _, cmd := range commands {
		assert.NotContains(t, cmd, "pipefail")
	}
	assert.Contains(t, doc, "pipefail", "the document has to warn about it, not merely avoid it")

	target := startBareMachine(t, ctx)
	loadBackup(t, ctx, target, filepath.Join(repo, prefix), identity)

	// pg_dumpall --globals-only recreates every role in the cluster, including
	// the superuser, so psql reports an error for each one that already exists
	// and exits non-zero. The document says so in as many words; this is the
	// test honouring that rather than pretending the exit status is meaningful.
	assert.Contains(t, doc, "expected and not a failure",
		"a step that exits non-zero on success has to say so, or an operator scripting "+
			"this document treats a working restore as a failed one")

	for i, cmd := range commands {
		// DBNAME is the one substitution the document itself asks for: the
		// database to restore *into* is the reader's choice, and the document
		// says so. Everything else runs exactly as written.
		cmd = strings.ReplaceAll(cmd, "DBNAME", restoredDB)
		runInContainer(t, ctx, target, fmt.Sprintf("step-%02d", i), cmd,
			strings.Contains(cmd, "psql --dbname=postgres"))
	}

	for table, want := range wanted {
		got := queryInt(t, ctx, target, restoredDB,
			"SELECT count(*) FROM "+table)
		assert.Equal(t, want, got, "table %s came back with the wrong number of rows", table)
	}

	// Row counts alone would pass on a dump that lost every value. The checksum
	// is what says the data is the same data.
	assert.Equal(t,
		queryString(t, ctx, source, "shop", checksumQuery),
		queryString(t, ctx, target, restoredDB, checksumQuery),
		"the restored data does not match the original")

	// Roles live in the cluster, not in the database, so they arrive only if
	// the globals sidecar was restored. Without them the restored database has
	// owners and grants that do not exist.
	assert.Equal(t, 1, queryInt(t, ctx, target, "postgres",
		"SELECT count(*) FROM pg_roles WHERE rolname = 'shop_reader'"),
		"the globals sidecar did not arrive, so the restored database's grants are broken")
}

const checksumQuery = `SELECT md5(string_agg(id::text || ':' || total::text, ',' ORDER BY id)) FROM orders`

// shellBlocks returns the contents of every ```sh block in the document, in
// order, one command per entry.
func shellBlocks(doc string) []string {
	var out []string
	var inBlock bool
	var block strings.Builder

	scanner := bufio.NewScanner(strings.NewReader(doc))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "```sh"):
			inBlock, block = true, strings.Builder{}
		case inBlock && strings.HasPrefix(line, "```"):
			inBlock = false
			out = append(out, splitCommands(block.String())...)
		case inBlock:
			block.WriteString(line)
			block.WriteByte('\n')
		}
	}
	return out
}

// splitCommands turns a block into individual commands, keeping a continued
// line with the one it belongs to.
func splitCommands(block string) []string {
	var out []string
	var current strings.Builder
	for _, line := range strings.Split(block, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		current.WriteString(line)
		if strings.HasSuffix(strings.TrimRight(line, " "), "\\") {
			current.WriteByte('\n')
			continue
		}
		out = append(out, current.String())
		current.Reset()
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}

func startPostgres(t *testing.T, ctx context.Context) *tcpostgres.PostgresContainer {
	t.Helper()
	c, err := tcpostgres.Run(ctx, "postgres:17",
		tcpostgres.WithDatabase("shop"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword(sourcePassword),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(90*time.Second)),
	)
	require.NoError(t, err)
	// context.Background rather than the test's: Go cancels t.Context()
	// before cleanups run, and a cancelled context cannot stop a container.
	//nolint:contextcheck // teardown outlives the test context by design
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	return c
}

// startBareMachine is a machine with age, zstd and the PostgreSQL tools, and
// nothing of Koffr's.
func startBareMachine(t *testing.T, ctx context.Context) testcontainers.Container {
	t.Helper()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{Context: "testdata", KeepImage: true},
			Env:            map[string]string{"POSTGRES_PASSWORD": "restore-target"},
			WaitingFor: wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(120 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)
	// context.Background rather than the test's: Go cancels t.Context()
	// before cleanups run, and a cancelled context cannot stop a container.
	//nolint:contextcheck // teardown outlives the test context by design
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	// If Koffr were reachable here the test would prove nothing.
	code, _, err := c.Exec(ctx, []string{"sh", "-c", "command -v koffr"})
	require.NoError(t, err)
	require.NotEqual(t, 0, code, "this machine is supposed to have no koffr")
	return c
}

func mappedPort(t *testing.T, ctx context.Context, c *tcpostgres.PostgresContainer) int {
	t.Helper()
	p, err := c.MappedPort(ctx, "5432/tcp")
	require.NoError(t, err)
	n, err := strconv.Atoi(p.Port())
	require.NoError(t, err)
	return n
}

// seed builds a database with a role, three tables and a foreign key, so the
// restore has something to get wrong.
func seed(t *testing.T, ctx context.Context, c *tcpostgres.PostgresContainer) {
	t.Helper()
	statements := fmt.Sprintf(`
		CREATE ROLE shop_reader;
		CREATE TABLE items(id serial PRIMARY KEY, sku text NOT NULL UNIQUE);
		CREATE TABLE customers(id serial PRIMARY KEY, name text NOT NULL);
		CREATE TABLE orders(
			id serial PRIMARY KEY,
			customer_id int NOT NULL REFERENCES customers(id),
			item_id int NOT NULL REFERENCES items(id),
			total numeric(10,2) NOT NULL);
		INSERT INTO items(sku) SELECT 'SKU-'||g FROM generate_series(1,%d) g;
		INSERT INTO customers(name) SELECT 'customer '||g FROM generate_series(1,%d) g;
		INSERT INTO orders(customer_id, item_id, total)
			SELECT (g %% %d) + 1, (g %% %d) + 1, (g * 7 %% 10000)::numeric / 100
			FROM generate_series(1,%d) g;
		GRANT SELECT ON ALL TABLES IN SCHEMA public TO shop_reader;`,
		wanted["items"], wanted["customers"],
		wanted["customers"], wanted["items"], wanted["orders"])

	code, out, err := c.Exec(ctx, []string{"psql", "-U", "postgres", "-d", "shop", "-v", "ON_ERROR_STOP=1", "-c", statements})
	require.NoError(t, err)
	require.Equal(t, 0, code, "seeding failed: %s", read(out))
}

func writeConfig(t *testing.T, dir, repo string, port int) (identity, cfgPath string) {
	t.Helper()
	identity, recipient := testutil.AgeIdentity(t)
	_, recovery := testutil.AgeIdentity(t)

	t.Setenv("KOFFR_IDENTITY", identity)
	t.Setenv("KOFFR_E2E_PASSWORD", sourcePassword)

	cfgPath = filepath.Join(dir, "koffr.yml")
	content := fmt.Sprintf(`version: 1
crypto:
  recipients:
    - %s
    - %s
  identity: env:KOFFR_IDENTITY
catalog:
  path: %s
destinations:
  main:
    type: fs
    path: %s
sources:
  shop:
    engine: postgresql
    host: 127.0.0.1
    port: %d
    user: postgres
    password: env:KOFFR_E2E_PASSWORD
    database: shop
    sslmode: disable
    destinations: [main]
`, recipient, recovery, filepath.Join(dir, "catalog.db"), repo, port)
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0o600))
	return identity, cfgPath
}

func runKoffr(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errBuf strings.Builder
	code := cli.Run(t.Context(), args, cli.Streams{Out: &out, Err: &errBuf})
	return code, out.String(), errBuf.String()
}

// readRestoreDoc finds the one backup in the repository and returns its
// procedure, plus the prefix its objects live under.
func readRestoreDoc(t *testing.T, repo string) (doc, prefix string) {
	t.Helper()
	var found string
	require.NoError(t, filepath.Walk(repo, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Name() == "RESTORE.md" {
			found = path
		}
		return nil
	}))
	require.NotEmpty(t, found, "the backup produced no restore procedure")

	body, err := os.ReadFile(found) //nolint:gosec // a path this test just created
	require.NoError(t, err)

	rel, err := filepath.Rel(repo, filepath.Dir(found))
	require.NoError(t, err)
	return string(body), rel
}

// loadBackup puts the objects and the identity on the bare machine, the way an
// operator would after downloading them. Step 1 of the document is "fetch the
// objects", and it is the one step a test cannot run for real.
func loadBackup(t *testing.T, ctx context.Context, c testcontainers.Container, prefixDir, identity string) {
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

	// CopyToContainer writes as root; the commands run as postgres.
	code, out, err := c.Exec(ctx, []string{"chown", "-R", "postgres:postgres", "/restore"})
	require.NoError(t, err)
	require.Equal(t, 0, code, read(out))
}

// runInContainer runs one command from the document, as the postgres user, in
// the directory holding the objects.
//
// The command goes in through a file rather than a shell argument, so that
// nothing in this test re-quotes what the document said.
func runInContainer(
	t *testing.T, ctx context.Context, c testcontainers.Container,
	name, command string, exitStatusIsNotMeaningful bool,
) {
	t.Helper()
	script := "/restore/" + name + ".sh"
	require.NoError(t, c.CopyToContainer(ctx, []byte(command+"\n"), script, 0o755))

	code, out, err := c.Exec(ctx,
		[]string{"su", "postgres", "-s", "/bin/sh", "-c", "cd /restore && sh " + script},
		tcexec.Multiplexed())
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

func queryInt(t *testing.T, ctx context.Context, c interface {
	Exec(context.Context, []string, ...tcexec.ProcessOption) (int, io.Reader, error)
}, database, query string) int {
	t.Helper()
	var n int
	_, err := fmt.Sscanf(strings.TrimSpace(queryRaw(t, ctx, c, database, query)), "%d", &n)
	require.NoError(t, err)
	return n
}

func queryString(t *testing.T, ctx context.Context, c interface {
	Exec(context.Context, []string, ...tcexec.ProcessOption) (int, io.Reader, error)
}, database, query string) string {
	t.Helper()
	return strings.TrimSpace(queryRaw(t, ctx, c, database, query))
}

func queryRaw(t *testing.T, ctx context.Context, c interface {
	Exec(context.Context, []string, ...tcexec.ProcessOption) (int, io.Reader, error)
}, database, query string) string {
	t.Helper()
	code, out, err := c.Exec(ctx,
		[]string{"psql", "-U", "postgres", "-d", database, "-tAc", query},
		tcexec.Multiplexed())
	require.NoError(t, err)
	body := read(out)
	require.Equal(t, 0, code, "query failed: %s", body)
	return body
}

func read(r io.Reader) string {
	body, err := io.ReadAll(r)
	if err != nil {
		return ""
	}
	return string(body)
}
