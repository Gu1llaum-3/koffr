package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/config"
)

// CFG-07 — the production paths of E-026 are the defaults of the flags, and
// both flags are there to move them (N-11).
func TestCFG07TheFlagsDefaultToTheProductionPaths(t *testing.T) {
	flags := NewRoot().PersistentFlags()

	for _, c := range []struct{ flag, want string }{
		{"config", config.DefaultConfigFile},
		{"state-dir", config.DefaultStateDir},
	} {
		got := flags.Lookup(c.flag)
		if got == nil {
			t.Errorf("--%s does not exist", c.flag)

			continue
		}
		if got.DefValue != c.want {
			t.Errorf("--%s defaults to %q, want %q", c.flag, got.DefValue, c.want)
		}
	}
}

// CFG-08 — a command run by hand does not clear the working space. Clearing it
// happens when the state opens, which is what serve does; a `config validate`
// typed while a backup is running must not destroy that backup's scratch
// files (N-10).
func TestCFG08ACommandRunByHandDoesNotClearTheWorkingSpace(t *testing.T) {
	stateDir := t.TempDir()

	scratch := filepath.Join(stateDir, "tmp", "dump-in-progress.sql")
	if err := os.MkdirAll(filepath.Dir(scratch), 0o750); err != nil {
		t.Fatalf("prepare the working space: %v", err)
	}
	if err := os.WriteFile(scratch, []byte("a job is writing here"), 0o600); err != nil {
		t.Fatalf("prepare the working space: %v", err)
	}

	run(t, "config", "validate", "--config", reference, "--state-dir", stateDir)

	if _, err := os.Stat(scratch); err != nil {
		t.Errorf("a command run by hand cleared the working space: %v", err)
	}
}
