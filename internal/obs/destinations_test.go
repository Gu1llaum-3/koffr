package obs

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR-0012 — the file gets everything, the console gets what its reader
// expects. An event below the console threshold is written down and not shown.
func TestAnEventBelowTheConsoleLevelReachesTheFileOnly(t *testing.T) {
	var console bytes.Buffer
	path := filepath.Join(t.TempDir(), "koffr.log")

	logger, closeLogger := newTestLogger(t, &console, Options{
		File:         path,
		ConsoleLevel: levelWarn,
	})
	logger.Info("command started", "command", "version")
	closeLogger()

	if console.Len() != 0 {
		t.Errorf("an info line reached the console of a command:\n%s", console.String())
	}
	if !strings.Contains(readFile(t, path), "command started") {
		t.Error("the file lost the line the console did not show")
	}
}

// ADR-0012 — and a warning reaches both: a command that is about to do
// something surprising says so where a human is looking.
func TestAWarningReachesBothDestinations(t *testing.T) {
	var console bytes.Buffer
	path := filepath.Join(t.TempDir(), "koffr.log")

	logger, closeLogger := newTestLogger(t, &console, Options{
		File:         path,
		ConsoleLevel: levelWarn,
	})
	logger.Warn("destination unreachable", "destination", "s3-ovh")
	closeLogger()

	for name, written := range map[string]string{
		"console": console.String(),
		"file":    readFile(t, path),
	} {
		if !strings.Contains(written, "destination unreachable") {
			t.Errorf("the %s lost the warning:\n%s", name, written)
		}
	}
}

// ADR-0012 — serve wants everything on its console, and says so by asking for
// it. Nothing is silently different for a daemon.
func TestAConsoleAskedForEverythingGetsEverything(t *testing.T) {
	var console bytes.Buffer

	logger, closeLogger := newTestLogger(t, &console, Options{ConsoleLevel: levelDebug, Level: levelDebug})
	logger.Debug("probing", "database", "shop")
	closeLogger()

	if !strings.Contains(console.String(), "probing") {
		t.Errorf("a console asked for debug did not get it:\n%s", console.String())
	}
}

// E-115 — masking lives in the handler, so it applies to both destinations and
// cannot be lost by adding a third one.
func TestMaskingAppliesToEveryDestination(t *testing.T) {
	const secret = "hunter2-do-not-log-me"

	var console bytes.Buffer
	path := filepath.Join(t.TempDir(), "koffr.log")

	logger, closeLogger := newTestLogger(t, &console, Options{File: path, ConsoleLevel: levelDebug})
	logger.Warn("connecting", "password", secret)
	closeLogger()

	for name, written := range map[string]string{
		"console": console.String(),
		"file":    readFile(t, path),
	} {
		if strings.Contains(written, secret) {
			t.Errorf("the %s carries the secret:\n%s", name, written)
		}
	}
}

// The file level and the console level are independent: raising one does not
// silence the other.
func TestTheFileLevelAndTheConsoleLevelAreIndependent(t *testing.T) {
	var console bytes.Buffer
	path := filepath.Join(t.TempDir(), "koffr.log")

	logger, closeLogger := newTestLogger(t, &console, Options{
		File:         path,
		Level:        levelWarn,  // the file keeps warnings and worse
		ConsoleLevel: levelDebug, // the console shows everything
	})
	logger.Info("command started")
	closeLogger()

	if !strings.Contains(console.String(), "command started") {
		t.Errorf("the console lost a line it asked for:\n%s", console.String())
	}
	if written := readFile(t, path); strings.Contains(written, "command started") {
		t.Errorf("the file kept a line below its own level:\n%s", written)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read %s: %v", path, err)
	}

	return string(raw)
}
