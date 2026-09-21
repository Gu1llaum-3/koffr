package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ErrLocked is E-051: a job is already running on this database, and a second
// one is **refused**. Never queued — a queue would let a nightly schedule pile
// up jobs on a server that is already struggling (§ 5.3 F3.1).
var ErrLocked = errors.New("a job is already running on this database")

// lockDirectory is where the locks live, under the state directory. They are
// files rather than rows because a lock has to survive a corrupted database and
// be readable by eye: one that needs SQL to inspect is one nobody unblocks at
// three in the morning (`N-5`).
const lockDirectory = "locks"

// Lock is a held lock on one database. It is released by the job that took it,
// and taken over by the next one when the process that held it is gone.
type Lock struct {
	path string
}

// holder is what a lock file says, in a shape a human reads without a tool.
type holder struct {
	Database   string `json:"database"`
	Job        string `json:"job"`
	PID        int    `json:"pid"`
	AcquiredAt string `json:"acquired_at"`
}

// Acquire takes the lock of one database, or refuses saying who holds it.
//
// A lock left behind by a process that no longer exists does not block: a
// machine that loses power at 3 a.m. must back up again the next night without
// anyone logging in to delete a file. Neither does a lock file that cannot be
// read — a write cut in half is not a reason to stop backing up.
func Acquire(stateDirectory, database, job string, at time.Time) (*Lock, error) {
	directory := filepath.Join(stateDirectory, lockDirectory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("make the lock directory %s: %w", directory, err)
	}

	path := filepath.Join(directory, safeSegment(database)+".lock")

	written, err := json.MarshalIndent(holder{
		Database:   database,
		Job:        job,
		PID:        os.Getpid(),
		AcquiredAt: at.UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("describe the lock of %s: %w", database, err)
	}

	written = append(written, '\n')

	// Two attempts at most: the first, and one more after a lock nobody holds
	// has been cleared. A third would be a loop against another agent.
	for attempt := range 2 {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, writeErr := file.Write(written)
			closeErr := file.Close()

			if err := errors.Join(writeErr, closeErr); err != nil {
				_ = os.Remove(path)

				return nil, fmt.Errorf("write the lock of %s: %w", database, err)
			}

			return &Lock{path: path}, nil
		}

		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("take the lock of %s: %w", database, err)
		}

		if attempt > 0 {
			break
		}

		if err := clearIfAbandoned(path, database); err != nil {
			return nil, err
		}
	}

	return nil, fmt.Errorf("%w: %s", ErrLocked, database)
}

// clearIfAbandoned removes a lock nobody holds any more, and reports the
// refusal of E-051 when somebody does.
func clearIfAbandoned(path, database string) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // it went away on its own; the next attempt takes it
		}

		return fmt.Errorf("read the lock of %s: %w", database, err)
	}

	var held holder
	if err := json.Unmarshal(contents, &held); err != nil {
		// A write cut in half is not a reason to stop backing up.
		return removeStale(path, database)
	}

	if !running(held.PID) {
		return removeStale(path, database)
	}

	return fmt.Errorf("%w: %s is being backed up by the job %s, started at %s (pid %d)",
		ErrLocked, held.Database, held.Job, held.AcquiredAt, held.PID)
}

func removeStale(path, database string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear the abandoned lock of %s: %w", database, err)
	}

	return nil
}

// running says whether a process still exists. Signal zero asks the kernel
// exactly that and delivers nothing.
func running(pid int) bool {
	if pid <= 0 {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	return process.Signal(syscall.Signal(0)) == nil
}

// Path is where the lock is written. A job that has to be unblocked by hand is
// unblocked by removing this file.
func (l *Lock) Path() string {
	return l.path
}

// Release gives the database back. Releasing twice is not an error: a job that
// failed halfway must not fail again on its way out.
func (l *Lock) Release() error {
	if l == nil || l.path == "" {
		return nil
	}

	if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("release the lock %s: %w", l.path, err)
	}

	l.path = ""

	return nil
}
