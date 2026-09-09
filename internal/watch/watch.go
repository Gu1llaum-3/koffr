// Package watch is Koffr's supervision of its own promise: it alerts on the
// state of the backups and of the path to them -- a source that stops
// answering, a backup that did not run, one that shrank, one that is no longer
// on the remote -- and on nothing about the health of the database itself.
// Disk, connections, replication lag and slow queries belong to a dedicated
// monitor (EF-138, EF-139); this draws the line deliberately.
package watch

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/notify"
)

// ShrinkAlarming reports whether a backup shrank enough versus the previous one
// to be worth an alert, and by what fraction. A backup that loses 40 % of its
// size overnight is an incident, not a fluctuation.
//
// Growth and equality never alarm; a first backup (no previous) never alarms;
// a previous size of zero cannot yield a meaningful fraction and never alarms.
func ShrinkAlarming(current, previous int64, maxDrop float64) (bool, float64) {
	if previous <= 0 || current >= previous {
		return false, 0
	}
	drop := float64(previous-current) / float64(previous)
	return drop >= maxDrop, drop
}

// Stale reports whether too long has passed since the last successful backup.
// A zero lastSuccess (none ever) is stale once threshold has passed since the
// reference; callers pass the source's first-seen time as lastSuccess so a
// brand-new source is not reported stale on the first tick.
func Stale(lastSuccess, now time.Time, threshold time.Duration) bool {
	return now.Sub(lastSuccess) > threshold
}

// Deps are what the watcher needs from the outside, injected so the loop is
// testable without a database or an object store.
type Deps struct {
	Sources []string

	// Reachable returns nil when the source answers a trivial query. Its error
	// must never carry a credential (ENF-021).
	Reachable func(ctx context.Context, sourceID string) error

	// LastSuccess is when the source last backed up successfully; ok is false
	// when it never has, in which case Since is used as the reference.
	LastSuccess func(ctx context.Context, sourceID string) (t time.Time, ok bool)

	// LatestPresent reports whether the source's most recent backup is still on
	// its destinations. ok is false when there is nothing to check yet.
	LatestPresent func(ctx context.Context, sourceID string) (present bool, ok bool, err error)

	StaleAfter time.Duration
	Interval   time.Duration

	Publish func(notify.Event)
	Logf    func(format string, args ...any)
	Now     func() time.Time
}

// Watcher runs the checks on a cadence and alerts on transitions only, so a
// source that has been down for an hour produces one alert, not sixty.
type Watcher struct {
	deps Deps

	mu        sync.Mutex
	firstSeen map[string]time.Time
	up        map[string]bool // last known reachability; absent = assumed up
	stale     map[string]bool // whether a stale alert is outstanding
	missing   map[string]bool // whether a missing-backup alert is outstanding
}

// New builds a watcher, filling defaults.
func New(d Deps) *Watcher {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logf == nil {
		d.Logf = func(string, ...any) {}
	}
	if d.Interval <= 0 {
		d.Interval = time.Minute
	}
	if d.StaleAfter <= 0 {
		d.StaleAfter = 24 * time.Hour
	}
	return &Watcher{
		deps:      d,
		firstSeen: map[string]time.Time{},
		up:        map[string]bool{},
		stale:     map[string]bool{},
		missing:   map[string]bool{},
	}
}

// Run checks once immediately, then every Interval, until the context ends.
func (w *Watcher) Run(ctx context.Context) error {
	w.Check(ctx)
	t := time.NewTicker(w.deps.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			w.Check(ctx)
		}
	}
}

// Check runs one pass over every source.
func (w *Watcher) Check(ctx context.Context) {
	for _, id := range w.deps.Sources {
		if ctx.Err() != nil {
			return
		}
		w.checkReachable(ctx, id)
		w.checkFresh(ctx, id)
		w.checkPresent(ctx, id)
	}
}

func (w *Watcher) checkReachable(ctx context.Context, id string) {
	if w.deps.Reachable == nil {
		return
	}
	err := w.deps.Reachable(ctx, id)
	w.mu.Lock()
	wasUp, known := w.up[id]
	if !known {
		wasUp = true // assume up, so the first failure is a transition worth alerting
	}
	nowUp := err == nil
	w.up[id] = nowUp
	w.mu.Unlock()

	switch {
	case wasUp && !nowUp:
		w.deps.Publish(notify.Event{
			Severity: notify.SeverityError, Kind: notify.KindSourceUnreachable, SourceID: id,
			OccurredAt: w.deps.Now(),
			Message:    fmt.Sprintf("source %s stopped answering: %v", id, err),
		})
	case !wasUp && nowUp:
		w.deps.Publish(notify.Event{
			Severity: notify.SeverityInfo, Kind: notify.KindSourceRecovered, SourceID: id,
			OccurredAt: w.deps.Now(),
			Message:    fmt.Sprintf("source %s is answering again", id),
		})
	}
}

func (w *Watcher) checkFresh(ctx context.Context, id string) {
	if w.deps.LastSuccess == nil {
		return
	}
	now := w.deps.Now()
	last, ok := w.deps.LastSuccess(ctx, id)
	if !ok {
		// Never backed up. Measure staleness from when we first saw the source,
		// so a freshly added one is not reported stale on the first tick.
		w.mu.Lock()
		fs, seen := w.firstSeen[id]
		if !seen {
			fs = now
			w.firstSeen[id] = fs
		}
		w.mu.Unlock()
		last = fs
	}
	stale := Stale(last, now, w.deps.StaleAfter)

	w.mu.Lock()
	was := w.stale[id]
	w.stale[id] = stale
	w.mu.Unlock()

	if stale && !was {
		w.deps.Publish(notify.Event{
			Severity: notify.SeverityWarning, Kind: notify.KindBackupStale, SourceID: id,
			OccurredAt: now,
			Message: fmt.Sprintf("no successful backup of %s in the last %s",
				id, w.deps.StaleAfter),
		})
	}
}

func (w *Watcher) checkPresent(ctx context.Context, id string) {
	if w.deps.LatestPresent == nil {
		return
	}
	present, ok, err := w.deps.LatestPresent(ctx, id)
	if err != nil || !ok {
		return // cannot tell: do not guess a backup is gone (fail-safe)
	}
	w.mu.Lock()
	was := w.missing[id]
	w.missing[id] = !present
	w.mu.Unlock()

	if !present && !was {
		w.deps.Publish(notify.Event{
			Severity: notify.SeverityError, Kind: notify.KindBackupMissing, SourceID: id,
			OccurredAt: w.deps.Now(),
			Message:    fmt.Sprintf("the most recent backup of %s is no longer on its destination", id),
		})
	}
}
