package engine_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// whyNoDocker is empty when containers can be started, and says why not
// otherwise.
var whyNoDocker string

// TestMain asks once whether Docker is reachable, so that each test does not
// pay for the question.
//
// Probes are tested against real servers (N-3). On a machine without Docker the
// tests below cannot run — but staying silent would leave a suite that is green
// and proves nothing, so they are skipped **loudly**.
//
// Turning that skip into a failure where it matters is not decided here:
// scripts/run-tests.sh refuses to start when KOFFR_REQUIRE_DOCKER=1 and Docker
// does not answer. That keeps AR-05 intact — only internal/config reads the
// environment — and puts the decision where the race detector's already is.
func TestMain(m *testing.M) {
	whyNoDocker = dockerFailure()

	os.Exit(m.Run())
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

// needsContainers skips a test that cannot run without Docker, saying so.
func needsContainers(t *testing.T) {
	t.Helper()

	if whyNoDocker == "" {
		return
	}

	t.Skipf("containers: off — %s\n"+
		"  This test probes a real server; it proves nothing without one.\n"+
		"  The CI runs it with KOFFR_REQUIRE_DOCKER=1 and fails without.\n"+
		"  On a machine whose Docker context is not the default socket (Colima, Podman),\n"+
		"  export DOCKER_HOST, and TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE for the reaper.",
		whyNoDocker)
}
