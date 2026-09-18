package engine_test

import (
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
)

// Probes are tested against real servers, never against a stand-in. A probe
// that lies about a version is exactly the defect P3 exists to prevent, and a
// fake driver would agree with whatever the code believes (N-3).
type server struct {
	target resolve.Target
}

func startPostgres(t *testing.T, version string) server {
	t.Helper()
	needsContainers(t)

	const (
		user     = "koffr_probe"
		password = "probe-password"
		database = "probe"
	)

	container, err := postgres.Run(t.Context(), "postgres:"+version,
		postgres.WithDatabase(database),
		postgres.WithUsername(user),
		postgres.WithPassword(password),
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
		Database: database,
		User:     user,
		Password: password,
	}}
}
