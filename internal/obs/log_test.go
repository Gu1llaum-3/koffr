package obs

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// E-121 — the logs are JSON on standard output, one object per line, carrying
// time, level and message. journald reads that without being configured, which
// is the point: koffr ships as a systemd unit and nobody edits a log pipeline
// to read it.
func TestLogsAreOneJSONObjectPerLine(t *testing.T) {
	var out bytes.Buffer

	logger, closeLogger := newTestLogger(t, &out, Options{})
	defer closeLogger()

	logger.Info("backup started", "database", "shop", "job", "01HZY")
	logger.Warn("destination slow", "destination", "s3-ovh", "waited_ms", 4200)

	lines := nonEmptyLines(out.String())
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), out.String())
	}

	for _, line := range lines {
		if !strings.HasPrefix(line, "{") {
			t.Errorf("a line does not start with the JSON object; journald would not parse it:\n%s", line)
		}

		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line is not JSON: %v\n%s", err, line)
		}
		for _, key := range []string{"time", "level", "msg"} {
			if _, ok := entry[key]; !ok {
				t.Errorf("the entry has no %q:\n%s", key, line)
			}
		}
	}
}

// The attributes a caller passes are the attributes that come out, with their
// types kept.
func TestAttributesArriveWithTheirTypes(t *testing.T) {
	var out bytes.Buffer

	logger, closeLogger := newTestLogger(t, &out, Options{})
	defer closeLogger()

	logger.Info("backup finished", "database", "shop", "size_bytes", 41231, "verified", true)

	entry := firstEntry(t, out.String())

	if got := entry["database"]; got != "shop" {
		t.Errorf("database = %v, want %q", got, "shop")
	}
	if got, ok := entry["size_bytes"].(float64); !ok || int(got) != 41231 {
		t.Errorf("size_bytes = %v, want 41231", entry["size_bytes"])
	}
	if got := entry["verified"]; got != true {
		t.Errorf("verified = %v, want true", got)
	}
}

// E-119 — nothing here needs a service. The logger writes where it is told and
// asks nobody.
func TestTheLoggerNeedsNothingButAWriter(t *testing.T) {
	var out bytes.Buffer

	logger, closeLogger := New(&out, Options{})
	if logger == nil {
		t.Fatal("New returned no logger")
	}
	if err := closeLogger(); err != nil {
		t.Errorf("closing: %v", err)
	}
}

func newTestLogger(t *testing.T, out *bytes.Buffer, options Options) (*Logger, func()) {
	t.Helper()

	logger, closeLogger := New(out, options)

	return logger, func() {
		if err := closeLogger(); err != nil {
			t.Errorf("closing the logger: %v", err)
		}
	}
}

func nonEmptyLines(raw string) []string {
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}

	return lines
}

func firstEntry(t *testing.T, raw string) map[string]any {
	t.Helper()

	lines := nonEmptyLines(raw)
	if len(lines) == 0 {
		t.Fatal("nothing was logged")
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("line is not JSON: %v\n%s", err, lines[0])
	}

	return entry
}

// Levels named for the tests, so that a reader does not have to remember the
// numeric values of slog.
const (
	levelDebug = -4
	levelWarn  = 4
)
