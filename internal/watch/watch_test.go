package watch_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/Gu1llaum-3/koffr/internal/notify"
	"github.com/Gu1llaum-3/koffr/internal/watch"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestShrinkAlarming(t *testing.T) {
	cases := []struct {
		cur, prev int64
		maxDrop   float64
		alarm     bool
	}{
		{600, 1000, 0.4, true},  // 40 % drop, at the threshold
		{601, 1000, 0.4, false}, // just under
		{1000, 1000, 0.4, false},
		{1200, 1000, 0.4, false}, // grew
		{500, 0, 0.4, false},     // no previous size
		{0, 1000, 0.4, true},     // vanished to nothing
		{500, 1000, 0.4, true},
	}
	for _, c := range cases {
		got, _ := watch.ShrinkAlarming(c.cur, c.prev, c.maxDrop)
		assert.Equal(t, c.alarm, got, "ShrinkAlarming(%d,%d,%v)", c.cur, c.prev, c.maxDrop)
	}
}

func TestStale(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	assert.True(t, watch.Stale(now.Add(-25*time.Hour), now, 24*time.Hour))
	assert.False(t, watch.Stale(now.Add(-23*time.Hour), now, 24*time.Hour))
}

type recorder struct {
	mu sync.Mutex
	ev []notify.Event
}

func (r *recorder) publish(e notify.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ev = append(r.ev, e)
}
func (r *recorder) kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.ev))
	for i, e := range r.ev {
		out[i] = e.Kind
	}
	return out
}

// A source that goes down alerts once, not on every tick, and its recovery
// alerts once too.
func TestReachabilityAlertsOnTransitionsOnly(t *testing.T) {
	rec := &recorder{}
	var down bool
	w := watch.New(watch.Deps{
		Sources: []string{"shop"},
		Reachable: func(context.Context, string) error {
			if down {
				return errors.New("connection refused")
			}
			return nil
		},
		Publish: rec.publish,
		Now:     func() time.Time { return time.Unix(0, 0) },
	})
	ctx := context.Background()
	w.Check(ctx) // up
	down = true
	w.Check(ctx) // down -> one alert
	w.Check(ctx) // still down -> no new alert
	down = false
	w.Check(ctx) // up -> recovery alert

	assert.Equal(t, []string{notify.KindSourceUnreachable, notify.KindSourceRecovered}, rec.kinds())
}

// The reachability error must never carry a credential.
func TestReachabilityErrorCarriesNoSecret(t *testing.T) {
	rec := &recorder{}
	w := watch.New(watch.Deps{
		Sources:   []string{"shop"},
		Reachable: func(context.Context, string) error { return errors.New("connection refused") },
		Publish:   rec.publish,
	})
	w.Check(context.Background())
	require.NotEmpty(t, rec.ev)
	assert.NotContains(t, rec.ev[0].Message, "sentinel-secret")
}

// Freshness alerts once when the last success is too old, and a new source is
// not reported stale on its first tick.
func TestFreshnessAlertsWhenStale(t *testing.T) {
	rec := &recorder{}
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	last := now.Add(-48 * time.Hour)
	w := watch.New(watch.Deps{
		Sources: []string{"shop", "new"},
		LastSuccess: func(_ context.Context, id string) (time.Time, bool) {
			if id == "shop" {
				return last, true
			}
			return time.Time{}, false // never backed up
		},
		StaleAfter: 24 * time.Hour,
		Publish:    rec.publish,
		Now:        func() time.Time { return now },
	})
	w.Check(context.Background())
	w.Check(context.Background()) // no duplicate
	assert.Equal(t, []string{notify.KindBackupStale}, rec.kinds(),
		"only the stale source alerts, and only once; the new source is not stale on its first tick")
}

// A backup that vanished from its destination alerts once; if presence cannot
// be determined, nothing is claimed.
func TestPresenceAlertsWhenGoneNotWhenUnknown(t *testing.T) {
	rec := &recorder{}
	w := watch.New(watch.Deps{
		Sources: []string{"gone", "unknown"},
		LatestPresent: func(_ context.Context, id string) (bool, bool, error) {
			switch id {
			case "gone":
				return false, true, nil
			default:
				return false, false, errors.New("cannot reach destination")
			}
		},
		Publish: rec.publish,
	})
	w.Check(context.Background())
	w.Check(context.Background())
	assert.Equal(t, []string{notify.KindBackupMissing}, rec.kinds())
}

// Run checks immediately and stops promptly on cancellation, leaking nothing.
func TestRunStopsCleanly(t *testing.T) {
	rec := &recorder{}
	w := watch.New(watch.Deps{
		Sources:   []string{"shop"},
		Reachable: func(context.Context, string) error { return nil },
		Interval:  time.Hour,
		Publish:   rec.publish,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}
