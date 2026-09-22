package backup_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/domain/backup"
)

// BKP-17 — the staging directory is purged when a job starts. A file left by a
// job that was killed is removed: `internal/state` knows how to purge this
// directory (E-026), but `backup` never opens the state, so nothing was ever
// purging it. On a machine where jobs are interrupted — shutdown, OOM, the
// SIGTERM of F3.9 — the disk filled without bound (A-16).
func TestBKP17AJobPurgesWhatAKilledJobLeftBehind(t *testing.T) {
	world := newWorld(t)
	orphan := plantStagingFile(t, world.stateDir, 2147483646) // a pid the kernel has not handed out

	if _, err := world.service().Run(t.Context(), request()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("the staging file of a dead job is still there: %s", orphan)
	}
}

// BKP-17 — but a file belonging to a **living** process is left alone. The lock
// is per database, so two jobs on two databases run at once, and a purge that
// swept the whole directory would delete the other one's buffer underneath it.
func TestBKP17ALivingJobsStagingFileIsNotTouched(t *testing.T) {
	world := newWorld(t)
	mine := plantStagingFile(t, world.stateDir, os.Getpid())

	if _, err := world.service().Run(t.Context(), request()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Stat(mine); err != nil {
		t.Errorf("the purge removed the staging file of a running process: %v", err)
	}
}

// BKP-18 — a job that fails in the middle of staging leaves **nothing**. The
// buffer is removed whether the job succeeded or not; only a killed process can
// leave one, and BKP-17 is what collects it.
func TestBKP18AFailedJobLeavesNoBuffer(t *testing.T) {
	world := newWorld(t)
	world.packer.fail = errors.New("the disk went away mid-stream")

	if _, err := world.service().Run(t.Context(), request()); err == nil {
		t.Fatal("a job whose packing failed reported success")
	}

	left := stagingFiles(t, world.stateDir)
	if len(left) != 0 {
		t.Errorf("a failed job left %d buffer(s) behind: %v", len(left), left)
	}
}

// BKP-18 — and a job that succeeds leaves nothing either.
func TestBKP18ASuccessfulJobLeavesNoBuffer(t *testing.T) {
	world := newWorld(t)

	if _, err := world.service().Run(t.Context(), request()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if left := stagingFiles(t, world.stateDir); len(left) != 0 {
		t.Errorf("a successful job left %d buffer(s) behind: %v", len(left), left)
	}
}

// plantStagingFile writes a buffer as a job of that pid would have left it.
func plantStagingFile(t *testing.T, stateDirectory string, pid int) string {
	t.Helper()

	directory := filepath.Join(stateDirectory, "tmp")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("make the staging directory: %v", err)
	}

	path := filepath.Join(directory, backup.StagingFileName(pid, "planted"))
	if err := os.WriteFile(path, []byte("half an archive"), 0o600); err != nil {
		t.Fatalf("plant a staging file: %v", err)
	}

	return path
}

func stagingFiles(t *testing.T, stateDirectory string) []string {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(stateDirectory, "tmp"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		t.Fatalf("read the staging directory: %v", err)
	}

	var left []string
	for _, entry := range entries {
		left = append(left, entry.Name())
	}

	return left
}

// The staging file carries the pid of the job that owns it, which is what makes
// the purge safe under concurrency (`N-4`).
func TestTheStagingFileNamesTheProcessThatOwnsIt(t *testing.T) {
	name := backup.StagingFileName(4242, "01JQ8F3K2M7X9P4W")

	if !strings.Contains(name, strconv.Itoa(4242)) {
		t.Errorf("the name does not carry the pid: %s", name)
	}
	if !strings.Contains(name, "01JQ8F3K2M7X9P4W") {
		t.Errorf("the name does not carry the job: %s", name)
	}

	owner, ok := backup.OwnerOfStagingFile(name)
	if !ok || owner != 4242 {
		t.Errorf("OwnerOfStagingFile(%q) = %d, %v — want 4242, true", name, owner, ok)
	}

	// A file nobody named this way is not ours, and is left alone.
	if _, ok := backup.OwnerOfStagingFile("something-else.tmp"); ok {
		t.Error("a file koffr did not write was claimed as a staging file")
	}
}
