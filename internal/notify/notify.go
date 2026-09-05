// Package notify reports what happened, and — more importantly — makes the
// absence of a report detectable.
package notify

import (
	"context"
	"time"
)

// Severity filters which events reach which notifier.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Notifier delivers an event to one channel.
type Notifier interface {
	Notify(ctx context.Context, ev Event) error

	// Name identifies the notifier in logs and in configuration errors.
	Name() string
}

// Event is what happened.
type Event struct {
	Severity   Severity
	Kind       string // backup.completed, verify.failed, prune.blocked, ...
	SourceID   string
	BackupID   string
	Message    string
	OccurredAt time.Time

	// Details is rendered into the webhook payload and the email body. It never
	// carries a credential (ENF-021).
	Details map[string]string
}

// DeadMansSwitch pings an external monitor when a job succeeds.
//
// This is the inverse of ordinary alerting and it is the only mechanism that
// catches a job which never ran at all: Koffr answers /healthz with 200 while
// no backup has happened for three weeks. The alert comes from the ping NOT
// arriving, so it cannot be defeated by Koffr being broken (EF-131).
type DeadMansSwitch interface {
	Ping(ctx context.Context, sourceID string) error
}
