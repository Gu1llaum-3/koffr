package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// N-6 — `koffr tools list` shows every candidate it found, not only the ones
// koffr installed. It is what makes E-038 observable: an operator can see that
// the enumeration really visited the host, and with which versions.
func TestToolsListShowsEveryCandidateWithItsProvenance(t *testing.T) {
	system := t.TempDir()
	managed := t.TempDir()

	writeTool(t, filepath.Join(system, "pg_dump"), "pg_dump (PostgreSQL) 14.11")
	writeTool(t, filepath.Join(managed, "postgresql", "17", "bin", "pg_dump"), "pg_dump (PostgreSQL) 17.2")

	out := run(t, "tools", "list",
		"--tools-dir", managed,
		"--search-path", system,
	)

	for _, want := range []string{"postgresql", "14.11", "17.2", "host", "managed", "pg_dump"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing lost %q:\n%s", want, out)
		}
	}
}

// The output is stable: an operator compares two runs, and a diff that moves on
// its own is a diff nobody reads.
func TestToolsListIsSorted(t *testing.T) {
	system := t.TempDir()
	writeTool(t, filepath.Join(system, "pg_dump"), "pg_dump (PostgreSQL) 16.10")
	writeTool(t, filepath.Join(system, "mariadb-dump"), "mariadb-dump from 11.4.8-MariaDB, client 10.19")

	first := run(t, "tools", "list", "--search-path", system)
	second := run(t, "tools", "list", "--search-path", system)

	if first != second {
		t.Errorf("two runs disagree:\n%s\n---\n%s", first, second)
	}
	if mariadb, postgres := strings.Index(first, "mariadb"), strings.Index(first, "postgresql"); mariadb > postgres {
		t.Errorf("the listing is not ordered by engine:\n%s", first)
	}
}

// A machine with no tool at all says so, rather than printing an empty table
// that looks like a bug.
func TestToolsListSaysWhenItFoundNothing(t *testing.T) {
	out := run(t, "tools", "list",
		"--search-path", filepath.Join(t.TempDir(), "empty"),
		"--tools-dir", filepath.Join(t.TempDir(), "empty"),
	)

	if !strings.Contains(strings.ToLower(out), "no ") {
		t.Errorf("an empty machine produced an empty listing:\n%s", out)
	}
}

func writeTool(t *testing.T, path, answer string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	script := "#!/bin/sh\necho '" + answer + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // a fixture
		t.Fatalf("write %s: %v", path, err)
	}
}

// ADR-0014 — the managed install is out of the MVP. `tools install` and
// `tools remove` do not exist: koffr --help promises only what it holds.
func TestToolsInstallDoesNotExist(t *testing.T) {
	for _, gone := range []string{"install", "remove"} {
		if _, err := failing(t, "tools", gone, "postgresql", "17"); err == nil {
			t.Errorf("koffr tools %s exists, and ADR-0014 says it does not", gone)
		}
	}
}
