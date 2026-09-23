package catalog

import (
	"context"
	"errors"
	"time"
)

// Verification is how far an archive has been checked. The four values are the
// ones the schema constrains (ADR-0006), and their order is meaningful:
// nothing, then the checksum, then the structure as well.
//
// `failed` is not the absence of a check — `P4` wants those never to be
// confused: an archive nobody looked at and an archive that was looked at and
// found wrong are two different situations for an operator.
type Verification string

// The four states of `backups.verified`.
const (
	NotVerified Verification = "none"
	Checksum    Verification = "checksum"
	Structure   Verification = "structure"
	Failed      Verification = "failed"
)

// Verified says whether this state counts as verified for `P4` — and therefore
// for the retention of the lot 5, which keeps only what was verified.
func (v Verification) Verified() bool {
	return v == Checksum || v == Structure
}

// ErrNoSuchBackup is returned when an identifier names nothing. Setting the
// state of a backup nobody recorded is an error, never a silent no-op:
// `koffr verify` on a wrong identifier has to say so.
var ErrNoSuchBackup = errors.New("no backup has this identifier")

// Backup is one archive as the catalogue keeps it.
//
// The catalogue is an **index**, never the source of truth: everything a
// restore needs lives in the manifest deposited beside the archive, so that a
// repository stays usable if the agent disappears (ADR-0006, E-059).
type Backup struct {
	ID       string
	Database string

	// Job is the execution that produced it. Empty until the scheduler writes
	// job rows, which the lot 5 brings; the schema allows it (`N-7`).
	Job string

	StartedAt  time.Time
	FinishedAt time.Time

	RawBytes    int64
	StoredBytes int64

	SHA256Raw    string
	SHA256Stored string

	Verified   Verification
	VerifiedAt time.Time

	// Manifest is the JSON deposited beside the archive, kept here so that a
	// listing needs no round trip to the destination.
	Manifest string

	Locations []Location
}

// Location is one copy of an archive, on one destination.
type Location struct {
	Destination string
	Path        string
	Bytes       int64
	StoredAt    time.Time
}

// Filter narrows a listing. Its zero value lists everything.
type Filter struct {
	Database    string
	Destination string
	Limit       int
}

// Catalog is the index of what has been backed up. The domain declares it;
// `internal/state` implements it, and `cmd/koffr` wires the two — `AR-01`
// forbids the domain to know SQLite, `AR-04` forbids a command to know it
// either (`N-3`).
type Catalog interface {
	// RecordDatabase stores the resolved configuration of a database, or
	// updates it. A backup cannot be recorded before it: the foreign key of the
	// schema requires the row, and E-028 wants the change noticed.
	RecordDatabase(ctx context.Context, database Database, at time.Time) error

	// RecordBackup indexes one archive and where its copies are.
	RecordBackup(ctx context.Context, backup Backup) error

	// SetVerification records what a verification concluded, afterwards —
	// because an archive is verified after it is written (E-024).
	SetVerification(ctx context.Context, id string, verified Verification, at time.Time) error

	// Backups lists what is indexed, **most recent first**: an operator
	// compares one listing to the last.
	Backups(ctx context.Context, filter Filter) ([]Backup, error)
}
