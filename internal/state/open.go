package state

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	// The pure Go driver, registered as "sqlite". E-027 forbids mattn's, which
	// needs CGO and would cost the static binary of E-117.
	_ "modernc.org/sqlite"
)

// Names of what E-026 puts under the state directory.
const (
	DatabaseFile = "koffr.db"
	ToolsDir     = "tools"
	TmpDir       = "tmp"
)

// State is the local database: the jobs, the catalogue and the schedules.
// A single koffr process writes to it (ADR-0006).
type State struct {
	db  *sql.DB
	dir string
}

// Open prepares the state directory, opens the database, applies the
// migrations and clears the working space of E-026.
func Open(dir string) (*State, error) {
	for _, sub := range []string{"", ToolsDir, TmpDir} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o750); err != nil {
			return nil, fmt.Errorf("prepare the state directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", dsn(filepath.Join(dir, DatabaseFile)))
	if err != nil {
		return nil, fmt.Errorf("open the state database: %w", err)
	}

	state := &State{db: db, dir: dir}

	if err := db.Ping(); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("open the state database: %w", err)
	}

	if err := state.migrate(); err != nil {
		_ = db.Close()

		return nil, err
	}

	// Cleared here rather than on every command: a command run by hand must
	// not destroy the working space of a job that is running (N-10).
	if err := state.clearTmp(); err != nil {
		_ = db.Close()

		return nil, err
	}

	return state, nil
}

// DB gives access to the database. Only this package builds queries on it.
func (s *State) DB() *sql.DB {
	return s.db
}

// Dir is the state directory this state lives in.
func (s *State) Dir() string {
	return s.dir
}

// Close releases the database.
func (s *State) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close the state database: %w", err)
	}

	return nil
}

// clearTmp empties the working space of the jobs. Whatever is there belongs to
// a run that did not finish, and nothing reads it afterwards (E-026).
func (s *State) clearTmp() error {
	tmp := filepath.Join(s.dir, TmpDir)

	entries, err := os.ReadDir(tmp)
	if err != nil {
		return fmt.Errorf("read the working directory: %w", err)
	}

	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(tmp, entry.Name())); err != nil {
			return fmt.Errorf("clear the working directory: %w", err)
		}
	}

	return nil
}

// dsn carries the settings of ADR-0006 as connection parameters rather than as
// statements run after opening: SQLite applies most pragmas per connection, and
// database/sql opens as many as it likes.
func dsn(path string) string {
	pragmas := []string{
		"journal_mode(WAL)",
		"foreign_keys(ON)",
		"busy_timeout(5000)",
		"synchronous(NORMAL)",
	}

	query := url.Values{}
	for _, pragma := range pragmas {
		query.Add("_pragma", pragma)
	}

	return "file:" + path + "?" + query.Encode()
}
