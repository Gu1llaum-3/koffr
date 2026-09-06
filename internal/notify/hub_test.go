package notify_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/notify"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

// spy records what it was asked to deliver.
type spy struct {
	name string
	err  error

	mu   sync.Mutex
	got  []notify.Event
	slow time.Duration
}

func (s *spy) Name() string { return s.name }

func (s *spy) Notify(ctx context.Context, ev notify.Event) error {
	if s.slow > 0 {
		select {
		case <-time.After(s.slow):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, ev)
	return s.err
}

func (s *spy) kinds() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.got))
	for _, ev := range s.got {
		out = append(out, ev.Kind)
	}
	return out
}

func ev(kind string, sev notify.Severity) notify.Event {
	return notify.Event{Kind: kind, Severity: sev, SourceID: "prod", Message: "something happened"}
}

// EF-130: filtering by severity. Someone who wants only what wakes them up must
// be able to say so, or they turn the whole thing off.
func TestHub_FiltersBySeverity(t *testing.T) {
	all := &spy{name: "all"}
	urgent := &spy{name: "urgent"}

	hub := &notify.Hub{Channels: []notify.Channel{
		{Notifier: all, MinSeverity: notify.SeverityInfo},
		{Notifier: urgent, MinSeverity: notify.SeverityError},
	}}

	hub.Publish(t.Context(), ev("backup.completed", notify.SeverityInfo))
	hub.Publish(t.Context(), ev("backup.caught_up", notify.SeverityWarning))
	hub.Publish(t.Context(), ev("backup.failed", notify.SeverityError))
	hub.Wait()

	assert.Equal(t, []string{"backup.completed", "backup.caught_up", "backup.failed"}, all.kinds())
	assert.Equal(t, []string{"backup.failed"}, urgent.kinds(),
		"a channel asking for errors must not receive the nightly success it did not ask for")
}

// And by event kind, for the case severity cannot express: a chat room that
// wants restores and nothing else.
func TestHub_FiltersByKind(t *testing.T) {
	only := &spy{name: "restores"}
	hub := &notify.Hub{Channels: []notify.Channel{
		{Notifier: only, MinSeverity: notify.SeverityInfo, Kinds: []string{"restore.completed"}},
	}}

	hub.Publish(t.Context(), ev("backup.completed", notify.SeverityInfo))
	hub.Publish(t.Context(), ev("restore.completed", notify.SeverityInfo))
	hub.Wait()

	assert.Equal(t, []string{"restore.completed"}, only.kinds())
}

// A notifier that fails must never fail a backup. The backup happened; an
// unreachable webhook does not un-happen it, and reporting failure would send
// an operator to rerun work that is done.
func TestHub_ANotifierFailureIsReportedNotPropagated(t *testing.T) {
	broken := &spy{name: "broken", err: errors.New("connection refused")}
	working := &spy{name: "working"}

	var reported []string
	hub := &notify.Hub{
		Channels: []notify.Channel{
			{Notifier: broken, MinSeverity: notify.SeverityInfo},
			{Notifier: working, MinSeverity: notify.SeverityInfo},
		},
		OnError: func(name string, err error) { reported = append(reported, name+": "+err.Error()) },
	}

	hub.Publish(t.Context(), ev("backup.completed", notify.SeverityInfo))
	hub.Wait()

	assert.Len(t, working.kinds(), 1, "one channel failing must not stop the others")
	require.Len(t, reported, 1)
	assert.Contains(t, reported[0], "broken")
}

// A webhook that hangs must not hold a backup job open. Delivery is bounded and
// the job moves on.
func TestHub_SlowNotifierDoesNotBlockForever(t *testing.T) {
	slow := &spy{name: "slow", slow: time.Minute}
	hub := &notify.Hub{
		Channels: []notify.Channel{{Notifier: slow, MinSeverity: notify.SeverityInfo}},
		Timeout:  50 * time.Millisecond,
	}

	start := time.Now()
	hub.Publish(t.Context(), ev("backup.completed", notify.SeverityInfo))
	hub.Wait()
	assert.Less(t, time.Since(start), 5*time.Second, "a hung webhook must not hold the scheduler")
}

// ENF-021, on the one surface built to send things outward.
func TestHub_EventsCarryNoSecret(t *testing.T) {
	seen := &spy{name: "seen"}
	hub := &notify.Hub{Channels: []notify.Channel{{Notifier: seen, MinSeverity: notify.SeverityInfo}}}

	hub.Publish(t.Context(), notify.Event{
		Kind: "backup.failed", Severity: notify.SeverityError, SourceID: "prod",
		Message: "backup failed: connection refused",
		Details: map[string]string{"destination": "main"},
	})
	hub.Wait()

	require.Len(t, seen.kinds(), 1)
	testutil.AssertNoSecretLeak(t, seen.got[0].Message, seen.got[0].SourceID)
}

// A hub with nothing configured is the normal case for someone who has not set
// notifications up, and it must cost nothing and say nothing.
func TestHub_NoChannels(t *testing.T) {
	hub := &notify.Hub{}
	hub.Publish(t.Context(), ev("backup.completed", notify.SeverityInfo))
	hub.Wait()
}

func TestSeverity_Ordering(t *testing.T) {
	assert.True(t, notify.SeverityError.AtLeast(notify.SeverityWarning))
	assert.True(t, notify.SeverityWarning.AtLeast(notify.SeverityWarning))
	assert.False(t, notify.SeverityInfo.AtLeast(notify.SeverityWarning))
}
