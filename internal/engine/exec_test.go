package engine_test

import (
	"strings"
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
	"github.com/Gu1llaum-3/koffr/internal/engine"
)

// RSV-10 — the exec strategy finds the tool **inside the container of the
// database** and reads its version by running it **there**. A MariaDB image
// ships the client that matches its own server, which is exactly the tool
// § 5.2 wants when the host has none.
func TestRSV10TheContainerToolIsFoundAndRunThere(t *testing.T) {
	container := startMariaDBNamed(t, "11.4")

	found, err := engine.NewContainerFinder(engine.ContainerOptions{}).
		FindIn(t.Context(), container, resolve.MariaDB, resolve.Dump)
	if err != nil {
		t.Fatalf("FindIn: %v", err)
	}

	if len(found) == 0 {
		t.Fatal("no tool found inside a MariaDB container, which ships its own client")
	}
	if found[0].Source != resolve.Container {
		t.Errorf("source = %q, want %q", found[0].Source, resolve.Container)
	}
	if found[0].Family != resolve.MariaDB {
		t.Errorf("family = %q, want %q", found[0].Family, resolve.MariaDB)
	}
	if found[0].Version.Major != 11 {
		t.Errorf("version = %s, want 11.x — read from the tool in the container", found[0].Version)
	}
}

// RSV-10 — without a Docker socket the failure names the socket it wanted.
// Falling back on a host tool would be the silent substitution E-046 refuses:
// the operator asked for the container's tool for a reason.
func TestRSV10WithoutADockerSocketTheFailureNamesIt(t *testing.T) {
	finder := engine.NewContainerFinder(engine.ContainerOptions{
		Socket: "/var/run/there-is-no-docker-here.sock",
	})

	_, err := finder.FindIn(t.Context(), "erp-mariadb", resolve.MariaDB, resolve.Dump)
	if err == nil {
		t.Fatal("a missing Docker socket was not reported")
	}
	for _, want := range []string{"there-is-no-docker-here.sock", "erp-mariadb"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q:\n%v", want, err)
		}
	}
}

// A container that is not there is named too, rather than being reported as an
// absence of tools.
func TestRSV10AMissingContainerIsNamed(t *testing.T) {
	needsContainers(t)

	_, err := engine.NewContainerFinder(engine.ContainerOptions{}).
		FindIn(t.Context(), "koffr-no-such-container", resolve.MariaDB, resolve.Dump)
	if err == nil {
		t.Fatal("a container that does not exist produced no error")
	}
	if !strings.Contains(err.Error(), "koffr-no-such-container") {
		t.Errorf("the error does not name the container:\n%v", err)
	}
}
