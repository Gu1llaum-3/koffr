package engine_test

import (
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mariadb"
	"github.com/testcontainers/testcontainers-go/modules/mysql"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
)

// Probes are tested against real servers, never against a stand-in. A probe
// that lies about a version is exactly the defect P3 exists to prevent, and a
// fake driver would agree with whatever the code believes (N-3).
type server struct {
	target resolve.Target
}

// The same credentials everywhere: what is under test is the probe, not the
// imagination of the fixture.
const (
	probeUser     = "koffr_probe"
	probePassword = "probe-password"
	probeDatabase = "probe"
)

func startPostgres(t *testing.T, version string) server {
	t.Helper()
	needsContainers(t)

	container, err := postgres.Run(t.Context(), "postgres:"+version,
		postgres.WithDatabase(probeDatabase),
		postgres.WithUsername(probeUser),
		postgres.WithPassword(probePassword),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres:%s — is Docker running? %v", version, err)
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

	return server{target: resolve.Target{
		Engine:   resolve.PostgreSQL,
		Host:     host,
		Port:     int(port.Num()),
		Database: probeDatabase,
		User:     probeUser,
		Password: probePassword,
	}}
}

func startMariaDB(t *testing.T, version string) server {
	t.Helper()
	needsContainers(t)

	container, err := mariadb.Run(t.Context(), "mariadb:"+version,
		mariadb.WithDatabase(probeDatabase),
		mariadb.WithUsername(probeUser),
		mariadb.WithPassword(probePassword),
	)
	if err != nil {
		t.Fatalf("start mariadb:%s: %v", version, err)
	}
	testcontainers.CleanupContainer(t, container)

	return server{target: mysqlTarget(t, container, resolve.MariaDB)}
}

func startMySQL(t *testing.T, version string) server {
	t.Helper()
	needsContainers(t)

	container, err := mysql.Run(t.Context(), "mysql:"+version,
		mysql.WithDatabase(probeDatabase),
		mysql.WithUsername(probeUser),
		mysql.WithPassword(probePassword),
	)
	if err != nil {
		t.Fatalf("start mysql:%s: %v", version, err)
	}
	testcontainers.CleanupContainer(t, container)

	return server{target: mysqlTarget(t, container, resolve.MySQL)}
}

// mysqlTarget reads the address of a started container. The family passed here
// is what the *configuration* would say; the probe reads the real one.
func mysqlTarget(t *testing.T, container testcontainers.Container, declared resolve.Family) resolve.Target {
	t.Helper()

	host, err := container.Host(t.Context())
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := container.MappedPort(t.Context(), "3306/tcp")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}

	return resolve.Target{
		Engine:   declared,
		Host:     host,
		Port:     int(port.Num()),
		Database: probeDatabase,
		User:     probeUser,
		Password: probePassword,
	}
}
