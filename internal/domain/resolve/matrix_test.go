package resolve_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
)

// RSV-04 — pg_dump backs up a server of its own major or older, never a newer
// one: it refuses and exits, and koffr must know that before running it rather
// than after.
func TestRSV04PostgreSQLDumpNeedsAToolAtLeastAsRecent(t *testing.T) {
	server := resolve.ServerInfo{Family: resolve.PostgreSQL, Version: resolve.ParseVersion("16.10"), Reachable: true}

	cases := []struct {
		tool string
		fits bool
	}{
		{"15.4", false}, // older: pg_dump refuses
		{"16.10", true}, // the same major
		{"16.2", true},  // the same major, older minor — majors are what count
		{"17.2", true},  // newer: supported
		{"18.0", true},
	}

	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			candidate := candidate(resolve.PostgreSQL, resolve.Dump, c.tool, resolve.Host)

			if got := resolve.Fits(candidate, server, resolve.Dump); got != c.fits {
				t.Errorf("Fits(%s, server 16.10) = %v, want %v", c.tool, got, c.fits)
			}
		})
	}
}

// RSV-05 — the archive format moves on: an old pg_restore cannot read a recent
// archive, so restoring is bounded by the archive, not by the server.
func TestRSV05PostgreSQLRestoreNeedsAToolAtLeastAsRecent(t *testing.T) {
	archive := resolve.ServerInfo{Family: resolve.PostgreSQL, Version: resolve.ParseVersion("17.2")}

	if resolve.Fits(candidate(resolve.PostgreSQL, resolve.Restore, "16.10", resolve.Host), archive, resolve.Restore) {
		t.Error("a pg_restore 16 was accepted for an archive written by 17")
	}
	if !resolve.Fits(candidate(resolve.PostgreSQL, resolve.Restore, "17.2", resolve.Host), archive, resolve.Restore) {
		t.Error("a pg_restore 17 was refused for an archive written by 17")
	}
}

// RSV-06 — restoring into an older major is not guaranteed, and the § 5.2 says
// to signal it, not to block it. An operator restoring a production dump into
// an older test instance knows what they are doing; koffr says it out loud and
// gets out of the way.
func TestRSV06ADownwardRestoreIsWarnedAboutNotBlocked(t *testing.T) {
	origin := resolve.ParseVersion("17.2")

	warning := resolve.WarnIfDownward(origin, resolve.ParseVersion("16.10"))
	if warning == "" {
		t.Error("restoring a 17 archive into a 16 target passed without a word")
	}
	for _, want := range []string{"17", "16"} {
		if !strings.Contains(warning, want) {
			t.Errorf("the warning does not name %q: %s", want, warning)
		}
	}

	if got := resolve.WarnIfDownward(origin, resolve.ParseVersion("18.0")); got != "" {
		t.Errorf("restoring into a newer target warned about nothing it should: %s", got)
	}
}

// RSV-07 — the two families are never crossed, even when that leaves koffr with
// nothing. An archive written by the wrong family is an archive nobody can
// restore, which is worse than a backup that did not happen and said so.
func TestRSV07TheMySQLFamiliesAreNeverCrossed(t *testing.T) {
	mariadb := resolve.ServerInfo{Family: resolve.MariaDB, Version: resolve.ParseVersion("11.4.8"), Reachable: true}

	oracle := candidate(resolve.MySQL, resolve.Dump, "8.4.6", resolve.Host)
	if resolve.Fits(oracle, mariadb, resolve.Dump) {
		t.Error("Oracle's mysqldump was accepted for a MariaDB")
	}

	// And the other way round.
	mysql := resolve.ServerInfo{Family: resolve.MySQL, Version: resolve.ParseVersion("8.4.6"), Reachable: true}
	if resolve.Fits(candidate(resolve.MariaDB, resolve.Dump, "11.4.8", resolve.Host), mysql, resolve.Dump) {
		t.Error("mariadb-dump was accepted for a MySQL")
	}

	// Same family, older client: refused too.
	if resolve.Fits(candidate(resolve.MariaDB, resolve.Dump, "10.6.21", resolve.Host), mariadb, resolve.Dump) {
		t.Error("a MariaDB client older than its server was accepted")
	}
	if !resolve.Fits(candidate(resolve.MariaDB, resolve.Dump, "11.4.8", resolve.Host), mariadb, resolve.Dump) {
		t.Error("a MariaDB client of the server's own version was refused")
	}
}

// RSV-08 — the closest compatible version wins. With 16, 17 and 18 available
// for a server in 16, it is 16 that runs: the tool that matches the server is
// the one whose behaviour is known.
func TestRSV08TheClosestCompatibleVersionWins(t *testing.T) {
	server := resolve.ServerInfo{Family: resolve.PostgreSQL, Version: resolve.ParseVersion("16.10"), Reachable: true}

	chosen, err := resolve.Choose([]resolve.Candidate{
		candidate(resolve.PostgreSQL, resolve.Dump, "18.0", resolve.Host),
		candidate(resolve.PostgreSQL, resolve.Dump, "16.10", resolve.Host),
		candidate(resolve.PostgreSQL, resolve.Dump, "17.2", resolve.Host),
	}, server, resolve.Dump)
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}

	if chosen.Version.Major != 16 {
		t.Errorf("chose %s, want 16 — the closest compatible version, not the newest", chosen.Version)
	}
}

// RSV-08 — the trap the § 5.2 names. The host has 14, the server is 16, and a
// managed 16 is installed: choosing the 14 because it came from the host is
// the bug the specification calls out by name.
func TestRSV08ProvenanceOnlyBreaksTies(t *testing.T) {
	server := resolve.ServerInfo{Family: resolve.PostgreSQL, Version: resolve.ParseVersion("16.10"), Reachable: true}

	chosen, err := resolve.Choose([]resolve.Candidate{
		candidate(resolve.PostgreSQL, resolve.Dump, "14.11", resolve.Host),
		candidate(resolve.PostgreSQL, resolve.Dump, "16.10", resolve.Managed),
	}, server, resolve.Dump)
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}

	if chosen.Version.Major != 16 || chosen.Source != resolve.Managed {
		t.Errorf("chose %s from %s, want the managed 16 — the trap of § 5.2", chosen.Version, chosen.Source)
	}

	// At equal versions, and only then, provenance decides: host, then managed,
	// then container.
	tied, err := resolve.Choose([]resolve.Candidate{
		candidate(resolve.PostgreSQL, resolve.Dump, "16.10", resolve.Container),
		candidate(resolve.PostgreSQL, resolve.Dump, "16.10", resolve.Managed),
		candidate(resolve.PostgreSQL, resolve.Dump, "16.10", resolve.Host),
	}, server, resolve.Dump)
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}
	if tied.Source != resolve.Host {
		t.Errorf("at equal versions koffr chose %s, want %s", tied.Source, resolve.Host)
	}
}

// RSV-09 — an impossible resolution says what koffr expected and what it found,
// and never promises a command koffr does not have. The managed install is out
// of the MVP (ADR-0014): installing a client is a prerequisite the README
// carries, not something koffr does.
func TestRSV09AnImpossibleResolutionSaysWhatToDo(t *testing.T) {
	server := resolve.ServerInfo{Family: resolve.PostgreSQL, Version: resolve.ParseVersion("17.2"), Reachable: true}

	_, err := resolve.Choose([]resolve.Candidate{
		candidate(resolve.PostgreSQL, resolve.Dump, "15.4", resolve.Host),
	}, server, resolve.Dump)
	if err == nil {
		t.Fatal("a pg_dump 15 was accepted for a server in 17")
	}
	if !errors.Is(err, resolve.ErrNoCompatibleTool) {
		t.Errorf("got %v, want %v", err, resolve.ErrNoCompatibleTool)
	}

	message := err.Error()
	for _, want := range []string{
		"postgresql", // the family
		"17",         // the version expected
		"15.4",       // what was found
		"host",       // and where it came from
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the message does not give %q:\n%s", want, message)
		}
	}

	// ADR-0014 — koffr has no managed install, so it does not send an operator
	// to a command it does not have.
	if strings.Contains(message, "tools install") {
		t.Errorf("the message promises a command that does not exist:\n%s", message)
	}
}

// And with nothing at all found, the message still says what was expected.
func TestRSV09NothingFoundAlsoSaysWhatToDo(t *testing.T) {
	server := resolve.ServerInfo{Family: resolve.MariaDB, Version: resolve.ParseVersion("11.4.8"), Reachable: true}

	_, err := resolve.Choose(nil, server, resolve.Dump)
	if !errors.Is(err, resolve.ErrNoCompatibleTool) {
		t.Fatalf("got %v, want %v", err, resolve.ErrNoCompatibleTool)
	}
	for _, want := range []string{"mariadb", "11", "none"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message does not give %q:\n%s", want, err.Error())
		}
	}
}

func candidate(family resolve.Family, tool resolve.Tool, version string, source resolve.Source) resolve.Candidate {
	return resolve.Candidate{
		Family:  family,
		Tool:    tool,
		Path:    "/usr/bin/" + string(family) + "-" + version,
		Version: resolve.ParseVersion(version),
		Source:  source,
	}
}
