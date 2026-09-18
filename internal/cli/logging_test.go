package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// E-121, seen from a command: koffr writes structured logs, and something in
// koffr actually writes them. Until this was wired, internal/obs was code only
// its own tests had ever run (A-07).
func TestACommandWritesAJSONLogLine(t *testing.T) {
	dir := t.TempDir()

	run(t, "version", "--log-dir", dir)

	entries := logLines(t, filepath.Join(dir, "koffr.log"))
	if len(entries) == 0 {
		t.Fatal("no log line was written")
	}

	for _, key := range []string{"time", "level", "msg"} {
		if _, ok := entries[0][key]; !ok {
			t.Errorf("the entry has no %q: %v", key, entries[0])
		}
	}
	if got := entries[0]["command"]; got != "version" {
		t.Errorf("command = %v, want %q", got, "version")
	}
}

// --log-level changes what is written, so that a silent agent can be made
// talkative without rebuilding it.
func TestTheLogLevelIsSettable(t *testing.T) {
	quiet := t.TempDir()
	run(t, "version", "--log-dir", quiet, "--log-level", "error")

	if lines := logLines(t, filepath.Join(quiet, "koffr.log")); len(lines) != 0 {
		t.Errorf("at level error, an info line was still written: %v", lines)
	}

	loud := t.TempDir()
	run(t, "version", "--log-dir", loud, "--log-level", "debug")

	if lines := logLines(t, filepath.Join(loud, "koffr.log")); len(lines) == 0 {
		t.Error("at level debug, nothing was written")
	}
}

// An unknown level is refused rather than quietly ignored: a typo must not
// silence an agent.
func TestAnUnknownLogLevelIsRefused(t *testing.T) {
	if _, err := failing(t, "version", "--log-level", "chatty"); err == nil {
		t.Fatal("an unknown log level was accepted")
	}
}

// N-3 — a log directory koffr cannot write to does NOT stop the command.
// `koffr version` run by an unprivileged user must not fail on /var/log/koffr.
func TestAnUnwritableLogDirectoryDoesNotStopTheCommand(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatalf("make the directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	out := run(t, "version", "--log-dir", filepath.Join(parent, "refused"))

	if !strings.Contains(out, "koffr") {
		t.Errorf("the command lost its output because of the log file:\n%s", out)
	}
}

// And no secret reaches the file, which is where one would actually end up.
func TestTheLogFileCarriesNoSecret(t *testing.T) {
	const secret = "hunter2-do-not-log-me"

	dir := t.TempDir()
	config := writeConfig(t, "    password: "+secret+"\n")

	run(t, "config", "validate", "--config", config, "--log-dir", dir)

	written, err := os.ReadFile(filepath.Join(dir, "koffr.log"))
	if err != nil {
		t.Fatalf("read the log: %v", err)
	}
	if strings.Contains(string(written), secret) {
		t.Errorf("the log carries the password:\n%s", written)
	}
}

func logLines(t *testing.T, path string) []map[string]any {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read %s: %v", path, err)
	}

	var entries []map[string]any
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("a log line is not JSON: %v\n%s", err, line)
		}
		entries = append(entries, entry)
	}

	return entries
}
