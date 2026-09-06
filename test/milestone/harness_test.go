//go:build milestone

package milestone_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Gu1llaum-3/koffr/internal/cli"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

const (
	sourceID   = "bulk"
	sourceDB   = "bulk"
	pgPassword = "milestone-password"
)

// TestMain applies the same gate as every other suite that needs containers:
// skip on a laptop without a runtime, fail under CI. A milestone gate that
// reported success because it never ran would be the worst instance of the
// failure mode this repository keeps fighting.
func TestMain(m *testing.M) {
	unavailable := testutil.EnsureDockerHost()
	skip, fatal := testutil.SkipOrFailWithoutDocker(unavailable)
	if fatal != "" {
		fmt.Fprintln(os.Stderr, fatal)
		os.Exit(1)
	}
	if skip {
		fmt.Fprintln(os.Stderr, "no container runtime:", unavailable)
		os.Exit(0)
	}
	if _, err := exec.LookPath("pg_dump"); err != nil {
		fmt.Fprintln(os.Stderr, "pg_dump is not on PATH; the backup half runs here")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// ---------------------------------------------------------------- report
//
// The criteria are printed as a table at the end rather than left as a pile of
// assertions. A milestone gate whose output has to be read backwards from a
// stack trace is one whose result gets summarised as "it failed", which is not
// the same as knowing which criterion did.

type report struct {
	rows []reportRow
}

type reportRow struct {
	name, value, why string
	ok               bool
}

func newReport(t *testing.T) *report {
	t.Helper()
	return &report{}
}

func (r *report) add(name, value string, ok bool, why string) {
	r.rows = append(r.rows, reportRow{name: name, value: value, why: why, ok: ok})
}

func (r *report) print(t *testing.T) {
	t.Helper()
	var failed int
	var b strings.Builder
	b.WriteString("\n=== M1 exit criteria ===\n")
	for _, row := range r.rows {
		mark := "PASS"
		if !row.ok {
			mark, failed = "FAIL", failed+1
		}
		fmt.Fprintf(&b, "  %-4s %-34s %-18s %s\n", mark, row.name, row.value, row.why)
	}
	fmt.Fprintf(&b, "  %d of %d\n", len(r.rows)-failed, len(r.rows))
	t.Log(b.String())

	if failed > 0 {
		t.Fatalf("%d exit criteria not met", failed)
	}
}

// ---------------------------------------------------------------- containers

func startPostgres(t *testing.T, ctx context.Context) *tcpostgres.PostgresContainer {
	t.Helper()
	c, err := tcpostgres.Run(ctx, "postgres:17",
		tcpostgres.WithDatabase(sourceDB),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword(pgPassword),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(3*time.Minute)),
	)
	require.NoError(t, err)
	//nolint:contextcheck // teardown outlives the test context by design
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	port, err := c.MappedPort(ctx, "5432/tcp")
	require.NoError(t, err)
	sharedPGPort, err = strconv.Atoi(port.Port())
	require.NoError(t, err)
	return c
}

// seedTo fills the database until it is at least sizeGB gibibytes on disk.
//
// Incompressible-ish text on purpose: a table of repeated values would compress
// to almost nothing and turn a ten gibibyte criterion into a ten megabyte
// upload, which measures the wrong thing entirely.
func seedTo(t *testing.T, ctx context.Context, c *tcpostgres.PostgresContainer, sizeGB int) {
	t.Helper()
	start := time.Now()
	t.Logf("seeding %d GiB; this is the slow part", sizeGB)

	psql(t, ctx, c, sourceDB, `
		CREATE ROLE bulk_reader;
		CREATE TABLE bulk(
			id bigserial PRIMARY KEY,
			payload text NOT NULL,
			amount numeric(12,2) NOT NULL);
		GRANT SELECT ON ALL TABLES IN SCHEMA public TO bulk_reader;`)

	// Seed until the database *measures* the target, rather than trusting a
	// rows-per-gibibyte constant. The first attempt used one and produced 679
	// MiB for a nominal gibibyte -- a third short, which at ten would have been
	// a criterion quietly measured at seven.
	target := int64(sizeGB) << 30
	const batch = 2_000_000
	for from := int64(1); ; from += batch {
		psql(t, ctx, c, sourceDB, fmt.Sprintf(`
			INSERT INTO bulk(payload, amount)
			SELECT md5(g::text) || md5((g*7)::text) || md5((g*13)::text) ||
			       md5((g*17)::text) || md5((g*19)::text) || md5((g*23)::text),
			       (g %% 100000)::numeric / 100
			FROM generate_series(%d, %d) g;`, from, from+batch-1))

		size, err := strconv.ParseInt(
			query(t, ctx, c, sourceDB, "SELECT pg_database_size(current_database())"), 10, 64)
		require.NoError(t, err)
		t.Logf("  %s / %s (%s)", humanBytes(size), humanBytes(target), time.Since(start).Round(time.Second))
		if size >= target {
			break
		}
	}
	psql(t, ctx, c, sourceDB, "VACUUM ANALYZE bulk")
}

// ---------------------------------------------------------------- fingerprint

type dbFingerprint struct {
	rows     int
	checksum string
	bytes    int64
}

// fingerprint is what "restored correctly" means, at a size where reading every
// value twice is not free.
//
// Row counts alone would pass on a dump that lost every value, so the checksum
// is over the data. It is an aggregate rather than a per-row hash because at
// ten gibibytes the difference is minutes.
func fingerprint(t *testing.T, ctx context.Context, c *tcpostgres.PostgresContainer, database string) dbFingerprint {
	t.Helper()
	rows, err := strconv.Atoi(query(t, ctx, c, database, "SELECT count(*) FROM bulk"))
	require.NoError(t, err)

	sum := query(t, ctx, c, database,
		`SELECT md5(sum(amount)::text || count(*)::text || sum(length(payload))::text ||
		            md5(string_agg(payload, '' ORDER BY id))::text)
		 FROM (SELECT id, payload, amount FROM bulk ORDER BY id LIMIT 200000) s`)

	size, err := strconv.ParseInt(
		query(t, ctx, c, database, "SELECT pg_database_size(current_database())"), 10, 64)
	require.NoError(t, err)

	return dbFingerprint{rows: rows, checksum: sum, bytes: size}
}

func psql(t *testing.T, ctx context.Context, c *tcpostgres.PostgresContainer, database, sql string) {
	t.Helper()
	code, out, err := c.Exec(ctx,
		[]string{"psql", "-U", "postgres", "-d", database, "-v", "ON_ERROR_STOP=1", "-c", sql},
		tcexec.Multiplexed())
	require.NoError(t, err)
	require.Equal(t, 0, code, "%s", read(out))
}

func query(t *testing.T, ctx context.Context, c *tcpostgres.PostgresContainer, database, sql string) string {
	t.Helper()
	code, out, err := c.Exec(ctx,
		[]string{"psql", "-U", "postgres", "-d", database, "-tAc", sql}, tcexec.Multiplexed())
	require.NoError(t, err)
	body := strings.TrimSpace(read(out))
	require.Equal(t, 0, code, "%s", body)
	return body
}

// ---------------------------------------------------------------- heap

// measurePeakHeap samples the heap while fn runs and returns the highest
// reading.
//
// Sampling rather than a before-and-after difference: the pipeline's whole
// claim is that memory does not grow with the database, and a measurement taken
// only at the end would miss a peak in the middle -- which is exactly where a
// buffering bug would put one.
func measurePeakHeap(t *testing.T, fn func()) uint64 {
	t.Helper()
	runtime.GC()

	var peak atomic.Uint64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				runtime.ReadMemStats(&m)
				for {
					old := peak.Load()
					if m.HeapAlloc <= old || peak.CompareAndSwap(old, m.HeapAlloc) {
						break
					}
				}
			}
		}
	}()

	fn()
	close(stop)
	<-done
	return peak.Load()
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func read(r interface{ Read([]byte) (int, error) }) string {
	body := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		body = append(body, buf[:n]...)
		if err != nil {
			return string(body)
		}
	}
}

// ---------------------------------------------------------------- destinations

// destination is one repository to prove the criteria against. Criterion 1 asks
// for both, because "it works on a filesystem" says nothing about the backend
// that has a network in the middle.
type destination struct {
	name   string
	config string // path to a koffr.yml pointing at it
}

func fsDestination(t *testing.T, dataRoot string) destination {
	t.Helper()
	dir := subdir(t, dataRoot, "fs")
	return destination{
		name: "fs",
		config: writeConfig(t, dir, fmt.Sprintf(`
  main:
    type: fs
    path: %s`, filepath.Join(dir, "repo"))),
	}
}

func s3Destination(t *testing.T, ctx context.Context, dataRoot string) destination {
	t.Helper()
	const user, pass, bucket = "milestone", "milestone-secret", "koffr"

	c, err := tcminio.Run(ctx, "minio/minio:RELEASE.2025-04-22T22-12-26Z",
		tcminio.WithUsername(user), tcminio.WithPassword(pass))
	require.NoError(t, err)
	//nolint:contextcheck // teardown outlives the test context by design
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	endpoint, err := c.ConnectionString(ctx)
	require.NoError(t, err)

	code, out, err := c.Exec(ctx, []string{"sh", "-c",
		fmt.Sprintf("mc alias set local http://localhost:9000 %s %s && mc mb -p local/%s", user, pass, bucket)},
		tcexec.Multiplexed())
	require.NoError(t, err)
	require.Equal(t, 0, code, "%s", read(out))

	t.Setenv("KOFFR_S3_KEY", user)
	t.Setenv("KOFFR_S3_SECRET", pass)

	dir := subdir(t, dataRoot, "s3")
	return destination{
		name: "s3",
		config: writeConfig(t, dir, fmt.Sprintf(`
  main:
    type: s3
    bucket: %s
    region: us-east-1
    endpoint: http://%s
    access_key_id: env:KOFFR_S3_KEY
    secret_access_key: env:KOFFR_S3_SECRET`, bucket, endpoint)),
	}
}

// writeConfig builds a configuration around one destination block.
//
// TMPDIR points at an empty directory of its own, which is what makes criterion
// 4 measurable: anything Koffr stages goes through os.CreateTemp, and that
// follows TMPDIR.
func writeConfig(t *testing.T, dir, destinations string) string {
	t.Helper()
	// One identity for the whole run. Generating a pair per destination made
	// the second call overwrite KOFFR_IDENTITY, and the first repository became
	// undecryptable halfway through the test.
	identity, recipient, recovery := sharedKeys(t)
	t.Setenv("KOFFR_IDENTITY", identity)
	t.Setenv("KOFFR_PG_PASSWORD", pgPassword)

	path := filepath.Join(dir, "koffr.yml")
	content := fmt.Sprintf(`version: 1
crypto:
  recipients:
    - %s
    - %s
  identity: env:KOFFR_IDENTITY
catalog:
  path: %s
destinations:%s
sources:
  %s:
    engine: postgresql
    host: 127.0.0.1
    port: %d
    user: postgres
    password: env:KOFFR_PG_PASSWORD
    database: %s
    sslmode: disable
    destinations: [main]
`, recipient, recovery, filepath.Join(dir, "catalog.db"),
		destinations, sourceID, sharedPGPort, sourceDB)

	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// sharedPGPort is the mapped port of the one database every destination reads
// from. Set once, by startPostgres.
var sharedPGPort int

var keysOnce struct {
	sync.Once
	identity, recipient, recovery string
}

func sharedKeys(t *testing.T) (identity, recipient, recovery string) {
	t.Helper()
	keysOnce.Do(func() {
		keysOnce.identity, keysOnce.recipient = testutil.AgeIdentity(t)
		_, keysOnce.recovery = testutil.AgeIdentity(t)
	})
	return keysOnce.identity, keysOnce.recipient, keysOnce.recovery
}

// stagingDir is what criterion 4 watches: a directory of its own, pointed at by
// TMPDIR, holding nothing at the start.
//
// It is set before any t.TempDir() call and kept apart from the repository. The
// first attempt watched the system temporary directory, which on a laptop holds
// Steam cookies and half of npm, and put the repository inside it -- so the
// destination's own object, mid-write, read as a staged copy of itself.
// subdir makes a working directory outside the watched staging area.
func subdir(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	return dir
}

func stagingDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "koffr-staging-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMPDIR", dir)
	return dir
}

// ---------------------------------------------------------------- koffr

func runKoffr(t *testing.T, configPath string, args ...string) string {
	t.Helper()
	var out, errBuf strings.Builder
	full := append([]string{"--config", configPath}, args...)

	code := cli.Run(context.Background(), full, cli.Streams{Out: &out, Err: &errBuf})
	require.Equal(t, cli.ExitOK, code,
		"koffr %s\nstdout: %s\nstderr: %s", strings.Join(args, " "), out.String(), errBuf.String())
	return out.String()
}

func latestBackup(t *testing.T, configPath string) string {
	t.Helper()
	out := runKoffr(t, configPath, "--output", "json", "ls", "--limit", "1")

	var listed struct {
		Result struct {
			Backups []struct {
				ID string `json:"backup_id"`
			} `json:"backups"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &listed))
	require.NotEmpty(t, listed.Result.Backups)
	return listed.Result.Backups[0].ID
}
