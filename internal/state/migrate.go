package state

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migration is one numbered SQL file, embedded in the binary. koffr carries its
// schema: there is no directory to deploy alongside it (E-009).
type migration struct {
	version int
	name    string
	sql     string
}

// migrations are the schema of this binary, in order.
var migrations = loadMigrations()

func loadMigrations() []migration {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		panic("read the embedded migrations: " + err.Error())
	}

	loaded := make([]migration, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()

		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			panic("migration " + name + " does not start with a number")
		}

		body, err := fs.ReadFile(migrationFiles, "migrations/"+name)
		if err != nil {
			panic("read migration " + name + ": " + err.Error())
		}

		loaded = append(loaded, migration{version: version, name: name, sql: string(body)})
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].version < loaded[j].version })

	return loaded
}

// migrate brings the database up to the schema of this binary. Each migration
// runs in its own transaction, and nothing is ever rolled back automatically:
// a schema koffr cannot read is a reason to stop, not to improvise (ADR-0006).
func (s *State) migrate() error {
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
		    version    INTEGER NOT NULL PRIMARY KEY,
		    applied_at TEXT    NOT NULL CHECK (applied_at GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*Z')
		) STRICT`,
	); err != nil {
		return fmt.Errorf("prepare the migration ledger: %w", err)
	}

	var current int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read the schema version: %w", err)
	}

	if latest := len(migrations); current > latest {
		return fmt.Errorf(
			"the state is at schema version %d and this koffr knows %d: it was written by a newer version, and koffr does not downgrade a backup catalogue",
			current, latest,
		)
	}

	for _, step := range migrations {
		if step.version <= current {
			continue
		}
		if err := s.apply(step); err != nil {
			return err
		}
	}

	return nil
}

func (s *State) apply(step migration) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", step.name, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(step.sql); err != nil {
		return fmt.Errorf("apply migration %s: %w", step.name, err)
	}

	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		step.version, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("record migration %s: %w", step.name, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", step.name, err)
	}

	return nil
}
