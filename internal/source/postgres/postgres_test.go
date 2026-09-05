package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/executor/local"
	"github.com/Gu1llaum-3/koffr/internal/source"
	"github.com/Gu1llaum-3/koffr/internal/source/postgres"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

const (
	adminUser = "postgres"
	adminPass = "probe-admin"
	database  = "probe"
)

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
		if shared.skipWhy == "" {
			if _, err := exec.LookPath("pg_dump"); err != nil {
				// CT-001: Koffr shells out to client binaries it cannot embed.
				// Saying so plainly beats a confusing failure deep in a test.
				shared.skipWhy = "pg_dump is not on PATH"
			}
		}
		if shared.skipWhy == "" {
			if err := startPostgres(); err != nil {
				shared.skipWhy = fmt.Sprintf("postgres container unavailable: %v", err)
			}
		}
		if _, fatal := testutil.SkipOrFailWithoutDocker(shared.skipWhy); fatal != "" {
			fmt.Fprintln(os.Stderr, fatal)
			return 1
		}
		return m.Run()
	}())
}

func startPostgres() error {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:17",
		tcpostgres.WithDatabase(database),
		tcpostgres.WithUsername(adminUser),
		tcpostgres.WithPassword(adminPass),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")),
	)
	if err != nil {
		return err
	}
	shared.container = container
	host, err := container.Host(ctx)
	if err != nil {
		return err
	}
	port, err := container.MappedPort(ctx, "5432/tcp")
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
// accepts queries", which testcontainers' port check does not cover.
func waitReady(ctx context.Context) error {
	var lastErr error
	for range 60 {
		conn, err := pgx.Connect(ctx, adminDSN())
		if err == nil {
			_ = conn.Close(ctx)
			return nil
		}
		lastErr = err
	}
	return lastErr
}

func adminDSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		adminUser, adminPass, shared.host, shared.port, database)
}

func skipUnlessReady(t *testing.T) {
	t.Helper()
	if shared.skipWhy != "" {
		t.Skip(shared.skipWhy)
	}
}

// admin runs statements as the superuser, for arranging each test's fixture.
//
// It deliberately does not use t.Context(): since Go 1.24 that context is
// cancelled just before cleanup functions run, so every t.Cleanup calling this
// would fail on a cancelled context rather than tidying up.
func admin(t *testing.T, statements ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, adminDSN())
	require.NoError(t, err)
	defer func() { assert.NoError(t, conn.Close(ctx)) }()
	for _, s := range statements {
		_, err := conn.Exec(ctx, s)
		require.NoError(t, err, "statement: %s", s)
	}
}

// containerExec runs a command inside the database container, for fixtures that
// need something on its filesystem rather than in its catalog.
func containerExec(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	code, reader, err := shared.container.Exec(ctx, args)
	require.NoError(t, err)
	out, _ := io.ReadAll(reader)
	require.Zero(t, code, "%v: %s", args, out)
}

func baseConfig() postgres.Config {
	return postgres.Config{
		Host:     shared.host,
		Port:     shared.port,
		User:     adminUser,
		Password: adminPass,
		Database: database,
		SSLMode:  "disable",
	}
}

func newSource(t *testing.T, cfg postgres.Config) *postgres.Logical {
	t.Helper()
	s, err := postgres.NewLogical(cfg)
	require.NoError(t, err)
	return s
}

func localExec(t *testing.T) executor.Executor {
	t.Helper()
	ex := local.New()
	t.Cleanup(func() { assert.NoError(t, ex.Close()) })
	return ex
}

func TestProbe_ReportsServerAndKinds(t *testing.T) {
	skipUnlessReady(t)

	info, err := newSource(t, baseConfig()).Probe(t.Context(), localExec(t))
	require.NoError(t, err)

	assert.Equal(t, source.EnginePostgreSQL, info.Engine)
	assert.True(t, strings.HasPrefix(info.ServerVersion, "17."),
		"got %q", info.ServerVersion)
	assert.Contains(t, info.Kinds, source.KindLogical)
}

// PD-006 and CT-001: pg_dump must be at least the server's version, and a
// mismatch is a configuration problem to report at validation rather than a
// confusing failure at 3 AM.
func TestProbe_RejectsMissingOrOlderClient(t *testing.T) {
	skipUnlessReady(t)

	cfg := baseConfig()
	cfg.BinDir = t.TempDir() // empty: no pg_dump here
	_, err := newSource(t, cfg).Probe(t.Context(), localExec(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pg_dump")
}

// P-007, closed here rather than in M2: streamed physical backup cannot cover a
// cluster with more than one tablespace, and the check is a plain query, so it
// belongs in Probe where a configuration can still be refused (EF-005).
func TestProbe_ReportsExtraTablespaces(t *testing.T) {
	skipUnlessReady(t)
	src := newSource(t, baseConfig())

	info, err := src.Probe(t.Context(), localExec(t))
	require.NoError(t, err)
	assert.NotContains(t, strings.Join(info.Restrictions, " "), "tablespace",
		"a stock cluster has only the default tablespaces")

	// A tablespace needs a directory the server owns; CREATE TABLESPACE will not
	// make one.
	containerExec(t, "mkdir", "-p", "/tmp/probe_extra")
	containerExec(t, "chown", "postgres:postgres", "/tmp/probe_extra")
	admin(t, "CREATE TABLESPACE probe_extra LOCATION '/tmp/probe_extra'")
	t.Cleanup(func() { admin(t, "DROP TABLESPACE IF EXISTS probe_extra") })

	info, err = src.Probe(t.Context(), localExec(t))
	require.NoError(t, err)
	assert.NotContains(t, info.Kinds, source.KindPhysical)
	assert.Contains(t, strings.Join(info.Restrictions, " "), "tablespace",
		"the restriction must be reported so a configuration can be refused")
}

// EF-019: a dump that silently omits a table the role cannot read is worse than
// no dump, because it looks like a backup. The check runs before pg_dump is
// started, not after it has already written half an archive.
func TestProbe_RefusesRoleThatCannotReadEverything(t *testing.T) {
	skipUnlessReady(t)

	admin(t,
		"DROP TABLE IF EXISTS unreadable",
		"CREATE TABLE unreadable (id int)",
		"DROP ROLE IF EXISTS probe_limited",
		"CREATE ROLE probe_limited LOGIN PASSWORD 'limited'",
		"GRANT CONNECT ON DATABASE probe TO probe_limited",
		"GRANT USAGE ON SCHEMA public TO probe_limited",
	)
	t.Cleanup(func() {
		admin(t,
			"DROP OWNED BY probe_limited",
			"DROP ROLE IF EXISTS probe_limited",
			"DROP TABLE IF EXISTS unreadable")
	})

	cfg := baseConfig()
	cfg.User, cfg.Password = "probe_limited", "limited"

	_, err := newSource(t, cfg).Probe(t.Context(), localExec(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unreadable",
		"the error must name the relation the role cannot read")
}

func TestOpen_ProducesARestorableDump(t *testing.T) {
	skipUnlessReady(t)
	admin(t,
		"DROP TABLE IF EXISTS widgets",
		"CREATE TABLE widgets (id int PRIMARY KEY, label text)",
		"INSERT INTO widgets SELECT g, 'w-' || g FROM generate_series(1, 500) g",
	)
	t.Cleanup(func() { admin(t, "DROP TABLE IF EXISTS widgets") })

	stream, err := newSource(t, baseConfig()).Open(t.Context(), localExec(t), source.Request{
		Kind:  source.KindLogical,
		Label: "probe",
	})
	require.NoError(t, err)

	dump := filepath(t, "dump.pgdump")
	f, err := os.Create(dump) //nolint:gosec // a path this test just built
	require.NoError(t, err)
	n, err := io.Copy(f, stream.Reader)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.NoError(t, stream.Close())
	assert.Positive(t, n)

	assert.Equal(t, source.CodecNone, stream.Codec,
		"the dump is uncompressed: zstd in the pipeline compresses better and faster")

	// pg_restore reading it is the only proof that matters; a non-empty stream
	// proves nothing about the archive being well formed.
	out, err := exec.CommandContext(t.Context(), "pg_restore", "--list", dump).CombinedOutput()
	require.NoError(t, err, "pg_restore rejected the archive: %s", out)
	assert.Contains(t, string(out), "widgets")
}

func TestOpen_HonoursFilters(t *testing.T) {
	skipUnlessReady(t)
	admin(t,
		"DROP SCHEMA IF EXISTS kept CASCADE",
		"DROP SCHEMA IF EXISTS dropped CASCADE",
		"CREATE SCHEMA kept",
		"CREATE SCHEMA dropped",
		"CREATE TABLE kept.wanted (id int)",
		"CREATE TABLE dropped.unwanted (id int)",
	)
	t.Cleanup(func() {
		admin(t, "DROP SCHEMA IF EXISTS kept CASCADE", "DROP SCHEMA IF EXISTS dropped CASCADE")
	})

	stream, err := newSource(t, baseConfig()).Open(t.Context(), localExec(t), source.Request{
		Kind:           source.KindLogical,
		ExcludeSchemas: []string{"dropped"},
	})
	require.NoError(t, err)

	dump := filepath(t, "filtered.pgdump")
	f, err := os.Create(dump) //nolint:gosec // a path this test just built
	require.NoError(t, err)
	_, err = io.Copy(f, stream.Reader)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.NoError(t, stream.Close())

	out, err := exec.CommandContext(t.Context(), "pg_restore", "--list", dump).CombinedOutput()
	require.NoError(t, err, "%s", out)
	assert.Contains(t, string(out), "wanted")
	assert.NotContains(t, string(out), "unwanted")
}

// pg_dumpall --globals-only carries roles and tablespaces, which a dump of one
// database does not. Restoring without them produces a database whose owners
// and grants do not exist.
func TestOpen_ProducesGlobalsSidecar(t *testing.T) {
	skipUnlessReady(t)

	stream, err := newSource(t, baseConfig()).Open(t.Context(), localExec(t), source.Request{
		Kind: source.KindLogical,
	})
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, stream.Reader)
	require.NoError(t, err)

	sidecars, err := stream.Sidecars()
	require.NoError(t, err)
	require.NoError(t, stream.Close())

	globals, ok := sidecars["globals.sql"]
	require.True(t, ok, "sidecars: %v", keys(sidecars))
	assert.Contains(t, string(globals), "ROLE "+adminUser)
	testutil.AssertNoSecretLeak(t, string(globals))
}

// ENF-021. argv is world-readable in /proc/<pid>/cmdline, so a password there
// is visible to every user on the machine for as long as the dump runs.
func TestOpen_NoCredentialInArguments(t *testing.T) {
	skipUnlessReady(t)

	cfg := baseConfig()
	cfg.Password = adminPass
	rendered, err := newSource(t, cfg).RenderCommand(source.Request{Kind: source.KindLogical}, 5432)
	require.NoError(t, err)

	testutil.AssertNoSecretLeak(t, strings.Join(rendered, " "))
	assert.NotContains(t, strings.Join(rendered, " "), adminPass)
}

func TestOpen_CancellationStopsTheDump(t *testing.T) {
	skipUnlessReady(t)
	admin(t,
		"DROP TABLE IF EXISTS big",
		"CREATE TABLE big (id bigint, payload text)",
		"INSERT INTO big SELECT g, repeat('x', 900) FROM generate_series(1, 200000) g",
	)
	t.Cleanup(func() { admin(t, "DROP TABLE IF EXISTS big") })

	ctx, cancel := context.WithCancel(t.Context())
	stream, err := newSource(t, baseConfig()).Open(ctx, localExec(t), source.Request{Kind: source.KindLogical})
	require.NoError(t, err)

	// Read a little, then abandon it exactly as a failing pipeline would.
	_, err = io.CopyN(io.Discard, stream.Reader, 4096)
	require.NoError(t, err)
	cancel()

	done := make(chan error, 1)
	go func() { done <- stream.Close() }()
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("Close blocked after cancellation")
	}
}

// indirect reports Direct false while dialling exactly like the local executor.
//
// That is what an SSH executor looks like to this package, and it forces the
// tunnelled path without needing a bastion container: what is under test is the
// ordering P-004 established, not SSH itself.
type indirect struct{ executor.Executor }

func (indirect) Capabilities() executor.Capabilities {
	return executor.Capabilities{CanDial: true, CanExec: false, Direct: false, Target: "indirect"}
}

// P-004: libpq matches .pgpass on host AND port, and a tunnel's port is chosen
// by the kernel, so the credentials file can only be written once the listener
// is bound.
//
// The proof is indirect and all the stronger for it: the server requires a
// password, so a .pgpass written with the wrong port produces an authentication
// failure. A dump that completes through the tunnel is a dump whose credentials
// file carried the port the kernel picked.
func TestOpen_ThroughATunnel(t *testing.T) {
	skipUnlessReady(t)
	admin(t,
		"DROP TABLE IF EXISTS tunnelled",
		"CREATE TABLE tunnelled (id int PRIMARY KEY)",
		"INSERT INTO tunnelled SELECT generate_series(1, 100)",
	)
	t.Cleanup(func() { admin(t, "DROP TABLE IF EXISTS tunnelled") })

	stream, err := newSource(t, baseConfig()).Open(t.Context(),
		indirect{Executor: localExec(t)}, source.Request{Kind: source.KindLogical})
	require.NoError(t, err)

	dump := filepath(t, "tunnelled.pgdump")
	f, err := os.Create(dump) //nolint:gosec // a path this test just built
	require.NoError(t, err)
	_, err = io.Copy(f, stream.Reader)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.NoError(t, stream.Close())

	out, err := exec.CommandContext(t.Context(), "pg_restore", "--list", dump).CombinedOutput()
	require.NoError(t, err, "%s", out)
	assert.Contains(t, string(out), "tunnelled")
}

// An executor that can neither dial directly nor be tunnelled must be refused
// when the backup is set up, not halfway through it (PD-006).
func TestOpen_RefusesExecutorThatCannotReachTheDatabase(t *testing.T) {
	skipUnlessReady(t)

	_, err := newSource(t, baseConfig()).Open(t.Context(), noRoute{}, source.Request{Kind: source.KindLogical})
	require.Error(t, err)
}

type noRoute struct{}

func (noRoute) Dial(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("no route")
}
func (noRoute) Start(context.Context, executor.Command) (executor.Process, error) {
	return nil, errors.New("no exec")
}
func (noRoute) Capabilities() executor.Capabilities {
	return executor.Capabilities{Target: "no-route"}
}
func (noRoute) Close() error { return nil }

func filepath(t *testing.T, name string) string {
	t.Helper()
	return t.TempDir() + "/" + name
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
