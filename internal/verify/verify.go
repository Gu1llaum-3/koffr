// Package verify checks that a backup is actually restorable.
//
// PD-007: a backup that has not been verified is a hypothesis, not a backup.
package verify

import (
	"context"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
)

// Tier is how deep a verification goes. Higher tiers subsume lower ones.
type Tier int

const (
	// TierIntegrity needs no container runtime and is never disabled: digests,
	// end-to-end decrypt and decompress, structural validation, manifest
	// consistency (EF-070, EF-077).
	TierIntegrity Tier = 1

	// TierRestore starts a throwaway container, restores into it, and counts
	// rows (EF-071). Requires Docker or Podman, locally or on a remote
	// verification host.
	TierRestore Tier = 2

	// TierBusiness runs operator-supplied SQL against the restored instance and
	// asserts on the results (EF-072).
	TierBusiness Tier = 3
)

// Verifier runs one verification against one backup.
type Verifier interface {
	Verify(ctx context.Context, b catalog.Backup, tier Tier) (Report, error)

	// Available reports whether this verifier can run right now. It is called
	// at configuration load so a missing container runtime is a startup error
	// rather than a 3 AM surprise (PD-006, EF-073).
	Available(ctx context.Context) error
}

// Report is the outcome of a verification.
type Report struct {
	Tier      Tier
	Passed    bool
	StartedAt time.Time
	Duration  time.Duration

	// Tables is populated from tier 2 onwards.
	Tables []TableReport

	// Assertions is populated at tier 3.
	Assertions []AssertionReport

	// Failures explains what went wrong, in operator-readable form.
	Failures []string
}

// TableReport is one restored table.
type TableReport struct {
	Schema string
	Name   string
	Rows   int64
}

// AssertionReport is one operator-supplied check.
type AssertionReport struct {
	Name     string
	Query    string
	Expected string
	Actual   string
	Passed   bool
}
