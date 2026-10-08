package resolve_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
)

// A-11, N-3 — a fleet mixes databases that resolve their tool differently: some
// on the host, some inside their own container. E-046 makes that a per-database
// decision, so a diagnosis has to honour it database by database, in one pass.
func TestEachDatabaseResolvesWhereItSaysTo(t *testing.T) {
	diagnoses := resolve.Diagnose(t.Context(),
		fakeProbe{servers: map[string]resolve.ServerInfo{
			"host-one":      {Reachable: true, Family: resolve.PostgreSQL, Version: resolve.ParseVersion("16.10")},
			"container-one": {Reachable: true, Family: resolve.MariaDB, Version: resolve.ParseVersion("11.4.8")},
		}},
		fakeFinder{candidates: []resolve.Candidate{
			{Family: resolve.PostgreSQL, Tool: resolve.Dump, Version: resolve.ParseVersion("16.10"), Source: resolve.Host, Path: "/usr/bin/pg_dump"},
		}},
		fakeContainers{byName: map[string][]resolve.Candidate{
			"erp-mariadb": {{Family: resolve.MariaDB, Tool: resolve.Dump, Version: resolve.ParseVersion("11.4.8"), Source: resolve.Container, Path: "mariadb-dump"}},
		}},
		[]resolve.Subject{
			{ID: "shop", Target: resolve.Target{Engine: resolve.PostgreSQL, Host: "host-one"}},
			{ID: "erp", Target: resolve.Target{Engine: resolve.MariaDB, Host: "container-one"}, Container: "erp-mariadb"},
		},
	)

	if len(diagnoses) != 2 {
		t.Fatalf("got %d diagnoses, want 2", len(diagnoses))
	}

	shop, erp := diagnoses[0], diagnoses[1]

	if shop.Tool.Source != resolve.Host {
		t.Errorf("shop resolved from %q, want %q", shop.Tool.Source, resolve.Host)
	}
	if erp.Tool.Source != resolve.Container {
		t.Errorf("erp resolved from %q, want %q — it declared a container", erp.Tool.Source, resolve.Container)
	}
	if erp.NoTool != nil {
		t.Errorf("erp found no tool although its container has one: %v", erp.NoTool)
	}
}

// N-4 — a container that does not answer is a failure that names it. Falling
// back on a host tool would be the silent substitution E-046 refuses: the
// operator asked for the container's tool for a reason.
func TestAContainerThatDoesNotAnswerIsNamedAndNotWorkedAround(t *testing.T) {
	// The host has a perfectly good MariaDB client. It must not be used.
	diagnoses := resolve.Diagnose(t.Context(),
		fakeProbe{servers: map[string]resolve.ServerInfo{
			"erp-host": {Reachable: true, Family: resolve.MariaDB, Version: resolve.ParseVersion("11.4.8")},
		}},
		fakeFinder{candidates: []resolve.Candidate{
			{Family: resolve.MariaDB, Tool: resolve.Dump, Version: resolve.ParseVersion("11.8.6"), Source: resolve.Host, Path: "/usr/bin/mariadb-dump"},
		}},
		fakeContainers{failure: errors.New("the container erp-mariadb is not running")},
		[]resolve.Subject{
			{ID: "erp", Target: resolve.Target{Engine: resolve.MariaDB, Host: "erp-host"}, Container: "erp-mariadb"},
		},
	)

	erp := diagnoses[0]

	if erp.NoTool == nil {
		t.Fatalf("a dead container was worked around, and koffr chose %s", erp.Tool)
	}
	if !strings.Contains(erp.NoTool.Error(), "erp-mariadb") {
		t.Errorf("the failure does not name the container:\n%v", erp.NoTool)
	}
	if erp.Tool.Source == resolve.Host {
		t.Error("koffr fell back on a host tool for a database that asked for its container")
	}
}

// A database that declares no container is resolved on the host, as before.
func TestADatabaseWithoutAContainerStillResolvesOnTheHost(t *testing.T) {
	diagnoses := resolve.Diagnose(t.Context(),
		fakeProbe{servers: map[string]resolve.ServerInfo{
			"shop-host": {Reachable: true, Family: resolve.PostgreSQL, Version: resolve.ParseVersion("16.10")},
		}},
		fakeFinder{candidates: []resolve.Candidate{
			{Family: resolve.PostgreSQL, Tool: resolve.Dump, Version: resolve.ParseVersion("17.2"), Source: resolve.Host, Path: "/usr/bin/pg_dump"},
		}},
		fakeContainers{failure: errors.New("no Docker here")},
		[]resolve.Subject{{ID: "shop", Target: resolve.Target{Engine: resolve.PostgreSQL, Host: "shop-host"}}},
	)

	if diagnoses[0].NoTool != nil {
		t.Fatalf("a host database was refused a host tool: %v", diagnoses[0].NoTool)
	}
	if diagnoses[0].Tool.Source != resolve.Host {
		t.Errorf("source = %q, want %q", diagnoses[0].Tool.Source, resolve.Host)
	}
}

// fakeFinder answers with a fixed list, whatever it is asked.
type fakeFinder struct {
	candidates []resolve.Candidate
	failure    error
}

// Find answers with the candidates of the kind it is asked for. The kind
// matters since `RSV-14`: a machine can hold `pg_dump` and not `pg_restore`.
func (f fakeFinder) Find(_ context.Context, _ resolve.Family, kind resolve.Tool) ([]resolve.Candidate, error) {
	if f.failure != nil {
		return nil, f.failure
	}

	var of []resolve.Candidate

	for _, candidate := range f.candidates {
		if candidate.Tool == kind {
			of = append(of, candidate)
		}
	}

	return of, nil
}

// fakeContainers answers per container name.
type fakeContainers struct {
	byName  map[string][]resolve.Candidate
	failure error
}

func (f fakeContainers) FindIn(_ context.Context, name string, _ resolve.Family, _ resolve.Tool) ([]resolve.Candidate, error) {
	if f.failure != nil {
		return nil, f.failure
	}

	return f.byName[name], nil
}

var (
	_ resolve.ToolFinder          = fakeFinder{}
	_ resolve.ContainerToolFinder = fakeContainers{}
)

// RSV-14 — a PostgreSQL database whose machine has **no `pg_restore`** cannot be
// backed up, and the diagnosis says so **before** the night job finds out.
//
// `A-26`: since ADR-0017 the structure is checked while the dump streams past,
// by `pg_restore --list`. Without it `P4` refuses to call the result a backup —
// and `koffr doctor` showed an entirely green line on a machine where every
// PostgreSQL backup was going to fail. The command that exists to say "this
// will work" has to see a guaranteed failure.
func TestRSV14ADatabaseThatCannotBeVerifiedIsReported(t *testing.T) {
	dump := resolve.Candidate{
		Family: resolve.PostgreSQL, Tool: resolve.Dump,
		Version: resolve.ParseVersion("18.6"), Source: resolve.Host, Path: "/usr/bin/pg_dump",
	}
	restore := resolve.Candidate{
		Family: resolve.PostgreSQL, Tool: resolve.Restore,
		Version: resolve.ParseVersion("18.6"), Source: resolve.Host, Path: "/usr/bin/pg_restore",
	}

	probe := fakeProbe{servers: map[string]resolve.ServerInfo{
		"pg":    {Reachable: true, Family: resolve.PostgreSQL, Version: resolve.ParseVersion("18.6")},
		"maria": {Reachable: true, Family: resolve.MariaDB, Version: resolve.ParseVersion("11.8.3")},
	}}
	subjects := []resolve.Subject{
		{ID: "boutique", Target: resolve.Target{Engine: resolve.PostgreSQL, Host: "pg"}},
		{ID: "erp", Target: resolve.Target{Engine: resolve.MariaDB, Host: "maria"}},
	}

	t.Run("without pg_restore", func(t *testing.T) {
		diagnoses := resolve.Diagnose(t.Context(), probe,
			fakeFinder{candidates: []resolve.Candidate{dump, mariaDump()}}, fakeContainers{}, subjects)

		boutique, erp := diagnoses[0], diagnoses[1]

		if boutique.NotVerifiable == nil {
			t.Fatal("a PostgreSQL database with no pg_restore is reported as fine")
		}
		if !strings.Contains(boutique.NotVerifiable.Error(), "pg_restore") {
			t.Errorf("the reason does not name what is missing: %v", boutique.NotVerifiable)
		}
		if !strings.Contains(boutique.NotVerifiable.Error(), "18") {
			t.Errorf("the reason does not say which major to install: %v", boutique.NotVerifiable)
		}
		if boutique.Healthy() {
			t.Error("a database that cannot be backed up is reported healthy")
		}

		// MySQL and MariaDB look for their end marker in the dump itself: they
		// need no second tool, and must not be dragged down by this.
		if erp.NotVerifiable != nil {
			t.Errorf("a MariaDB database was asked for a pg_restore: %v", erp.NotVerifiable)
		}
		if !erp.Healthy() {
			t.Errorf("a MariaDB database with its tool is reported in trouble: %+v", erp)
		}
	})

	t.Run("with pg_restore", func(t *testing.T) {
		diagnoses := resolve.Diagnose(t.Context(), probe,
			fakeFinder{candidates: []resolve.Candidate{dump, restore, mariaDump()}}, fakeContainers{}, subjects)

		if diagnoses[0].NotVerifiable != nil {
			t.Errorf("pg_restore is there and the diagnosis says otherwise: %v", diagnoses[0].NotVerifiable)
		}
		if !diagnoses[0].Healthy() {
			t.Errorf("a database with both its tools is reported in trouble: %+v", diagnoses[0])
		}
	})
}

func mariaDump() resolve.Candidate {
	return resolve.Candidate{
		Family: resolve.MariaDB, Tool: resolve.Dump,
		Version: resolve.ParseVersion("11.8.3"), Source: resolve.Host, Path: "/usr/bin/mariadb-dump",
	}
}
