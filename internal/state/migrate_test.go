package state

import (
	"slices"
	"sort"
	"strings"
	"testing"
)

// E-028 names seven tables. All seven are created at lot 0, even the five that
// stay empty until later lots: the specification describes the whole schema, so
// creating it piecemeal would only multiply migrations (N-9).
var expectedTables = []string{
	"alerts",
	"backup_locations",
	"backups",
	"databases",
	"job_logs",
	"jobs",
	"schedules",
}

func TestTheSevenTablesOfE028AreCreated(t *testing.T) {
	state := open(t)

	got := tables(t, state)
	for _, want := range expectedTables {
		if !slices.Contains(got, want) {
			t.Errorf("table %q is missing; got %v", want, got)
		}
	}
}

// Applying the migrations twice changes nothing: koffr opens its state at every
// start.
func TestMigrationsAreIdempotent(t *testing.T) {
	dir := t.TempDir()

	first, err := Open(dir)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	before := tables(t, first)
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := Open(dir)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	if after := tables(t, second); !slices.Equal(before, after) {
		t.Errorf("the schema moved on a second open:\n before %v\n after  %v", before, after)
	}
	if version := schemaVersion(t, second); version != len(migrations) {
		t.Errorf("schema version = %d, want %d", version, len(migrations))
	}
}

// Each migration records that it ran, so that the next start knows where it is.
func TestEachMigrationRecordsItsVersion(t *testing.T) {
	state := open(t)

	if got := schemaVersion(t, state); got != len(migrations) {
		t.Errorf("schema version = %d, want %d", got, len(migrations))
	}
}

// A state written by a newer koffr is refused rather than opened: a schema this
// binary does not know is a downgrade, and a downgrade on a backup catalogue is
// how a catalogue is lost.
func TestAStateFromANewerVersionRefusesToOpen(t *testing.T) {
	dir := t.TempDir()

	state, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := state.DB().Exec(
		`INSERT INTO schema_migrations (version, applied_at) VALUES (99, '2026-09-18T00:00:00Z')`,
	); err != nil {
		t.Fatalf("forge a newer version: %v", err)
	}
	if err := state.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := Open(dir)
	if err == nil {
		_ = reopened.Close()
		t.Fatal("a state written by a newer koffr was opened")
	}
	if !strings.Contains(err.Error(), "99") {
		t.Errorf("the error does not name the version it found:\n%v", err)
	}
}

func tables(t *testing.T, state *State) []string {
	t.Helper()

	rows, err := state.DB().Query(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`,
	)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	sort.Strings(names)

	return names
}

func schemaVersion(t *testing.T, state *State) int {
	t.Helper()

	var version int
	if err := state.DB().QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read the schema version: %v", err)
	}

	return version
}
