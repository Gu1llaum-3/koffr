package scheduler_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/scheduler"
)

// clock is a hand-wound clock. The scheduler takes its time from a function so
// that a test about "what happens at 2 AM tomorrow" does not have to wait.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock(t *testing.T, at string) *clock {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, at)
	require.NoError(t, err)
	return &clock{now: parsed}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// recorder stands in for the backup runner.
type recorder struct {
	mu      sync.Mutex
	started []string
	release chan struct{} // when non-nil, Execute blocks until it is closed
	fail    func(attempt int) error
	calls   atomic.Int32
}

func (r *recorder) Execute(ctx context.Context, job scheduler.Job) error {
	n := int(r.calls.Add(1))

	r.mu.Lock()
	r.started = append(r.started, job.SourceID)
	release := r.release
	r.mu.Unlock()

	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if r.fail != nil {
		return r.fail(n)
	}
	return nil
}

func (r *recorder) runs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.started...)
}

func newScheduler(t *testing.T, c *clock, r *recorder, jobs ...scheduler.Job) *scheduler.Scheduler {
	t.Helper()
	s := &scheduler.Scheduler{
		Now:     c.Now,
		Tick:    time.Millisecond,
		Execute: r.Execute,
	}
	require.NoError(t, s.SetJobs(jobs))
	return s
}

// run starts the scheduler and returns a function that stops it and waits.
func run(t *testing.T, s *scheduler.Scheduler) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	return func() {
		cancel()
		select {
		case err := <-done:
			assert.ErrorIs(t, err, context.Canceled)
		case <-time.After(5 * time.Second):
			t.Fatal("the scheduler did not stop when its context ended")
		}
	}
}

// eventually waits for a condition the scheduler reaches on its own clock.
func eventually(t *testing.T, why string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(why)
}

// EF-090. The spec is cron, and the shortcuts are what people actually write.
func TestSetJobs_AcceptsCronAndShortcuts(t *testing.T) {
	for _, spec := range []string{"0 2 * * *", "@daily", "@hourly", "*/15 * * * *", "@every 30m"} {
		s := &scheduler.Scheduler{Execute: func(context.Context, scheduler.Job) error { return nil }}
		assert.NoError(t, s.SetJobs([]scheduler.Job{{SourceID: "prod", Spec: spec}}), "spec %q", spec)
	}
}

// A bad spec is refused when the configuration loads, not at 2 AM (PD-006).
func TestSetJobs_RefusesABadSpec(t *testing.T) {
	s := &scheduler.Scheduler{}
	err := s.SetJobs([]scheduler.Job{{SourceID: "prod", Spec: "every night please"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prod")
}

func TestRun_FiresOnSchedule(t *testing.T) {
	defer goleak.VerifyNone(t)

	c := newClock(t, "2026-03-01T01:59:00Z")
	r := &recorder{}
	s := newScheduler(t, c, r, scheduler.Job{SourceID: "prod", Spec: "0 2 * * *"})

	stop := run(t, s)
	defer stop()

	c.advance(time.Minute)
	eventually(t, "the job never fired at its scheduled time", func() bool {
		return len(r.runs()) == 1
	})

	// And exactly once for that window, however many ticks pass.
	c.advance(30 * time.Second)
	time.Sleep(20 * time.Millisecond)
	assert.Len(t, r.runs(), 1, "one window, one run")
}

// EF-092. An execution that overruns its next window is skipped and said so,
// never queued: a backup that piles up behind itself turns one slow night into
// a source with four pg_dumps on it.
func TestRun_OverrunSkipsRatherThanStacks(t *testing.T) {
	defer goleak.VerifyNone(t)

	c := newClock(t, "2026-03-01T01:59:59Z")
	release := make(chan struct{})
	r := &recorder{release: release}

	var skipped atomic.Int32
	s := newScheduler(t, c, r, scheduler.Job{SourceID: "prod", Spec: "* * * * *"})
	s.OnSkip = func(scheduler.Job, string) { skipped.Add(1) }

	stop := run(t, s)
	defer func() { close(release); stop() }()

	c.advance(time.Second)
	eventually(t, "the first run never started", func() bool { return len(r.runs()) == 1 })

	// Three more windows go by while the first is still running.
	for range 3 {
		c.advance(time.Minute)
		time.Sleep(5 * time.Millisecond)
	}
	assert.Len(t, r.runs(), 1, "a run in progress must not be joined by another")
	assert.GreaterOrEqual(t, int(skipped.Load()), 1, "a skipped window has to be reported, not silent")
}

// EF-093. Two sources due at the same instant, room for one.
func TestRun_GlobalConcurrencyIsCapped(t *testing.T) {
	defer goleak.VerifyNone(t)

	c := newClock(t, "2026-03-01T01:59:59Z")
	release := make(chan struct{})
	r := &recorder{release: release}

	s := newScheduler(t, c, r,
		scheduler.Job{SourceID: "a", Spec: "* * * * *"},
		scheduler.Job{SourceID: "b", Spec: "* * * * *"})
	s.MaxConcurrent = 1

	stop := run(t, s)
	defer func() { close(release); stop() }()

	c.advance(time.Second)
	eventually(t, "nothing started", func() bool { return len(r.runs()) >= 1 })

	time.Sleep(20 * time.Millisecond)
	assert.Len(t, r.runs(), 1, "the second job must wait, not saturate the link")
}

// EF-094 and ENF-011 together. The class decides whether a failure is worth
// retrying; retrying a configuration mistake every minute for a week is how a
// scheduler becomes noise nobody reads.
func TestRun_RetriesTransientFailuresOnly(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCalls int
	}{
		{"transient failure is retried", &classedError{catalog.ErrClassStorage}, 3},
		{"configuration failure is not", &classedError{catalog.ErrClassConfig}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)

			c := newClock(t, "2026-03-01T01:59:59Z")
			r := &recorder{fail: func(int) error { return tc.err }}

			s := newScheduler(t, c, r, scheduler.Job{SourceID: "prod", Spec: "0 2 * * *"})
			s.Retry = scheduler.RetryPolicy{Attempts: 3, InitialDelay: time.Minute, MaxDelay: time.Hour}

			stop := run(t, s)
			defer stop()

			c.advance(time.Second)
			eventually(t, "the job never ran", func() bool { return r.calls.Load() >= 1 })

			// Walk past every backoff the policy could ask for.
			for range 5 {
				c.advance(time.Hour)
				time.Sleep(10 * time.Millisecond)
			}
			assert.Equal(t, tc.wantCalls, int(r.calls.Load()))
		})
	}
}

// The delay grows and then stops growing: an exponential with no ceiling
// eventually schedules the retry after the next scheduled run, which is a job
// that quietly stops running.
func TestRetryPolicy_BacksOffAndCaps(t *testing.T) {
	p := scheduler.RetryPolicy{Attempts: 6, InitialDelay: time.Minute, MaxDelay: 10 * time.Minute}

	got := make([]time.Duration, 0, 5)
	for attempt := 1; attempt <= 5; attempt++ {
		got = append(got, p.Delay(attempt))
	}
	assert.Equal(t, []time.Duration{
		time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute,
	}, got)
}

// EF-104. A reload replaces the schedule; it does not touch what is running.
func TestReload_DoesNotDisturbARunningJob(t *testing.T) {
	defer goleak.VerifyNone(t)

	c := newClock(t, "2026-03-01T01:59:59Z")
	release := make(chan struct{})
	r := &recorder{release: release}

	s := newScheduler(t, c, r, scheduler.Job{SourceID: "prod", Spec: "* * * * *"})
	stop := run(t, s)
	var once sync.Once
	letGo := func() { once.Do(func() { close(release) }) }
	defer func() { letGo(); stop() }()

	c.advance(time.Second)
	eventually(t, "the job never started", func() bool { return len(r.runs()) == 1 })

	// The source disappears from the configuration while its backup is running.
	require.NoError(t, s.SetJobs([]scheduler.Job{{SourceID: "other", Spec: "@daily"}}))

	letGo()
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, []string{"prod"}, r.runs(),
		"a reload must let a running backup finish; killing it would waste the whole night's work")
}

// Cancelling the scheduler cancels what it is running. A backup left behind
// holds a connection, and for a physical backup a replication slot -- which
// makes the source retain WAL until its disk fills.
func TestRun_ShutdownCancelsRunningJobs(t *testing.T) {
	defer goleak.VerifyNone(t)

	c := newClock(t, "2026-03-01T01:59:59Z")
	r := &recorder{release: make(chan struct{})} // never closed

	s := newScheduler(t, c, r, scheduler.Job{SourceID: "prod", Spec: "* * * * *"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	c.advance(time.Second)
	eventually(t, "the job never started", func() bool { return len(r.runs()) == 1 })

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown waited on a job that will never finish on its own")
	}
}

// classedError carries an error class the way the pipeline's does.
type classedError struct{ class catalog.ErrorClass }

func (e *classedError) Error() string             { return "failed: " + string(e.class) }
func (e *classedError) Class() catalog.ErrorClass { return e.class }

var _ = errors.New

// The classifier is injected, and the one the CLI injects has to work on the
// errors a real backup produces. Without this, every failure looked
// unclassified and the policy retried a configuration mistake every minute --
// which the unit tests above could not see, because their double exposes a
// Class() method that nothing real implements.
func TestRun_UsesTheInjectedClassifier(t *testing.T) {
	defer goleak.VerifyNone(t)

	c := newClock(t, "2026-03-01T01:59:59Z")
	opaque := errors.New("something went wrong")
	r := &recorder{fail: func(int) error { return opaque }}

	s := newScheduler(t, c, r, scheduler.Job{SourceID: "prod", Spec: "0 2 * * *"})
	s.Retry = scheduler.RetryPolicy{Attempts: 3, InitialDelay: time.Minute, MaxDelay: time.Hour}
	s.Classify = func(error) catalog.ErrorClass { return catalog.ErrClassConfig }

	stop := run(t, s)
	defer stop()

	c.advance(time.Second)
	eventually(t, "the job never ran", func() bool { return r.calls.Load() >= 1 })
	for range 5 {
		c.advance(time.Hour)
		time.Sleep(10 * time.Millisecond)
	}
	assert.Equal(t, 1, int(r.calls.Load()),
		"an error the classifier calls a configuration mistake must not be retried, "+
			"however opaque the error itself is")
}
