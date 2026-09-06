package testutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// RequireDockerEnv is the variable CI sets so that a missing container runtime
// fails the build instead of quietly skipping the integration tests.
//
// A skipped test reads as a pass in a summary. For the storage and source
// backends, skipping everywhere would mean shipping a repository whose S3
// support was never once exercised.
//
// The name says Docker because that was the first thing it gated. It means
// something broader now: this environment is expected to have everything, so
// nothing may quietly skip. SkipOrFailWithoutTool reads it too.
const RequireDockerEnv = "KOFFR_REQUIRE_DOCKER"

// EnsureDockerHost points testcontainers at whichever daemon the docker CLI is
// already talking to.
//
// testcontainers looks for a socket at the conventional paths, which is wrong
// under Colima, Rancher Desktop, Podman and any non-default Docker context. The
// CLI already knows the answer, so asking it removes a whole class of "works on
// my machine" and keeps the endpoint out of the repository, where it would be
// one developer's home directory.
//
// It returns a reason to skip, or "" if a daemon was found.
func EnsureDockerHost() string {
	if os.Getenv("DOCKER_HOST") != "" {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "docker", "context", "inspect",
		"--format", "{{.Endpoints.docker.Host}}").Output()
	if err != nil {
		return fmt.Sprintf("no docker context available: %v", err)
	}
	host := strings.TrimSpace(string(out))
	if host == "" {
		return "docker context reported no endpoint"
	}

	if err := os.Setenv("DOCKER_HOST", host); err != nil {
		return fmt.Sprintf("set DOCKER_HOST: %v", err)
	}
	// Inside the VM the socket is at the conventional path whatever it is
	// outside. Ryuk, the reaper testcontainers starts, mounts it from there.
	if os.Getenv("TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE") == "" {
		if err := os.Setenv("TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE", "/var/run/docker.sock"); err != nil {
			return fmt.Sprintf("set TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE: %v", err)
		}
	}
	return ""
}

// SkipOrFailWithoutDocker decides what a missing container runtime means.
//
// Locally it is a skip: not having Docker running is a normal state for a
// laptop. Under CI, where RequireDockerEnv is set, it is a failure, because a
// silently skipped integration suite is indistinguishable from a passing one.
//
// The argument is what EnsureDockerHost returned: empty when a runtime is
// there, and otherwise the reason it is not. Passing a description of why the
// test needs Docker would skip every run, so the call reads:
//
//	unavailable := testutil.EnsureDockerHost()
//	skip, fatal := testutil.SkipOrFailWithoutDocker(unavailable)
func SkipOrFailWithoutDocker(unavailable string) (skip bool, fatal string) {
	if unavailable == "" {
		return false, ""
	}
	if os.Getenv(RequireDockerEnv) != "" {
		return false, fmt.Sprintf(
			"%s is set, so a container runtime is required: %s", RequireDockerEnv, unavailable)
	}
	return true, ""
}

// SkipOrFailWithoutTool applies the same rule to a client binary that
// SkipOrFailWithoutDocker applies to a container runtime.
//
// A laptop without the PostgreSQL client tools is a normal state. A CI run
// without them is a suite that silently tested nothing, which is the failure
// mode this whole file exists to prevent.
func SkipOrFailWithoutTool(tb testing.TB, name, why string) {
	tb.Helper()
	if _, err := exec.LookPath(name); err == nil {
		return
	}
	if os.Getenv(RequireDockerEnv) != "" {
		tb.Fatalf("%s is set, so %s must be installed: %s", RequireDockerEnv, name, why)
	}
	tb.Skipf("%s is not on PATH: %s", name, why)
}
