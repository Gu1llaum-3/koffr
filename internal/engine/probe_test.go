package engine_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
	"github.com/Gu1llaum-3/koffr/internal/engine"
)

// E-104a — the probe says whether a server answers and which version it runs.
// The version is read from the server, never from the configuration: doctor
// shows it, and the whole compatibility matrix depends on it being true.
func TestProbeReadsTheVersionOfAPostgreSQLServer(t *testing.T) {
	server := startPostgres(t, "18")

	got, err := engine.New().Probe(t.Context(), server.target)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	if !got.Reachable {
		t.Error("the server is running and the probe says it is not reachable")
	}
	if got.Family != resolve.PostgreSQL {
		t.Errorf("Family = %q, want %q", got.Family, resolve.PostgreSQL)
	}
	if got.Version.Major != 18 {
		t.Errorf("Version.Major = %d, want 18 (full version %s)", got.Version.Major, got.Version)
	}
}

// E-042 asks for actionable failures, which starts here: an operator must be
// able to tell a wrong host from a wrong password from a wrong database name.
func TestProbeTellsItsFailuresApart(t *testing.T) {
	server := startPostgres(t, "18")

	cases := []struct {
		name   string
		target func(resolve.Target) resolve.Target
		want   error
	}{
		{
			name:   "host that answers nothing",
			target: func(t resolve.Target) resolve.Target { t.Port = 1; return t },
			want:   resolve.ErrUnreachable,
		},
		{
			name:   "wrong password",
			target: func(t resolve.Target) resolve.Target { t.Password = "not-the-password"; return t },
			want:   resolve.ErrDenied,
		},
		{
			name:   "database that is not there",
			target: func(t resolve.Target) resolve.Target { t.Database = "never-created"; return t },
			want:   resolve.ErrNoSuchDatabase,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := engine.New().Probe(t.Context(), c.target(server.target))
			if !errors.Is(err, c.want) {
				t.Errorf("got %v, want %v", err, c.want)
			}
		})
	}
}

// A probe that cannot reach a server is not an error of koffr: doctor reports
// it as a line and carries on with the rest of the fleet.
func TestAnUnreachableServerIsReportedNotHidden(t *testing.T) {
	_, err := engine.New().Probe(t.Context(), resolve.Target{
		Engine: resolve.PostgreSQL, Host: "127.0.0.1", Port: 1, Database: "x", User: "x",
	})

	if !errors.Is(err, resolve.ErrUnreachable) {
		t.Fatalf("got %v, want %v", err, resolve.ErrUnreachable)
	}
}

// E-041 — the family is read from the server, never from the configuration.
// MySQL and MariaDB answer on the same port, speak the same protocol and need
// different dump tools: getting this wrong produces an archive nobody can
// restore, which is the failure the whole § 5.2 exists to prevent.
func TestProbeReadsTheFamilyFromTheServer(t *testing.T) {
	cases := []struct {
		name     string
		start    func(*testing.T) server
		want     resolve.Family
		major    int
		unwanted resolve.Family
	}{
		{
			name:  "MariaDB announces itself",
			start: func(t *testing.T) server { return startMariaDB(t, "11.4") },
			want:  resolve.MariaDB, major: 11, unwanted: resolve.MySQL,
		},
		{
			name:  "MySQL announces itself",
			start: func(t *testing.T) server { return startMySQL(t, "8.4") },
			want:  resolve.MySQL, major: 8, unwanted: resolve.MariaDB,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := c.start(t)

			got, err := engine.New().Probe(t.Context(), server.target)
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}

			if got.Family != c.want {
				t.Errorf("Family = %q, want %q", got.Family, c.want)
			}
			if got.Version.Major != c.major {
				t.Errorf("Version.Major = %d, want %d (full version %s)", got.Version.Major, c.major, got.Version)
			}
		})
	}
}

// E-041 again, from the other side: the configuration does not get a vote. A
// MariaDB declared as MySQL is still reported as MariaDB, so that the resolver
// refuses mysqldump for it rather than trusting a typo.
func TestTheConfigurationDoesNotDecideTheFamily(t *testing.T) {
	server := startMariaDB(t, "11.4")

	lying := server.target
	lying.Engine = resolve.MySQL

	got, err := engine.New().Probe(t.Context(), lying)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	if got.Family != resolve.MariaDB {
		t.Errorf("Family = %q, want %q — the configuration was believed over the server",
			got.Family, resolve.MariaDB)
	}
}

// E-056 — the probe finds the MyISAM tables of the database and names them.
// --single-transaction promises nothing about them: they are dumped outside the
// snapshot, so the manifest and the interface have to say so.
func TestTheProbeNamesTheMyISAMTables(t *testing.T) {
	server := startMariaDB(t, "11.4")
	seedMySQLFamily(t, server.target, true)

	info, err := engine.New().Probe(t.Context(), server.target)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if info.MyISAMUnknown != "" {
		t.Fatalf("the probe could not look: %s", info.MyISAMUnknown)
	}

	if len(info.MyISAMTables) != 1 || info.MyISAMTables[0] != "legacy_ledger" {
		t.Fatalf("MyISAMTables = %v, want [legacy_ledger]", info.MyISAMTables)
	}
	if !strings.Contains(info.MyISAMWarning(), "legacy_ledger") {
		t.Errorf("the warning does not name the table:\n%s", info.MyISAMWarning())
	}
}

// E-056 — and a database that is entirely InnoDB warns about nothing.
func TestTheProbeSaysNothingOfAFullyTransactionalDatabase(t *testing.T) {
	server := startMariaDB(t, "11.4")
	seedMySQLFamily(t, server.target, false)

	info, err := engine.New().Probe(t.Context(), server.target)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	if got := info.MyISAMWarning(); got != "" {
		t.Errorf("an InnoDB-only database warned anyway: %s", got)
	}
}
