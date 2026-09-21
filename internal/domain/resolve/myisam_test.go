package resolve_test

import (
	"strings"
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
)

// RSV-11 — MyISAM tables are named, because --single-transaction says nothing
// about them: they are dumped outside the snapshot, so the archive can be
// inconsistent and E-056 wants that carried to the manifest and the interface.
func TestRSV11MyISAMTablesAreNamedInTheWarning(t *testing.T) {
	info := resolve.ServerInfo{
		Reachable: true, Family: resolve.MariaDB, Version: resolve.ParseVersion("11.4.8"),
		MyISAMTables: []string{"legacy_ledger", "sessions"},
	}

	warning := info.MyISAMWarning()
	if warning == "" {
		t.Fatal("a database holding MyISAM tables warned about nothing")
	}
	for _, want := range []string{"legacy_ledger", "sessions", "single-transaction"} {
		if !strings.Contains(warning, want) {
			t.Errorf("the warning does not say %q:\n%s", want, warning)
		}
	}
}

// RSV-11 — a database that is entirely transactional says nothing. A warning
// that is always there is a warning nobody reads.
func TestRSV11AFullyTransactionalDatabaseWarnsAboutNothing(t *testing.T) {
	info := resolve.ServerInfo{Reachable: true, Family: resolve.MariaDB, Version: resolve.ParseVersion("11.4.8")}

	if got := info.MyISAMWarning(); got != "" {
		t.Errorf("a database with no MyISAM table warned anyway: %s", got)
	}
}

// RSV-11 — and silence about a question koffr failed to ask is not an answer.
// An account that cannot read the catalogue yields "koffr could not tell", not
// "there are none".
func TestRSV11NotBeingAbleToTellIsSaid(t *testing.T) {
	info := resolve.ServerInfo{
		Reachable: true, Family: resolve.MySQL, Version: resolve.ParseVersion("8.4.6"),
		MyISAMUnknown: "SELECT command denied to user 'koffr'",
	}

	warning := info.MyISAMWarning()
	if warning == "" {
		t.Fatal("koffr could not check for MyISAM tables and said nothing")
	}
	if !strings.Contains(warning, "denied") {
		t.Errorf("the warning does not carry why koffr could not tell:\n%s", warning)
	}
}
