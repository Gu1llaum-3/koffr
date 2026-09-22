package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mariadb"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// The end-to-end tests of this lot run against real servers. A stand-in would
// agree with whatever koffr believes, and what is under test here is precisely
// the part koffr does not control: what pg_dump writes, what age reads.
//
// Docker is asked for once, and its absence is a **loud skip**: the decision to
// turn it into a failure lives in scripts/run-tests.sh, which refuses to start
// when KOFFR_REQUIRE_DOCKER=1 and Docker does not answer. That keeps AR-05
// intact — only internal/config reads the environment.
var whyNoDocker string

const (
	fleetUser     = "koffr_backup"
	fleetPassword = "backup-password"
	fleetDatabase = "shop"
	seededTable   = "invoices"
)

func needsContainers(t *testing.T) {
	t.Helper()

	if whyNoDocker == "" {
		return
	}

	t.Skipf("containers: off — %s\n"+
		"  This test backs up a real server; it proves nothing without one.\n"+
		"  The CI runs it with KOFFR_REQUIRE_DOCKER=1 and fails without.",
		whyNoDocker)
}

func dockerFailure() string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return err.Error()
	}
	if err := provider.Health(ctx); err != nil {
		return err.Error()
	}

	return ""
}

// fleetServer is one database of the fleet, as the configuration would see it.
type fleetServer struct {
	engine    string
	host      string
	port      int
	container string
}

// startPostgresServer starts a PostgreSQL and fills it with enough rows that
// the raw dump is much larger than the archive: the promise of § 4.5 is about
// what a **big** database does, and a table of two rows cannot show it.
func startPostgresServer(t *testing.T, rows int) fleetServer {
	t.Helper()
	needsContainers(t)

	container, err := postgres.Run(t.Context(), "postgres:16",
		postgres.WithDatabase(fleetDatabase),
		postgres.WithUsername(fleetUser),
		postgres.WithPassword(fleetPassword),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres — is Docker running? %v", err)
	}
	testcontainers.CleanupContainer(t, container)

	host, err := container.Host(t.Context())
	if err != nil {
		t.Fatalf("container host: %v", err)
	}

	port, err := container.MappedPort(t.Context(), "5432/tcp")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}

	server := fleetServer{
		engine: "postgresql", host: host, port: int(port.Num()),
		container: nameOf(t, container),
	}

	connection, err := pgx.Connect(t.Context(), postgresURL(server))
	if err != nil {
		t.Fatalf("connect to seed: %v", err)
	}
	defer func() { _ = connection.Close(t.Context()) }()

	for _, statement := range []string{
		"CREATE TABLE " + seededTable + " (id serial primary key, label text)",
		fmt.Sprintf("INSERT INTO %s (label) SELECT repeat('invoice-', 25) FROM generate_series(1, %d)",
			seededTable, rows),
	} {
		if _, err := connection.Exec(t.Context(), statement); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	return server
}

// startMariaDBServer starts a MariaDB. Its archives are dumped by the client of
// its own image: on a machine that carries both families the host cannot hold
// both clients, which is what ADR-0015 is about.
func startMariaDBServer(t *testing.T) fleetServer {
	t.Helper()
	needsContainers(t)

	container, err := mariadb.Run(t.Context(), "mariadb:11.4",
		mariadb.WithDatabase(fleetDatabase),
		mariadb.WithUsername(fleetUser),
		mariadb.WithPassword(fleetPassword),
	)
	if err != nil {
		t.Fatalf("start mariadb: %v", err)
	}
	testcontainers.CleanupContainer(t, container)

	host, err := container.Host(t.Context())
	if err != nil {
		t.Fatalf("container host: %v", err)
	}

	port, err := container.MappedPort(t.Context(), "3306/tcp")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}

	seedMariaDB(t, container)

	return fleetServer{
		engine: "mariadb", host: host, port: int(port.Num()),
		container: nameOf(t, container),
	}
}

func nameOf(t *testing.T, container testcontainers.Container) string {
	t.Helper()

	name, err := container.Name(t.Context())
	if err != nil {
		t.Fatalf("container name: %v", err)
	}

	return strings.TrimPrefix(name, "/")
}

func postgresURL(server fleetServer) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s",
		fleetUser, fleetPassword, server.host, server.port, fleetDatabase)
}

// dumpConnections counts the backends pg_dump has open. pg_dump names itself in
// application_name, so this counts its connections and nothing else.
func dumpConnections(t *testing.T, server fleetServer) int {
	t.Helper()

	connection, err := pgx.Connect(context.Background(), postgresURL(server))
	if err != nil {
		return 0 // the server is on its way down; nothing is dumping
	}
	defer func() { _ = connection.Close(context.Background()) }()

	var live int
	if err := connection.QueryRow(context.Background(),
		"SELECT count(*) FROM pg_stat_activity WHERE application_name = 'pg_dump'").Scan(&live); err != nil {
		return 0
	}

	return live
}

// seedMariaDB fills the database through the client of its own image, rather
// than through a driver: AR-08 keeps database/sql to internal/state and
// internal/engine, tests included, and the image has everything needed.
func seedMariaDB(t *testing.T, container testcontainers.Container) {
	t.Helper()

	statements := strings.Join([]string{
		"CREATE TABLE " + seededTable + " (id int primary key, label varchar(64)) ENGINE=InnoDB;",
		"INSERT INTO " + seededTable + " VALUES (1, 'one'), (2, 'two');",
	}, " ")

	code, reader, err := container.Exec(t.Context(), []string{
		"mariadb", "-u", fleetUser, "-p" + fleetPassword, fleetDatabase, "-e", statements,
	})
	if err != nil {
		t.Fatalf("seed mariadb: %v", err)
	}

	if code != 0 {
		said, _ := io.ReadAll(reader)
		t.Fatalf("seed mariadb exited with %d:\n%s", code, said)
	}
}

func writeFleetFile(t *testing.T, path, body string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
