package engine_test

import (
	"errors"
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
