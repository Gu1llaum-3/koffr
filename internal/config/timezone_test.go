package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CFG-04 — agent.timezone is required and must be a zone Go can load. Schedules
// are read in it, so a missing or mistyped zone stops koffr rather than
// silently running the night's backups in the wrong hour (E-036, § 5.1 F1.5).
func TestCFG04TimezoneIsRequiredAndValidated(t *testing.T) {
	cases := []struct {
		name     string
		timezone string
		wants    string
	}{
		{name: "missing", timezone: "", wants: "agent.timezone"},
		{name: "mistyped", timezone: "Europe/Pariss", wants: "Europe/Pariss"},
		{name: "empty string", timezone: `""`, wants: "agent.timezone"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := "agent:\n  id: \"prod-fr-01\"\n"
			if c.timezone != "" {
				body += "  timezone: " + c.timezone + "\n"
			}

			_, err := Load(write(t, body))
			if err == nil {
				t.Fatalf("timezone %q was accepted", c.timezone)
			}
			if !strings.Contains(err.Error(), c.wants) {
				t.Errorf("the error does not mention %q:\n%v", c.wants, err)
			}
		})
	}
}

// CFG-04 — the declared zone is what koffr uses, and TZ is never consulted.
func TestCFG04TheSystemTimezoneHasNoEffect(t *testing.T) {
	t.Setenv("TZ", "Asia/Tokyo")

	config := load(t, `
agent:
  id: "prod-fr-01"
  timezone: "Europe/Paris"
`)

	if got := config.Location().String(); got != "Europe/Paris" {
		t.Errorf("Location() = %q, want %q — TZ leaked in", got, "Europe/Paris")
	}
}

// CFG-04 — TZ cannot stand in for a missing declaration either.
func TestCFG04ASetTZDoesNotExcuseAMissingDeclaration(t *testing.T) {
	t.Setenv("TZ", "Europe/Paris")

	_, err := Load(write(t, "agent:\n  id: \"prod-fr-01\"\n"))
	if err == nil {
		t.Fatal("a missing agent.timezone was accepted because TZ was set")
	}
}

// CFG-04 — the zone database is embedded in the binary (N-6). Without it a
// static binary resolves no zone on a host that has no zoneinfo, and E-036
// stops holding. This guards the import rather than the behaviour, which
// cannot be observed on a machine that does have zoneinfo.
func TestCFG04TheZoneDatabaseIsEmbedded(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	for _, source := range sources {
		raw, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("read %s: %v", source, err)
		}
		if strings.Contains(string(raw), `_ "time/tzdata"`) {
			return
		}
	}

	t.Error(`no file of internal/config imports _ "time/tzdata": a static binary would resolve no zone`)
}
