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

	// Export returns everything the catalog holds, in a form that can be
	// written to the repository and read back by a different Koffr.
	//
	// This is what makes "the catalog is a cache" true rather than merely
	// stated (EF-141). Without it, losing the machine loses the job history --
	// including the failures, which leave no manifest behind and exist nowhere
	// else.
	Export(ctx context.Context) (Snapshot, error)

	// Import merges a snapshot in. It is idempotent and additive: rows already
	// present are updated, rows absent from the snapshot are left alone.
	//
	// Additive, not replacing, because a rebuild can run on a catalog that is
	// merely behind rather than empty -- and a sync that deleted the jobs
	// recorded since the last replication would be a repair that loses data.
	Import(ctx context.Context, s Snapshot) error

	io.Closer
}

// Backup is one stored backup.
type Backup struct {
	ID          ID        `json:"backup_id"`
	SourceID    string    `json:"source_id"`
	Kind        string    `json:"kind"`
	ParentID    ID        `json:"parent_id,omitempty"`
	Destination string    `json:"destination"`
	Status      Status    `json:"status"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
	SizeBytes   int64     `json:"size_bytes"`
	ManifestKey string    `json:"manifest_key"`
	// Positions are denormalised from the manifest so retention can walk
	// chains and enforce the WAL guard without fetching manifests (EF-063).
	StartLSN   string `json:"start_lsn,omitempty"`
	EndLSN     string `json:"end_lsn,omitempty"`
	BinlogFile string `json:"binlog_file,omitempty"`
	BinlogPos  uint64 `json:"binlog_pos"`
}

// Job is one execution attempt, successful or not.
type Job struct {
	ID          string     `json:"job_id"`
	SourceID    string     `json:"source_id"`
	Kind        string     `json:"kind"`
	Trigger     Trigger    `json:"trigger"`
	Status      Status     `json:"status"`
	ErrorClass  ErrorClass `json:"error_class,omitempty"`
	ErrorDetail string     `json:"error_detail,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  time.Time  `json:"finished_at"`
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
	ID         string    `json:"verification_id"`
	BackupID   ID        `json:"backup_id"`
	Tier       int       `json:"tier"`
	Status     Status    `json:"status"`
	Report     []byte    `json:"report"` // JSON
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// BackupFilter selects backups to list.
// Snapshot is the whole catalog in a portable form.
//
// JSON rather than a copy of the database file, deliberately: the replica in
// the repository has to be readable by a Koffr that is not this one, and
// ideally by a person with jq (PD-001). It also keeps the door open to a
// different metadata engine -- an operator choosing PostgreSQL at install time
// should be able to import a snapshot written by SQLite.
type Snapshot struct {
	// FormatVersion is the snapshot's own version, not the catalog schema's.
	// A Koffr reading a newer one must refuse rather than guess.
	FormatVersion int `json:"format_version"`

	ExportedAt time.Time `json:"exported_at"`
	// KoffrVersion says what wrote it, for the same reason a manifest does.
	KoffrVersion string `json:"koffr_version"`

	Backups       []Backup       `json:"backups"`
	Jobs          []Job          `json:"jobs"`
	Verifications []Verification `json:"verifications"`
}

// SnapshotFormatVersion is the current snapshot format.
const SnapshotFormatVersion = 1

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
