//go:build milestone

// Package milestone_test holds the checks that close a milestone.
//
// They are behind a build tag and a `make verify-milestone` target rather than
// in the ordinary suite, because they take tens of minutes and move tens of
// gigabytes. A check that long, run in front of every push, is a check that
// gets skipped -- and then a milestone closes on a criterion nobody measured.
//
// What is here is exactly what the fast suite cannot cover: scale. Correctness
// is proved elsewhere, on small data, in seconds.
package milestone_test

import (
	"testing"
)

// TestMilestone is the entry point make verify-milestone runs.
//
// The M1 criteria it has to answer, from the roadmap:
//
//  1. Backup and restore of a 10 GiB database, to fs and to S3.
//  2. PD-001 at that scale: the same backup restored from RESTORE.md alone.
//  3. Peak heap under 512 MiB on that database (ENF-001).
//  4. No temporary file, on the database host or on this one (PD-003).
//  5. Integration green on PostgreSQL 14 through 18. -- covered, by
//     `make verify-pg-matrix`, which runs first.
//
// One through four are deliberately unimplemented rather than quietly absent.
// A milestone criterion with no code behind it is what this project has already
// been caught by twice, and a target that fails loudly is visible where a
// missing one is not. Closing them needs a 10 GiB fixture, a heap measurement
// around the whole job, and a filesystem watch on both hosts.
func TestMilestone(t *testing.T) {
	t.Fatal("verify-milestone covers only criterion 5 so far (make verify-pg-matrix); " +
		"criteria 1, 3 and 4 -- the 10 GiB run, the heap ceiling at that size, and the " +
		"temporary-file watch -- still have no code behind them")
}
