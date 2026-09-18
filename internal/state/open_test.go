package state

import (
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Opening the state applies the settings ADR-0006 fixed. They are not defaults:
// without WAL a reader blocks the writer, without foreign_keys the references
// between jobs, backups and locations are decoration, and without a busy
// timeout a concurrent read fails instead of waiting.
func TestOpenAppliesTheSettingsOfADR0006(t *testing.T) {
	state := open(t)

	settings := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"foreign_keys", "1"},
		{"busy_timeout", "5000"},
		{"synchronous", "1"}, // NORMAL
	}

	for _, setting := range settings {
		var got string
		if err := state.DB().QueryRow("PRAGMA " + setting.pragma).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", setting.pragma, err)
		}
		if got != setting.want {
			t.Errorf("PRAGMA %s = %q, want %q", setting.pragma, got, setting.want)
		}
	}
}

// The settings hold on every connection of the pool, not only the first one:
// SQLite applies most pragmas per connection, so a pool that opens a second one
// would silently lose them.
func TestTheSettingsHoldOnEveryConnectionOfThePool(t *testing.T) {
	state := open(t)

	// Two connections held at once forces the pool to open a second one.
	first, err := state.DB().Conn(t.Context())
	if err != nil {
		t.Fatalf("first connection: %v", err)
	}
	defer func() { _ = first.Close() }()

	second, err := state.DB().Conn(t.Context())
	if err != nil {
		t.Fatalf("second connection: %v", err)
	}
	defer func() { _ = second.Close() }()

	var got string
	if err := second.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&got); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if got != "1" {
		t.Errorf("the second connection has foreign_keys = %q, want %q", got, "1")
	}
}

// E-027 — the driver is the pure Go one, whose name is "sqlite". mattn's, which
// would be "sqlite3", needs CGO and would cost the static binary of E-117.
func TestTheDriverIsThePureGoOne(t *testing.T) {
	drivers := sql.Drivers()

	if !slices.Contains(drivers, "sqlite") {
		t.Errorf("the driver %q is not registered; drivers are %v", "sqlite", drivers)
	}
	if slices.Contains(drivers, "sqlite3") {
		t.Errorf("the driver %q is registered: mattn/go-sqlite3 got in, and CGO with it", "sqlite3")
	}
}

// The database lands where E-026 says, under the state directory it is given.
func TestTheDatabaseFileLandsWhereE026SaysIt(t *testing.T) {
	dir := t.TempDir()

	state, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })

	if _, err := os.Stat(filepath.Join(dir, "koffr.db")); err != nil {
		t.Errorf("koffr.db is not in the state directory: %v", err)
	}
}

func open(t *testing.T) *State {
	t.Helper()

	state, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })

	return state
}
