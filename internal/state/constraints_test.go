package state

import (
	"strings"
	"testing"
)

const utcNow = "2026-09-18T02:00:03Z"

// ADR-0006 — a status is spelled out and constrained by CHECK. A value the
// schema does not know is refused at insert time, so that a typo in a query
// cannot leave a job in a state nothing will ever read.
func TestAnUnknownStatusIsRefused(t *testing.T) {
	state := open(t)
	seedDatabase(t, state)

	cases := []struct {
		name  string
		stmt  string
		args  []any
		wants string
	}{
		{
			name:  "job status",
			stmt:  `INSERT INTO jobs (id, kind, database_id, status, created_at, updated_at) VALUES (?, 'backup', 'shop', 'finished', ?, ?)`,
			args:  []any{"job-1", utcNow, utcNow},
			wants: "status",
		},
		{
			name:  "job kind",
			stmt:  `INSERT INTO jobs (id, kind, database_id, status, created_at, updated_at) VALUES (?, 'archive', 'shop', 'running', ?, ?)`,
			args:  []any{"job-2", utcNow, utcNow},
			wants: "kind",
		},
		{
			name:  "engine",
			stmt:  `INSERT INTO databases (id, engine, host, port, "database", "user", fingerprint, resolved, created_at, updated_at) VALUES ('erp', 'oracle', 'h', 1, 'd', 'u', 'f', '{}', ?, ?)`,
			args:  []any{utcNow, utcNow},
			wants: "engine",
		},
		{
			name:  "alert event",
			stmt:  `INSERT INTO alerts (id, event, state, emitted_at, created_at, updated_at) VALUES ('a-1', 'disk_full', 'firing', ?, ?, ?)`,
			args:  []any{utcNow, utcNow, utcNow},
			wants: "event",
		},
		{
			name:  "backup verification state",
			stmt:  `INSERT INTO backups (id, database_id, started_at, verified, created_at, updated_at) VALUES ('b-1', 'shop', ?, 'probably', ?, ?)`,
			args:  []any{utcNow, utcNow, utcNow},
			wants: "verified",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := state.DB().Exec(c.stmt, c.args...)
			if err == nil {
				t.Fatal("an unknown value was accepted")
			}
			if !strings.Contains(err.Error(), c.wants) {
				t.Errorf("the error does not name the column %q:\n%v", c.wants, err)
			}
		})
	}
}

// ADR-0006 — timestamps are RFC 3339 in UTC. One representation in the
// database; the conversion to agent.timezone happens on display and in the
// manifest. A local offset is refused, which is what makes two runs either side
// of a daylight-saving change comparable.
func TestATimestampThatIsNotRFC3339UTCIsRefused(t *testing.T) {
	state := open(t)

	refused := []string{
		"2026-09-18T02:00:03+02:00", // the local offset the manifest shows
		"2026-09-18 02:00:03",       // the SQLite datetime() shape
		"1789213203",                // a Unix timestamp
		"2026-09-18T02:00:03",       // no zone at all
	}

	for _, value := range refused {
		t.Run(value, func(t *testing.T) {
			_, err := state.DB().Exec(
				`INSERT INTO databases (id, engine, host, port, "database", "user", fingerprint, resolved, created_at, updated_at)
				 VALUES ('x', 'postgresql', 'h', 1, 'd', 'u', 'f', '{}', ?, ?)`,
				value, value,
			)
			if err == nil {
				t.Fatalf("%q was accepted as a timestamp", value)
			}
		})
	}

	// And the shape the convention asks for is accepted, with or without a
	// fraction of a second.
	for _, value := range []string{utcNow, "2026-09-18T02:00:03.482Z"} {
		if _, err := state.DB().Exec(
			`INSERT INTO databases (id, engine, host, port, "database", "user", fingerprint, resolved, created_at, updated_at)
			 VALUES (?, 'postgresql', 'h', 1, 'd', 'u', 'f', '{}', ?, ?)`,
			value, value, value,
		); err != nil {
			t.Errorf("%q was refused: %v", value, err)
		}
	}
}

// ADR-0006 — sizes are bytes and durations are milliseconds, both integers.
// The tables are STRICT, so SQLite refuses what would otherwise be stored as
// text and read back as nonsense.
func TestASizeOrADurationThatIsNotAnIntegerIsRefused(t *testing.T) {
	state := open(t)
	seedDatabase(t, state)

	refused := []struct {
		name  string
		stmt  string
		value any
	}{
		{
			name:  "a size written as text",
			stmt:  `INSERT INTO backups (id, database_id, started_at, size_bytes, created_at, updated_at) VALUES ('b-text', 'shop', '` + utcNow + `', ?, '` + utcNow + `', '` + utcNow + `')`,
			value: "12 MB",
		},
		{
			name:  "a size written as a real",
			stmt:  `INSERT INTO backups (id, database_id, started_at, size_bytes, created_at, updated_at) VALUES ('b-real', 'shop', '` + utcNow + `', ?, '` + utcNow + `', '` + utcNow + `')`,
			value: 12.5,
		},
		{
			name:  "a duration written as text",
			stmt:  `INSERT INTO jobs (id, kind, database_id, status, duration_ms, created_at, updated_at) VALUES ('j-text', 'backup', 'shop', 'succeeded', ?, '` + utcNow + `', '` + utcNow + `')`,
			value: "3s",
		},
	}

	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			if _, err := state.DB().Exec(c.stmt, c.value); err == nil {
				t.Fatalf("%v was accepted", c.value)
			}
		})
	}
}

// foreign_keys=ON is not decoration: a job that names a database nobody
// declared is refused.
func TestAJobCannotNameADatabaseThatIsNotThere(t *testing.T) {
	state := open(t)

	_, err := state.DB().Exec(
		`INSERT INTO jobs (id, kind, database_id, status, created_at, updated_at)
		 VALUES ('orphan', 'backup', 'never-declared', 'pending', ?, ?)`,
		utcNow, utcNow,
	)
	if err == nil {
		t.Fatal("a job pointing at an unknown database was accepted")
	}
}

func seedDatabase(t *testing.T, state *State) {
	t.Helper()

	if _, err := state.DB().Exec(
		`INSERT INTO databases (id, engine, host, port, "database", "user", fingerprint, resolved, created_at, updated_at)
		 VALUES ('shop', 'postgresql', '10.0.3.12', 5432, 'shop', 'koffr_backup', 'f', '{}', ?, ?)`,
		utcNow, utcNow,
	); err != nil {
		t.Fatalf("seed a database: %v", err)
	}
}
