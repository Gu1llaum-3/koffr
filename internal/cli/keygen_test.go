package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// E-076, ADR-0007 — keygen shows the pair once and writes the private key
// nowhere. That is the whole design: an attacker who takes the machine gets the
// databases as they are now, never the history of what they were.
func TestKeygenShowsThePairAndWritesNothing(t *testing.T) {
	stateDir := t.TempDir()
	logDir := t.TempDir()

	out := run(t, "keygen", "--state-dir", stateDir, "--log-dir", logDir)

	if !strings.Contains(out, "age1") {
		t.Errorf("no public key in the output:\n%s", out)
	}
	if !strings.Contains(out, "AGE-SECRET-KEY-") {
		t.Errorf("no private key in the output:\n%s", out)
	}

	// Nothing of the pair reached the disk.
	for _, dir := range []string{stateDir, logDir} {
		for _, found := range filesUnder(t, dir) {
			contents, err := os.ReadFile(found)
			if err != nil {
				continue
			}
			if strings.Contains(string(contents), "AGE-SECRET-KEY-") {
				t.Errorf("the private key was written to %s", found)
			}
		}
	}
}

// And it says so, because an operator who closes this terminal loses the key.
func TestKeygenSaysThePrivateKeyIsNotKept(t *testing.T) {
	out := run(t, "keygen", "--state-dir", t.TempDir(), "--log-dir", t.TempDir())

	lowered := strings.ToLower(out)
	for _, want := range []string{"not", "store"} {
		if !strings.Contains(lowered, want) {
			t.Errorf("the output does not warn that the private key is not kept (%q):\n%s", want, out)
		}
	}
	if !strings.Contains(lowered, "recipients.txt") {
		t.Errorf("the output does not say where the public key goes:\n%s", out)
	}
}

// Two runs give two different keys: nothing is derived from the machine.
func TestKeygenGivesANewPairEveryTime(t *testing.T) {
	first := run(t, "keygen", "--state-dir", t.TempDir(), "--log-dir", t.TempDir())
	second := run(t, "keygen", "--state-dir", t.TempDir(), "--log-dir", t.TempDir())

	if first == second {
		t.Error("two keygen runs produced the same pair")
	}
}

func filesUnder(t *testing.T, dir string) []string {
	t.Helper()

	var found []string

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			found = append(found, filesUnder(t, path)...)

			continue
		}
		found = append(found, path)
	}

	return found
}
