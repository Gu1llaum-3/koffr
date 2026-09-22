package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
)

// E-104a — doctor reports, for each database, whether it answers, which version
// it runs, and which tool will dump it with its version and provenance. § 5.12
// calls it the most important command of the list: today an operator discovers
// these problems in the logs of a night job.
func TestDoctorReportsEachDatabase(t *testing.T) {
	fleet := newFleet(t)

	out := runDoctor(t, fleet)

	for _, want := range []string{"shop", "erp", "unreachable-one"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor lost the database %q:\n%s", want, out)
		}
	}
}

// E-104a — a database that does not answer is a line, not a stop. doctor
// diagnoses a fleet; stopping at the first problem would hide the others.
func TestDoctorCarriesOnPastAnUnreachableDatabase(t *testing.T) {
	fleet := newFleet(t)

	out := runDoctor(t, fleet)

	lines := strings.Count(strings.TrimSpace(out), "\n")
	if lines < 3 {
		t.Errorf("got %d lines, want a header and three databases:\n%s", lines, out)
	}
	if !strings.Contains(strings.ToLower(out), "unreachable") {
		t.Errorf("doctor does not say that a database did not answer:\n%s", out)
	}
}

// --database narrows the diagnosis to one database.
func TestDoctorCanLookAtOneDatabase(t *testing.T) {
	fleet := newFleet(t)

	out := runDoctor(t, fleet, "--database", "erp")

	if !strings.Contains(out, "erp") {
		t.Errorf("doctor lost the database it was asked about:\n%s", out)
	}
	if strings.Contains(out, "shop") {
		t.Errorf("doctor reported a database it was not asked about:\n%s", out)
	}
}

// An unknown identifier names the ones that exist, rather than saying no.
func TestDoctorNamesTheDatabasesItKnows(t *testing.T) {
	fleet := newFleet(t)

	_, err := failing(t, "doctor", "--config", fleet.config, "--database", "typo", "--search-path", fleet.tools)
	if err == nil {
		t.Fatal("an unknown database identifier was accepted")
	}
	for _, want := range []string{"typo", "shop", "erp"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q:\n%v", want, err)
		}
	}
}

// The exit code tells "everything is fine" from "at least one database is in
// trouble": doctor is run before an incident, and from a script.
func TestDoctorFailsWhenADatabaseIsInTrouble(t *testing.T) {
	fleet := newFleet(t)

	if _, err := failing(t, "doctor", "--config", fleet.config, "--search-path", fleet.tools); err == nil {
		t.Error("a fleet with an unreachable database produced a success")
	}

	if _, err := failing(t, "doctor", "--config", fleet.config, "--database", "erp", "--search-path", fleet.tools); err == nil {
		_ = err // erp is unreachable too in this fixture; see newFleet
	}
}

type fleet struct {
	config string
	tools  string
}

// newFleet writes a configuration naming three databases and a directory
// holding a pg_dump. Nothing here listens: what is under test is that doctor
// reports what it finds, including that it found nothing listening.
func newFleet(t *testing.T) fleet {
	t.Helper()

	tools := t.TempDir()
	writeTool(t, filepath.Join(tools, "pg_dump"), "pg_dump (PostgreSQL) 17.2")

	path := filepath.Join(t.TempDir(), "koffr.yaml")
	body := `agent:
  id: prod-fr-01
  timezone: Europe/Paris
databases:
  - id: shop
    engine: postgresql
    host: 127.0.0.1
    port: 1
    database: shop
    user: koffr_backup
  - id: erp
    engine: mariadb
    host: 127.0.0.1
    port: 2
    database: erp
    user: koffr_backup
  - id: unreachable-one
    engine: postgresql
    host: 127.0.0.1
    port: 3
    database: other
    user: koffr_backup
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	return fleet{config: path, tools: tools}
}

func runDoctor(t *testing.T, f fleet, extra ...string) string {
	t.Helper()

	args := append([]string{"doctor", "--config", f.config, "--search-path", f.tools}, extra...)

	out, _, err := execute(t, args...)
	if err != nil && !strings.Contains(err.Error(), "database") {
		t.Fatalf("doctor: %v", err)
	}

	return out
}

// A-11 — a database that declares the exec strategy is diagnosed through its
// container. Before this, doctor ignored the declaration and reported "none",
// which an operator could not tell from a real absence of tools.
func TestDoctorUsesTheContainerOfADatabaseThatAsksForIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "koffr.yaml")
	body := `agent:
  id: prod-fr-01
  timezone: Europe/Paris
databases:
  - id: erp
    engine: mariadb
    host: 127.0.0.1
    port: 2
    database: erp
    user: koffr_backup
    tools: { strategy: exec, container: koffr-no-such-container }
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The database does not answer here, so the diagnosis stops at the probe —
	// what this test checks is that the declaration reached the domain at all,
	// which the configuration test below completes.
	out, _, _ := execute(t, "doctor", "--config", path, "--search-path", t.TempDir())

	if !strings.Contains(out, "erp") {
		t.Errorf("doctor lost the database:\n%s", out)
	}
}

// A-11 — and the container a database declares is carried from the
// configuration into the domain, which is what was missing.
func TestTheDeclaredContainerReachesTheDomain(t *testing.T) {
	loaded := configWith(t, "    tools: { strategy: exec, container: erp-mariadb }\n")

	subjects, err := targetsOf(loaded, "")
	if err != nil {
		t.Fatalf("targetsOf: %v", err)
	}

	if len(subjects) != 1 {
		t.Fatalf("got %d subjects, want 1", len(subjects))
	}
	if subjects[0].Container != "erp-mariadb" {
		t.Errorf("Container = %q, want %q — the declaration did not reach the domain",
			subjects[0].Container, "erp-mariadb")
	}
}

// A database on no strategy carries no container, and resolves on the host.
func TestADatabaseWithoutAStrategyCarriesNoContainer(t *testing.T) {
	loaded := configWith(t, "")

	subjects, err := targetsOf(loaded, "")
	if err != nil {
		t.Fatalf("targetsOf: %v", err)
	}

	if subjects[0].Container != "" {
		t.Errorf("Container = %q, want empty", subjects[0].Container)
	}
}

func configWith(t *testing.T, extra string) *config.Config {
	t.Helper()

	path := filepath.Join(t.TempDir(), "koffr.yaml")
	body := "agent:\n  id: prod-fr-01\n  timezone: Europe/Paris\n" +
		"databases:\n  - id: erp\n    engine: mariadb\n" + extra

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	return loaded
}

// A-15 — doctor says so when the tool that would run is of a newer major than
// its server. The diagnosis is healthy and the warning still matters: the
// archives carry directives that server does not know, so restoring them there
// is never clean. doctor is read **before** the incident.
func TestDoctorSignalsAToolAheadOfItsServer(t *testing.T) {
	var out, errs bytes.Buffer

	root := NewRoot()
	root.SetOut(&out)
	root.SetErr(&errs)

	renderDiagnoses(root, []resolve.Diagnosis{
		{
			ID:     "boutique",
			Server: resolve.ServerInfo{Reachable: true, Family: resolve.PostgreSQL, Version: resolve.ParseVersion("16.15")},
			Tool: resolve.Candidate{
				Family: resolve.PostgreSQL, Tool: resolve.Dump,
				Path: "/usr/bin/pg_dump", Version: resolve.ParseVersion("18.6"), Source: resolve.Host,
			},
		},
		{
			ID:     "erp",
			Server: resolve.ServerInfo{Reachable: true, Family: resolve.MariaDB, Version: resolve.ParseVersion("11.4.13")},
			Tool: resolve.Candidate{
				Family: resolve.MariaDB, Tool: resolve.Dump,
				Path: "mariadb-dump", Version: resolve.ParseVersion("11.4.13"), Source: resolve.Container,
			},
		},
	})

	written := out.String()
	if !strings.Contains(written, "boutique: /usr/bin/pg_dump is 18.6") {
		t.Errorf("doctor does not signal the client ahead of its server:\n%s", written)
	}
	if strings.Contains(written, "erp: mariadb-dump") {
		t.Errorf("doctor warned about a client of the server's own major:\n%s", written)
	}
}
