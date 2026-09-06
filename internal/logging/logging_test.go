package logging_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/logging"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

func TestJSON_IsParseableWithTheFieldsAScriptNeeds(t *testing.T) {
	var out bytes.Buffer
	log, closeLog, err := logging.New(logging.Config{Format: "json", Level: "info", Writer: &out})
	require.NoError(t, err)
	defer func() { require.NoError(t, closeLog()) }()

	log.Info("backup completed", "source", "prod", "bytes", 1024)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(out.String())), &got))
	assert.Equal(t, "backup completed", got["msg"])
	assert.Equal(t, "INFO", got["level"])
	assert.Equal(t, "prod", got["source"])
	assert.NotEmpty(t, got["time"], "a log line with no timestamp is a log line nobody can correlate")
}

// Text for a person, JSON for a machine. The choice is the whole point of
// EF-114: progress on a terminal, structured logs otherwise.
func TestText_IsReadableRatherThanParseable(t *testing.T) {
	var out bytes.Buffer
	log, closeLog, err := logging.New(logging.Config{Format: "text", Level: "info", Writer: &out})
	require.NoError(t, err)
	defer func() { require.NoError(t, closeLog()) }()

	log.Info("backup completed", "source", "prod")
	assert.Contains(t, out.String(), "backup completed")
	assert.Contains(t, out.String(), "source=prod")
}

func TestLevel_Filters(t *testing.T) {
	for _, tc := range []struct{ level, wantSeen, wantHidden string }{
		{"debug", "a debug line", ""},
		{"info", "an info line", "a debug line"},
		{"warn", "a warning", "an info line"},
		{"error", "an error", "a warning"},
	} {
		t.Run(tc.level, func(t *testing.T) {
			var out bytes.Buffer
			log, closeLog, err := logging.New(logging.Config{Format: "json", Level: tc.level, Writer: &out})
			require.NoError(t, err)
			defer func() { require.NoError(t, closeLog()) }()

			log.Debug("a debug line")
			log.Info("an info line")
			log.Warn("a warning")
			log.Error("an error")

			assert.Contains(t, out.String(), tc.wantSeen)
			if tc.wantHidden != "" {
				assert.NotContains(t, out.String(), tc.wantHidden)
			}
		})
	}
}

func TestNew_RefusesAnUnknownLevelOrFormat(t *testing.T) {
	_, _, err := logging.New(logging.Config{Level: "chatty"})
	require.Error(t, err)
	_, _, err = logging.New(logging.Config{Format: "xml"})
	require.Error(t, err)
}

// A log file is where an error message lives for months, which makes it the
// worst place for a credential (ENF-021).
func TestNoSecretSurvivesIntoTheLog(t *testing.T) {
	var out bytes.Buffer
	log, closeLog, err := logging.New(logging.Config{Format: "json", Level: "info", Writer: &out})
	require.NoError(t, err)
	defer func() { require.NoError(t, closeLog()) }()

	log.Error("backup failed", "source", "prod", "error", "connection refused")
	testutil.AssertNoSecretLeak(t, out.String())
}

// EF-136's file half. A daemon that fills a disk with its own logs takes the
// backups down with it, which is a spectacular way for a backup tool to fail.
func TestFile_RotatesAndKeepsALimitedHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "koffr.log")

	log, closeLog, err := logging.New(logging.Config{
		Format: "json", Level: "info", Path: path,
		MaxSizeBytes: 2 << 10, MaxFiles: 3,
	})
	require.NoError(t, err)

	for i := range 200 {
		log.Info("a line long enough to fill a small file quickly", "n", i,
			"padding", strings.Repeat("x", 64))
	}
	require.NoError(t, closeLog())

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(entries), 3,
		"a log that never stops growing fills the disk the backups need")
	assert.GreaterOrEqual(t, len(entries), 2, "rotation should have happened at all")

	// The live file keeps its name, so a tail -f survives a rotation and so
	// does anything shipping logs by path.
	current, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotEmpty(t, current)

	var last map[string]any
	lines := strings.Split(strings.TrimSpace(string(current)), "\n")
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &last),
		"a rotation must not cut a line in half")
	assert.Equal(t, float64(199), last["n"], "the newest line is in the live file")
}

func TestFile_AndStreamTogether(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "koffr.log")

	var out bytes.Buffer
	log, closeLog, err := logging.New(logging.Config{
		Format: "json", Level: "info", Path: path, Writer: &out,
	})
	require.NoError(t, err)

	log.Info("backup completed", "source", "prod")
	require.NoError(t, closeLog())

	onDisk, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(onDisk), "backup completed")
	assert.Contains(t, out.String(), "backup completed",
		"a container reads stderr and an operator reads the file; both have to work")
}

// slog is used from several goroutines at once -- the scheduler, the hub, the
// job itself -- so the writer underneath it has to survive that.
func TestFile_ConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "koffr.log")
	log, closeLog, err := logging.New(logging.Config{
		Format: "json", Level: "info", Path: path, MaxSizeBytes: 4 << 10, MaxFiles: 2,
	})
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := range 50 {
				log.Info("concurrent", "worker", i, "n", j, "padding", strings.Repeat("y", 32))
			}
		}(i)
	}
	wg.Wait()
	require.NoError(t, closeLog())

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		var any map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &any), "interleaved write: %q", line)
	}
}

func TestNew_WithNothingConfiguredStillLogs(t *testing.T) {
	log, closeLog, err := logging.New(logging.Config{})
	require.NoError(t, err)
	require.NotNil(t, log)
	assert.IsType(t, &slog.Logger{}, log)
	require.NoError(t, closeLog())
}

// Rotated files are numbered, and the pruning has to read those numbers rather
// than sort the names. Lexically ".1" ".10" ".2" is the order, so past nine
// files kept, sorting by name deletes the second-newest and keeps the oldest --
// the exact opposite of the job.
func TestFile_PrunesTheOldestPastNineFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "koffr.log")

	log, closeLog, err := logging.New(logging.Config{
		Format: "json", Level: "info", Path: path,
		MaxSizeBytes: 512, MaxFiles: 12,
	})
	require.NoError(t, err)

	for i := range 400 {
		log.Info("filling", "n", i, "padding", strings.Repeat("z", 64))
	}
	require.NoError(t, closeLog())

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.LessOrEqual(t, len(entries), 12)

	// .1 is the most recent of the old ones, so it must be there whatever the
	// count reached.
	_, err = os.Stat(path + ".1")
	require.NoError(t, err, "the newest rotated file was pruned")
}
