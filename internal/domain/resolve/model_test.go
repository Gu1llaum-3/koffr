package resolve_test

import (
	"strings"
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
)

// The compatibility matrix reasons in major versions, so reading one out of
// whatever a server or a tool announces has to be right — and what they
// announce is not tidy.
func TestParseVersionReadsWhatServersAndToolsActuallySay(t *testing.T) {
	cases := []struct {
		raw                 string
		major, minor, patch int
	}{
		{"18.0", 18, 0, 0},
		{"16.10 (Debian 16.10-1.pgdg13+1)", 16, 10, 0},
		{"11.4.8-MariaDB-ubu2404", 11, 4, 8},
		{"8.4.6", 8, 4, 6},
		{"pg_dump (PostgreSQL) 15.4", 15, 4, 0},
		{"12", 12, 0, 0},
	}

	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			got := resolve.ParseVersion(c.raw)

			if got.Major != c.major || got.Minor != c.minor || got.Patch != c.patch {
				t.Errorf("got %d.%d.%d, want %d.%d.%d", got.Major, got.Minor, got.Patch, c.major, c.minor, c.patch)
			}
			if got.Raw != strings.TrimSpace(c.raw) {
				t.Errorf("Raw = %q, want what was announced", got.Raw)
			}
		})
	}
}

// A version nobody could read is zero, not a guess. The resolver must be able
// to say "I could not read this" rather than invent a major.
func TestAnUnreadableVersionIsZero(t *testing.T) {
	if got := resolve.ParseVersion("unknown"); !got.IsZero() {
		t.Errorf("got %+v, want the zero version", got)
	}
}

// E-115 — a target ends up in error messages and log lines. Its password does
// not travel with it.
func TestATargetNeverPrintsItsPassword(t *testing.T) {
	const secret = "hunter2-do-not-print-me"

	target := resolve.Target{
		Engine: resolve.PostgreSQL, Host: "10.0.3.12", Port: 5432,
		Database: "shop", User: "koffr_backup", Password: secret,
	}

	if rendered := target.String(); strings.Contains(rendered, secret) {
		t.Errorf("Target prints its password: %s", rendered)
	}
	if rendered := target.String(); !strings.Contains(rendered, "10.0.3.12") {
		t.Errorf("Target lost the topology that makes it useful: %s", rendered)
	}
}
