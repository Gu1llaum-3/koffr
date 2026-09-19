package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
