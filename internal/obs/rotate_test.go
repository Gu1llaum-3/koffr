package obs

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// E-121 — the same lines go to a file, and koffr rotates it itself. E-119 says
// no third-party service is required, and logrotate is one: a koffr that fills
// a disk because nobody installed logrotate has broken its own promise (N-5).
func TestTheLogFileIsWrittenAlongsideStandardOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "koffr.log")

	var out bytes.Buffer

	logger, closeLogger := newTestLogger(t, &out, Options{File: path})
	logger.Info("backup started", "database", "shop")
	closeLogger()

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the log file: %v", err)
	}

	for _, want := range []string{"backup started", "shop"} {
		if !strings.Contains(string(written), want) {
			t.Errorf("the file lost %q:\n%s", want, written)
		}
	}
	if !strings.Contains(out.String(), "backup started") {
		t.Errorf("standard output lost the line:\n%s", out.String())
	}
}

// The file rotates at the size it was given, and only the configured number of
// archives is kept.
func TestTheLogFileRotatesAndKeepsTheConfiguredNumberOfArchives(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "koffr.log")

	logger, closeLogger := newTestLogger(t, &bytes.Buffer{}, Options{
		File:       path,
		MaxSizeMB:  1,
		MaxBackups: 2,
	})

	// Roughly four megabytes of lines: enough to rotate three times.
	filler := strings.Repeat("x", 4096)
	for range 1024 {
		logger.Info("filling the log", "payload", filler)
	}
	closeLogger()

	current, archives := countLogs(t, dir)
	if current != 1 {
		t.Errorf("got %d current log files, want 1", current)
	}
	if archives == 0 {
		t.Fatalf("nothing rotated in %s", dir)
	}

	// Dropping the oldest archives happens in a goroutine of its own, after the
	// write that triggered the rotation: the cap is a promise kept shortly
	// after, not at the instant of the write.
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, archives = countLogs(t, dir)
		if archives <= 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %d archives after waiting, want at most the 2 that were configured", archives)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// countLogs returns how many current log files and how many archives sit in dir.
func countLogs(t *testing.T, dir string) (current, archives int) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the log directory: %v", err)
	}

	for _, entry := range entries {
		switch {
		case entry.Name() == "koffr.log":
			current++
		case strings.HasPrefix(entry.Name(), "koffr"):
			archives++
		}
	}

	return current, archives
}

// The directory of the log file is created if it is not there: a first start on
// a bare machine must not fail on a missing /var/log/koffr.
func TestTheLogDirectoryIsCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "never", "created", "koffr.log")

	logger, closeLogger := newTestLogger(t, &bytes.Buffer{}, Options{File: path})
	logger.Info("first start")
	closeLogger()

	if _, err := os.Stat(path); err != nil {
		t.Errorf("the log file was not created: %v", err)
	}
}
