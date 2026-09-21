package backup_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/domain/backup"
)

// BKP-01 — one job per database at any instant. A second request is **refused**,
// naming the job that holds the lock, and never queued: a queue would let a
// nightly schedule pile up jobs on a server that is already struggling
// (E-051, § 5.3 F3.1).
func TestBKP01ASecondJobOnTheSameDatabaseIsRefused(t *testing.T) {
	dir := t.TempDir()

	held, err := backup.Acquire(dir, "boutique-prod", "01JQ8F3K2M7X9P4W", time.Now())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer func() { _ = held.Release() }()

	_, err = backup.Acquire(dir, "boutique-prod", "01JQ8F3K2M7X9P4X", time.Now())
	if err == nil {
		t.Fatal("a second job on the same database was allowed")
	}
	if !errors.Is(err, backup.ErrLocked) {
		t.Errorf("got %v, want %v", err, backup.ErrLocked)
	}
	for _, want := range []string{"boutique-prod", "01JQ8F3K2M7X9P4W"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q:\n%v", want, err)
		}
	}
}

// BKP-01 — another database is not affected. The lock is per database, not per
// agent: F3.2 caps what runs at once, and that is a different question.
func TestBKP01AnotherDatabaseIsNotBlocked(t *testing.T) {
	dir := t.TempDir()

	first, err := backup.Acquire(dir, "boutique-prod", "job-1", time.Now())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer func() { _ = first.Release() }()

	second, err := backup.Acquire(dir, "erp-prod", "job-2", time.Now())
	if err != nil {
		t.Fatalf("a second database was blocked by the first: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// BKP-01 — releasing frees the database for the next job.
func TestBKP01ReleasingLetsTheNextJobIn(t *testing.T) {
	dir := t.TempDir()

	first, err := backup.Acquire(dir, "boutique-prod", "job-1", time.Now())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	second, err := backup.Acquire(dir, "boutique-prod", "job-2", time.Now())
	if err != nil {
		t.Fatalf("the lock was not freed: %v", err)
	}
	_ = second.Release()
}

// BKP-01 — a lock left behind by a process that is gone **does not block
// forever**. A machine that loses power at 3 a.m. must back up again the next
// night without anyone logging in to delete a file.
func TestBKP01ALockWhoseProcessIsGoneIsTakenOver(t *testing.T) {
	dir := t.TempDir()

	// A lock file written by a process that no longer exists: the highest pid
	// the kernel will not have handed out.
	writeStaleLock(t, dir, "boutique-prod", "job-of-a-dead-agent")

	taken, err := backup.Acquire(dir, "boutique-prod", "job-2", time.Now())
	if err != nil {
		t.Fatalf("a lock held by a dead process blocked a new job: %v", err)
	}
	if err := taken.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// BKP-01, `N-5` — the lock is a file, and it says who holds it. A lock that
// cannot be read without SQL is a lock nobody unblocks at three in the morning.
func TestBKP01TheLockSaysWhoHoldsIt(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 9, 22, 2, 0, 3, 0, time.UTC)

	held, err := backup.Acquire(dir, "boutique-prod", "01JQ8F3K2M7X9P4W", at)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer func() { _ = held.Release() }()

	written, err := os.ReadFile(held.Path())
	if err != nil {
		t.Fatalf("read the lock file: %v", err)
	}

	for _, want := range []string{"boutique-prod", "01JQ8F3K2M7X9P4W", "2026-09-22T02:00:03Z"} {
		if !strings.Contains(string(written), want) {
			t.Errorf("the lock file does not carry %q:\n%s", want, written)
		}
	}
	if !strings.HasPrefix(held.Path(), dir) {
		t.Errorf("the lock was written outside the state directory: %s", held.Path())
	}
}

// BKP-01 — an identifier that tries to climb out of the lock directory does not.
// A configuration is a file somebody edits; a path that climbs is a lock that
// overwrites something else.
func TestBKP01AnIdentifierNeverClimbsOutOfTheLockDirectory(t *testing.T) {
	dir := t.TempDir()

	held, err := backup.Acquire(dir, "../../etc/passwd", "job-1", time.Now())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer func() { _ = held.Release() }()

	clean := filepath.Clean(held.Path())
	if !strings.HasPrefix(clean, filepath.Clean(dir)+string(filepath.Separator)) {
		t.Errorf("the lock escaped its directory: %s", clean)
	}
}

// BKP-01 — and a lock file nobody can make sense of does not block forever
// either. A truncated write during a power cut must not need a human.
func TestBKP01ALockFileThatCannotBeReadIsTakenOver(t *testing.T) {
	dir := t.TempDir()

	held, err := backup.Acquire(dir, "boutique-prod", "job-1", time.Now())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	path := held.Path()
	if err := held.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := os.WriteFile(path, []byte("{ this was cut off"), 0o600); err != nil {
		t.Fatalf("write a truncated lock: %v", err)
	}

	taken, err := backup.Acquire(dir, "boutique-prod", "job-2", time.Now())
	if err != nil {
		t.Fatalf("an unreadable lock blocked a new job: %v", err)
	}
	_ = taken.Release()
}

// writeStaleLock plants a lock file held by a process that does not exist: a
// real one, written by Acquire, whose pid is then replaced by one the kernel
// will not have handed out. Writing the file by hand would let the test keep
// passing after a change of format, for the wrong reason.
func writeStaleLock(t *testing.T, dir, database, job string) {
	t.Helper()

	held, err := backup.Acquire(dir, database, job, time.Now())
	if err != nil {
		t.Fatalf("Acquire to plant a stale lock: %v", err)
	}

	path := held.Path()

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the planted lock: %v", err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	mine := strconv.Itoa(os.Getpid())

	stale := strings.Replace(string(contents), mine, "2147483646", 1)
	if stale == string(contents) {
		t.Fatalf("the lock file does not carry the pid of this process (%s):\n%s", mine, contents)
	}

	if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
		t.Fatalf("write the stale lock: %v", err)
	}
}
