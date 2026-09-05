// Package catalog holds the operational history of backups, jobs and
// verifications.
//
// The catalog is a CACHE. The repository is the source of truth (EF-141), and
// Sync rebuilds the catalog from it after a total loss of the Koffr node
// (EF-142). This is why the backend choice is unremarkable: SQLite avoids a
// circular dependency on a database that would itself need backing up
// (DEC-004).
package catalog

import (
	"context"
	"io"
	"time"
)

// ID identifies a backup. It is a ULID, so lexical order is chronological
// order.
type ID string

// Status is the outcome of a job, a backup or a verification.
type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
)

// ErrorClass decides whether an operation is retried. The class alone drives
// that decision; the message never does (ENF-011).
type ErrorClass string

const (
	// ErrClassConfig is not retried: the configuration must be fixed.
	ErrClassConfig ErrorClass = "config"
	// ErrClassSource is retried with exponential backoff.
	ErrClassSource ErrorClass = "source"
	// ErrClassNetwork is retried, resuming the multipart upload.
	ErrClassNetwork ErrorClass = "network"
	// ErrClassStorage is retried depending on the backend's own answer.
	ErrClassStorage ErrorClass = "storage"
	// ErrClassCrypto is not retried: an invalid recipient or unreadable key.
	ErrClassCrypto ErrorClass = "crypto"
	// ErrClassStalled is retried once, then fails (EF-095).
	ErrClassStalled ErrorClass = "stalled"
	// ErrClassCanceled is not retried.
	ErrClassCanceled ErrorClass = "canceled"
)

// MetadataStore is the catalog backend.
type MetadataStore interface {
	RecordJob(ctx context.Context, j Job) error
	RecordBackup(ctx context.Context, b Backup) error
	RecordVerification(ctx context.Context, v Verification) error

	ListBackups(ctx context.Context, f BackupFilter) ([]Backup, error)

	// Chain returns a backup and every ancestor it depends on. Retention
	// consults it before deleting anything, so an incremental chain can never
	// be broken (EF-062).
	Chain(ctx context.Context, id ID) ([]Backup, error)

	// Overview answers "is everything fine", for the CLI and for the read-only
	// status endpoint (EF-134).
	Overview(ctx context.Context) (Overview, error)

	io.Closer
}

// Backup is one stored backup.
type Backup struct {
	ID          ID
	SourceID    string
	Kind        string
	ParentID    ID
	Destination string
	Status      Status
	StartedAt   time.Time
	FinishedAt  time.Time
	SizeBytes   int64
	ManifestKey string

	// Positions are denormalised from the manifest so retention can walk
	// chains and enforce the WAL guard without fetching manifests (EF-063).
	StartLSN   string
	EndLSN     string
	BinlogFile string
	BinlogPos  uint64
}

// Job is one execution attempt, successful or not.
type Job struct {
	ID          string
	SourceID    string
	Kind        string
	Trigger     Trigger
	Status      Status
	ErrorClass  ErrorClass
	ErrorDetail string
	StartedAt   time.Time
	FinishedAt  time.Time
}

// Trigger says what started a job.
type Trigger string

const (
	TriggerSchedule Trigger = "schedule"
	TriggerManual   Trigger = "manual"
	TriggerRetry    Trigger = "retry"
)

// Verification is one verification run against one backup.
type Verification struct {
	ID         string
	BackupID   ID
	Tier       int
	Status     Status
	Report     []byte // JSON
	StartedAt  time.Time
	FinishedAt time.Time
}

// BackupFilter selects backups to list.
type BackupFilter struct {
	SourceID    string
	Kind        string
	Destination string
	Status      Status
	Since       time.Time
	Until       time.Time
	Limit       int
}

// Overview is the per-source health summary.
type Overview struct {
	Sources []SourceOverview
}

// SourceOverview answers the only question that matters for monitoring: how
// old is the last thing we know to be good.
type SourceOverview struct {
	SourceID           string
	LastSuccessfulAt   time.Time
	LastVerifiedAt     time.Time
	LastFailureAt      time.Time
	LastFailureClass   ErrorClass
	BackupCount        int
	TotalSizeBytes     int64
	NextScheduledAt    time.Time
	OldestRestorableAt time.Time
}
