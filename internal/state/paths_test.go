package state

import (
	"os"
	"path/filepath"
	"testing"
)

// CFG-08 — opening the state clears tmp/. Whatever is there belongs to a run
// that did not finish, and nothing reads it afterwards (E-026).
func TestCFG08OpeningTheStateClearsTheWorkingSpace(t *testing.T) {
	dir := t.TempDir()

	// A first open creates the layout; leftovers are then planted in it.
	first, err := Open(dir)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	leftovers := []string{
		filepath.Join(dir, TmpDir, "dump-01H.sql"),
		filepath.Join(dir, TmpDir, "staging", "part-0001"),
	}
	for _, path := range leftovers {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("plant a leftover: %v", err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("plant a leftover: %v", err)
		}
	}

	second, err := Open(dir)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	for _, path := range leftovers {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s survived the opening of the state", path)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, TmpDir)); err != nil {
		t.Errorf("tmp/ itself was removed instead of being cleared: %v", err)
	}
}

// CFG-08 — and it clears tmp/ only. A managed tool costs a download and the
// database is the catalogue: neither is working space.
func TestCFG08ClearingTheWorkingSpaceSparesEverythingElse(t *testing.T) {
	dir := t.TempDir()

	first, err := Open(dir)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	tool := filepath.Join(dir, ToolsDir, "postgresql", "16", "bin", "pg_dump")
	if err := os.MkdirAll(filepath.Dir(tool), 0o750); err != nil {
		t.Fatalf("plant a tool: %v", err)
	}
	if err := os.WriteFile(tool, []byte("ELF"), 0o750); err != nil {
		t.Fatalf("plant a tool: %v", err)
	}

	second, err := Open(dir)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	for _, path := range []string{tool, filepath.Join(dir, DatabaseFile)} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s was removed: %v", path, err)
		}
	}
}

// CFG-08 — the layout of E-026 is created if it is not there, so that a first
// start on a bare machine works.
func TestCFG08OpeningCreatesTheLayoutOfE026(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "never", "created")

	state, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })

	for _, sub := range []string{ToolsDir, TmpDir} {
		if info, err := os.Stat(filepath.Join(dir, sub)); err != nil || !info.IsDir() {
			t.Errorf("%s/ was not created: %v", sub, err)
		}
	}
}
