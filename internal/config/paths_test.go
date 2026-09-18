package config

import (
	"path/filepath"
	"testing"
)

// CFG-07 — the layout of E-026 is what koffr uses when nobody says otherwise.
// These are defaults, not constants: development happens on a machine where
// /etc/koffr is not writable (N-11).
func TestCFG07TheDefaultPathsAreTheOnesOfE026(t *testing.T) {
	paths := DefaultPaths()

	for _, c := range []struct{ name, got, want string }{
		{"configuration", paths.ConfigFile, "/etc/koffr/koffr.yaml"},
		{"recipients", paths.RecipientsFile(), "/etc/koffr/recipients.txt"},
		{"state database", paths.DatabaseFile(), "/var/lib/koffr/koffr.db"},
		{"managed tools", paths.ToolsDir(), "/var/lib/koffr/tools"},
		{"working space", paths.TmpDir(), "/var/lib/koffr/tmp"},
		{"log file", paths.LogFile(), "/var/log/koffr/koffr.log"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// CFG-07 — and each of them moves with the directory it hangs from.
func TestCFG07EveryPathFollowsTheDirectoryItIsOverriddenWith(t *testing.T) {
	root := t.TempDir()

	paths := Paths{
		ConfigFile: filepath.Join(root, "etc", "koffr.yaml"),
		StateDir:   filepath.Join(root, "lib"),
		LogDir:     filepath.Join(root, "log"),
	}

	for _, c := range []struct{ name, got, want string }{
		{"recipients", paths.RecipientsFile(), filepath.Join(root, "etc", "recipients.txt")},
		{"state database", paths.DatabaseFile(), filepath.Join(root, "lib", "koffr.db")},
		{"managed tools", paths.ToolsDir(), filepath.Join(root, "lib", "tools")},
		{"working space", paths.TmpDir(), filepath.Join(root, "lib", "tmp")},
		{"log file", paths.LogFile(), filepath.Join(root, "log", "koffr.log")},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}
