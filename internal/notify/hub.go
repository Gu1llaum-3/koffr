package notify

import (
	"context"
	"slices"
	"sync"
	"time"
)

// Event kinds. They are strings in a payload someone else parses, so they are
// declared once and never spelled inline.
const (
	KindBackupCompleted = "backup.completed"
	KindBackupFailed    = "backup.failed"
	KindBackupRetrying  = "backup.retrying"
	KindBackupSkipped   = "backup.skipped"

	// KindBackupCaughtUp says a scheduled window went by unattended and is
	// being made good now. It is a warning rather than information: the backup
	// is happening, but one did not happen when it should have, and that is
	// worth knowing before it becomes a habit.
	KindBackupCaughtUp = "backup.caught_up"

	// KindJobInterrupted says a job was found still marked running by a
	// process that is gone -- a crash, a kill, a reboot mid-backup.
	KindJobInterrupted = "job.interrupted"

	KindRestoreCompleted = "restore.completed"
	KindRestoreFailed    = "restore.failed"
)

// AtLeast reports whether s is as urgent as min.
func (s Severity) AtLeast(min Severity) bool { return rank(s) >= rank(min) }

func rank(s Severity) int {
	switch s {
	case SeverityError:
		return 2
	case SeverityWarning:
		return 1
	default:
		return 0
	}
}

// Channel is one notifier with the filter an operator put in front of it.
//
// Both filters exist because they answer different questions. Severity is "how
// much do I want to hear"; kinds is "about what". Someone who wants every
// restore in a chat room and only failures by email needs both.
type Channel struct {
	Notifier    Notifier
	MinSeverity Severity

	// Kinds restricts to these event kinds. Empty means all of them.
	Kinds []string
}

func (c Channel) wants(ev Event) bool {
	if !ev.Severity.AtLeast(c.MinSeverity) {
		return false
	}
	return len(c.Kinds) == 0 || slices.Contains(c.Kinds, ev.Kind)
}

// Hub fans an event out to every channel that asked for it.
//
// Delivery is asynchronous and bounded, and a failure is reported rather than
// returned. That is the whole design: the backup already happened, and an
// unreachable webhook does not un-happen it. A notifier that could fail a job
// would make the alerting less reliable than the thing it watches.
//
// Each channel has a queue of its own and one worker, so events reach a given
// channel in the order they were published. That ordering is not a nicety: an
// email saying a backup failed, arriving after the one saying it succeeded,
// tells the reader the opposite of what happened. Channels do not wait on each
// other -- a slow webhook must not delay the email.
type Hub struct {
	Channels []Channel

	// Timeout bounds one delivery. Zero means ten seconds: a hung webhook must
	// not hold a scheduler that has another source to back up.
	Timeout time.Duration

	// OnError reports a delivery that failed, for the log. Nil discards it,
	// which is only right when there is nowhere to report to.
	OnError func(name string, err error)

	start  sync.Once
	queues []chan Event
	wg     sync.WaitGroup

	closeOnce sync.Once
}

// queueDepth is how far a channel may fall behind before events are dropped.
//
// Dropping is the right answer at the far end of it. A notifier that cannot
// keep up with a backup schedule is broken, and blocking the scheduler on it
// would make the alerting able to stop the backups -- which is the one thing it
// must never do.
const queueDepth = 64

func (h *Hub) begin() {
	h.start.Do(func() {
		h.queues = make([]chan Event, len(h.Channels))
		for i := range h.Channels {
			q := make(chan Event, queueDepth)
			h.queues[i] = q

			h.wg.Add(1)
			go func(ch Channel, q chan Event) {
				defer h.wg.Done()
				for ev := range q {
					h.deliver(ch, ev)
				}
			}(h.Channels[i], q)
		}
	})
}

// Publish hands the event to every interested channel and returns immediately.
func (h *Hub) Publish(_ context.Context, ev Event) {
	if len(h.Channels) == 0 {
		return
	}
	// contextcheck cannot see the design here: the workers outlive this call by
	// construction, so there is no context to hand them. A backup that was
	// cancelled is the event most worth sending, and inheriting the cancelled
	// context would be exactly the wrong behaviour.
	//nolint:contextcheck // delivery deliberately outlives the publisher
	h.begin()

	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now().UTC()
	}
	if ev.Severity == "" {
		ev.Severity = SeverityInfo
	}

	for i, ch := range h.Channels {
		if !ch.wants(ev) {
			continue
		}
		select {
		case h.queues[i] <- ev:
		default:
			if h.OnError != nil {
				h.OnError(ch.Notifier.Name(),
					errQueueFull{kind: ev.Kind})
			}
		}
	}
}

// errQueueFull says an event was dropped rather than delivered late.
type errQueueFull struct{ kind string }

func (e errQueueFull) Error() string {
	return "notifications are backing up; dropped " + e.kind +
		" rather than hold the job that produced it"
}

func (h *Hub) deliver(ch Channel, ev Event) {
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	// A context of its own, not the publisher's. A backup that was cancelled is
	// exactly the event most worth sending, and inheriting the cancelled
	// context would drop it.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := ch.Notifier.Notify(ctx, ev); err != nil && h.OnError != nil {
		h.OnError(ch.Notifier.Name(), err)
	}
}

// Wait delivers what is queued and stops the workers.
//
// Called on shutdown, so a failure notification is not lost to the process
// exiting a millisecond after the job that produced it.
func (h *Hub) Wait() {
	h.closeOnce.Do(func() {
		for _, q := range h.queues {
			close(q)
		}
	})
	h.wg.Wait()
}
