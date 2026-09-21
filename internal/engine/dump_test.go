package engine_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql" // registers the driver the seeding uses
	"github.com/jackc/pgx/v5"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
	"github.com/Gu1llaum-3/koffr/internal/engine"
)

// E-024, N-3 — the dump comes out on a stream, in the custom format, with
// --no-owner --no-privileges, and what comes out is a real archive: pg_restore
// reads it back. Anything less and koffr writes files it has never checked.
func TestADumpOfPostgreSQLIsAnArchivePgRestoreReads(t *testing.T) {
	server := startPostgres(t, "16")
	seedPostgres(t, server)

	archive := dumpToMemory(t, engine.DumpRequest{
		Target: server.target,
		Tool:   compatibleHostTool(t, server),
	})

	// The custom format announces itself, and nothing else does.
	if !bytes.HasPrefix(archive, []byte("PGDMP")) {
		t.Errorf("the archive does not start like a -Fc dump: %q", head(archive))
	}

	listed := runTool(t, "pg_restore", "--list", writeTemp(t, "dump.pgc", archive))
	if !strings.Contains(listed, seededTable) {
		t.Errorf("pg_restore does not see the table that was seeded:\n%s", listed)
	}
}

// N-3 and E-115 — the options the manifest of § 5.3 shows, and no password on a
// command line every process on the machine can read.
func TestThePostgreSQLDumpCarriesTheOptionsOfTheSpecification(t *testing.T) {
	argv := engine.DumpCommand(engine.DumpRequest{
		Target: resolve.Target{
			Engine: resolve.PostgreSQL, Host: "db.internal", Port: 5432,
			Database: "shop", User: "koffr", Password: "hunter2",
		},
		Tool: resolve.Candidate{Family: resolve.PostgreSQL, Tool: resolve.Dump, Path: "/usr/bin/pg_dump"},
	})

	for _, want := range []string{"--format=custom", "--no-owner", "--no-privileges", "--no-password"} {
		if !carries(argv, want) {
			t.Errorf("the command does not carry %q: %v", want, argv)
		}
	}
	for _, argument := range argv {
		if strings.Contains(argument, "hunter2") {
			t.Errorf("the password is on the command line: %v", argv)
		}
	}
}

// E-056 — MySQL and MariaDB get --single-transaction, and the objects a schema
// needs to come back whole: routines, triggers and events.
func TestTheMySQLFamilyDumpIsTransactionalAndComplete(t *testing.T) {
	argv := engine.DumpCommand(engine.DumpRequest{
		Target: resolve.Target{
			Engine: resolve.MariaDB, Host: "db.internal", Port: 3306,
			Database: "erp", User: "koffr", Password: "hunter2",
		},
		Tool: resolve.Candidate{Family: resolve.MariaDB, Tool: resolve.Dump, Path: "/usr/bin/mariadb-dump"},
	})

	for _, want := range []string{"--single-transaction", "--routines", "--triggers", "--events"} {
		if !carries(argv, want) {
			t.Errorf("the command does not carry %q: %v", want, argv)
		}
	}
	for _, argument := range argv {
		if strings.Contains(argument, "hunter2") {
			t.Errorf("the password is on the command line: %v", argv)
		}
	}
}

// E-056, ADR-0015 — a real MariaDB dumped by the client of its own image. On a
// machine that carries both families this is the only way to reach the right
// client, so it is the way the test runs it too.
func TestAMariaDBIsDumpedByTheClientOfItsOwnContainer(t *testing.T) {
	server := startMariaDB(t, "11.4")
	seedMySQLFamily(t, server.target, false)

	archive := dumpToMemory(t, engine.DumpRequest{
		Target:    server.target,
		Tool:      containerTool(t, server.name, resolve.MariaDB),
		Container: server.name,
	})

	if !strings.Contains(string(archive), seededTable) {
		t.Errorf("the dump does not contain the table that was seeded:\n%s", head(archive))
	}
}

// ADR-0015 — the exec strategy applies to the dump itself, not only to the
// resolution of the tool. The archive comes out of the container as a stream.
func TestAPostgreSQLIsDumpedInsideItsOwnContainer(t *testing.T) {
	server := startPostgres(t, "16")
	seedPostgres(t, server)

	archive := dumpToMemory(t, engine.DumpRequest{
		Target:    server.target,
		Tool:      containerTool(t, server.name, resolve.PostgreSQL),
		Container: server.name,
	})

	if !bytes.HasPrefix(archive, []byte("PGDMP")) {
		t.Errorf("the archive does not start like a -Fc dump: %q", head(archive))
	}
}

// E-054 — the directory format is refused through the exec strategy, and the
// refusal says so. pg_dump would fill a directory inside the container, which
// koffr has no way of reading back as one archive; an operator who configured
// it gets a sentence rather than a surprise.
func TestTheDirectoryFormatIsRefusedInTheExecStrategy(t *testing.T) {
	_, err := engine.New().Dump(t.Context(), engine.DumpRequest{
		Target:    resolve.Target{Engine: resolve.PostgreSQL, Host: "h", Port: 5432, Database: "shop", User: "u"},
		Tool:      resolve.Candidate{Family: resolve.PostgreSQL, Tool: resolve.Dump, Path: "pg_dump"},
		Format:    engine.Directory,
		Container: "erp-postgres",
	})
	if err == nil {
		t.Fatal("the directory format was accepted inside a container")
	}
	if !errors.Is(err, engine.ErrDirectoryInExec) {
		t.Errorf("got %v, want %v", err, engine.ErrDirectoryInExec)
	}
	for _, want := range []string{"directory", "erp-postgres", "custom"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}
}

// E-054, from the other side: outside a container the directory format is not a
// stream either. It is written to a staging directory and packed afterwards,
// which is why the specification makes it impose the stage mode.
func TestTheDirectoryFormatSaysItNeedsStaging(t *testing.T) {
	_, err := engine.New().Dump(t.Context(), engine.DumpRequest{
		Target: resolve.Target{Engine: resolve.PostgreSQL, Host: "h", Port: 5432, Database: "shop", User: "u"},
		Tool:   resolve.Candidate{Family: resolve.PostgreSQL, Tool: resolve.Dump, Path: "pg_dump"},
		Format: engine.Directory,
	})
	if !errors.Is(err, engine.ErrDirectoryNeedsStaging) {
		t.Fatalf("got %v, want %v", err, engine.ErrDirectoryNeedsStaging)
	}
	if !strings.Contains(err.Error(), "stage") {
		t.Errorf("the refusal does not name the mode it requires:\n%v", err)
	}
}

// BKP-09 — a dump whose sub-process failed is a failure, not an archive. An
// archive of nothing is shaped exactly like an archive, and only the exit code
// tells them apart.
func TestADumpThatFailsIsAFailure(t *testing.T) {
	server := startPostgres(t, "16")

	wrong := server.target
	wrong.Password = "not-the-password"

	reader, err := engine.New().Dump(t.Context(), engine.DumpRequest{
		Target: wrong,
		Tool:   compatibleHostTool(t, server),
	})
	if err != nil {
		return // refused before starting, which is also correct
	}

	_, _ = io.Copy(io.Discard, reader)

	err = reader.Close()
	if err == nil {
		t.Fatal("a dump that could not authenticate reported success")
	}
	if !strings.Contains(err.Error(), "password") && !strings.Contains(err.Error(), "authentication") {
		t.Errorf("the failure does not carry what the tool said:\n%v", err)
	}
}

// E-055 — closing the dump waits for the sub-process, so the connection to the
// database is gone before anything is sent anywhere. The time a dump holds a
// production server must never depend on a destination's bandwidth.
//
// The backend is reaped by PostgreSQL when it notices the closed socket, which
// is prompt but not synchronous with our Close: the test waits for it, with a
// bound, rather than assuming either way.
func TestClosingTheDumpEndsTheConnection(t *testing.T) {
	server := startPostgres(t, "16")
	seedPostgres(t, server)

	reader, err := engine.New().Dump(t.Context(), engine.DumpRequest{
		Target: server.target,
		Tool:   compatibleHostTool(t, server),
	})
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	if _, err := io.Copy(io.Discard, reader); err != nil {
		t.Fatalf("read the dump: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close the dump: %v", err)
	}

	waitForNoDumpConnection(t, server)
}

// E-055 again, on the path that matters most: a dump abandoned half-read is
// stopped, not left running while koffr does something else. A sub-process
// still holding a transaction on a production server is the failure mode the
// stage mode exists to avoid.
func TestClosingAHalfReadDumpStopsIt(t *testing.T) {
	server := startPostgres(t, "16")
	seedPostgres(t, server)

	reader, err := engine.New().Dump(t.Context(), engine.DumpRequest{
		Target: server.target,
		Tool:   compatibleHostTool(t, server),
	})
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	if _, err := io.CopyN(io.Discard, reader, 8); err != nil {
		t.Fatalf("read the start of the dump: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close the dump: %v", err)
	}

	waitForNoDumpConnection(t, server)
}

// ---- helpers -------------------------------------------------------------

const seededTable = "invoices"

func carries(argv []string, want string) bool {
	for _, argument := range argv {
		if argument == want || strings.HasPrefix(argument, want+"=") {
			return true
		}
	}

	return false
}

func dumpToMemory(t *testing.T, request engine.DumpRequest) []byte {
	t.Helper()

	reader, err := engine.New().Dump(t.Context(), request)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	var out bytes.Buffer
	if _, err := io.Copy(&out, reader); err != nil {
		t.Fatalf("read the dump: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("the dump failed: %v", err)
	}

	return out.Bytes()
}

// compatibleHostTool picks the tool of this machine that the resolver would
// pick for this server, and skips loudly when there is none: a dump test
// without a client proves nothing, and pretending otherwise is worse.
func compatibleHostTool(t *testing.T, server server) resolve.Candidate {
	t.Helper()

	info, err := engine.New().Probe(t.Context(), server.target)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	found, err := engine.NewFinder(engine.FinderOptions{}).Find(t.Context(), info.Family, resolve.Dump)
	if err != nil {
		t.Fatalf("find a %s dump tool: %v", info.Family, err)
	}

	chosen, err := resolve.Choose(found, info, resolve.Dump)
	if err != nil {
		t.Skipf("no %s dump tool compatible with %s on this machine: %v\n"+
			"  This test dumps a real server; it proves nothing without the real client.",
			info.Family, info.Version, err)
	}

	return chosen
}

// containerTool resolves the tool inside the container of the database — what
// the exec strategy does for real.
func containerTool(t *testing.T, container string, family resolve.Family) resolve.Candidate {
	t.Helper()

	found, err := engine.NewContainerFinder(engine.ContainerOptions{}).
		FindIn(t.Context(), container, family, resolve.Dump)
	if err != nil {
		t.Fatalf("FindIn %s: %v", container, err)
	}
	if len(found) == 0 {
		t.Fatalf("no %s dump tool inside %s, which ships its own client", family, container)
	}

	return found[0]
}

func seedPostgres(t *testing.T, server server) {
	t.Helper()

	connection, err := pgx.Connect(t.Context(), fmt.Sprintf("postgres://%s:%s@%s:%d/%s",
		server.target.User, server.target.Password, server.target.Host, server.target.Port, server.target.Database))
	if err != nil {
		t.Fatalf("connect to seed: %v", err)
	}
	defer func() { _ = connection.Close(t.Context()) }()

	for _, statement := range []string{
		"CREATE TABLE " + seededTable + " (id serial primary key, label text)",
		"INSERT INTO " + seededTable + " (label) VALUES ('one'), ('two')",
	} {
		if _, err := connection.Exec(t.Context(), statement); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

// seedMySQLFamily creates one InnoDB table and, when asked, one MyISAM table:
// the mixture E-056 has to notice.
func seedMySQLFamily(t *testing.T, target resolve.Target, withMyISAM bool) {
	t.Helper()

	database, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?multiStatements=true",
		target.User, target.Password, target.Host, target.Port, target.Database))
	if err != nil {
		t.Fatalf("open to seed: %v", err)
	}
	defer func() { _ = database.Close() }()

	statements := []string{
		"CREATE TABLE " + seededTable + " (id int primary key, label varchar(32)) ENGINE=InnoDB",
		"INSERT INTO " + seededTable + " VALUES (1, 'one'), (2, 'two')",
	}
	if withMyISAM {
		statements = append(statements,
			"CREATE TABLE legacy_ledger (id int primary key) ENGINE=MyISAM")
	}

	for _, statement := range statements {
		if _, err := database.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

// waitForNoDumpConnection watches the server until the dump's backend is gone.
func waitForNoDumpConnection(t *testing.T, server server) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for {
		live := dumpConnections(t, server)
		if live == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d dump connections are still open ten seconds after Close", live)
		}

		time.Sleep(100 * time.Millisecond)
	}
}

// dumpConnections counts the backends pg_dump opened. pg_dump names itself in
// application_name, so the count is of its connections and of nothing else.
func dumpConnections(t *testing.T, server server) int {
	t.Helper()

	connection, err := pgx.Connect(t.Context(), fmt.Sprintf("postgres://%s:%s@%s:%d/%s",
		server.target.User, server.target.Password, server.target.Host, server.target.Port, server.target.Database))
	if err != nil {
		t.Fatalf("connect to count: %v", err)
	}
	defer func() { _ = connection.Close(t.Context()) }()

	var live int
	if err := connection.QueryRow(t.Context(),
		"SELECT count(*) FROM pg_stat_activity WHERE application_name = 'pg_dump'").Scan(&live); err != nil {
		t.Fatalf("count: %v", err)
	}

	return live
}

func runTool(t *testing.T, name string, args ...string) string {
	t.Helper()

	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is not installed: this check needs the real client", name)
	}

	out, err := exec.CommandContext(context.WithoutCancel(t.Context()), name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}

	return string(out)
}

func writeTemp(t *testing.T, name string, contents []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	return path
}

func head(of []byte) string {
	return string(of[:min(len(of), 400)])
}
