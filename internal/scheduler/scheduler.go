// Package scheduler runs backup jobs on a timetable.
//
// It is what turns Koffr from a tool someone runs into a system that runs, and
// it is where PD-007 becomes true: a job that did not happen has to be
// something Koffr notices, because silence is otherwise indistinguishable from
// success.
//
// Cron parsing is borrowed; the loop is not. The library answers "when is the
// next 2 AM" -- a question with real edge cases around daylight saving -- and
// this package decides what to do at that moment, taking its time from an
// injected clock so that a test about tomorrow night does not have to wait for
// it (EF-090).
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/source"
)

// Job is one piece of work on a timetable.
type Job struct {
	SourceID    string
	Destination string
	Kind        source.Kind

	// Spec is cron, or one of the shortcuts: @hourly, @daily, @weekly,
	// @monthly, @yearly, @every <duration>.
	Spec string
}

// RetryPolicy is EF-094.
type RetryPolicy struct {
	// Attempts counts the first try. Zero and one both mean "no retry".
	Attempts     int
	InitialDelay time.Duration
	MaxDelay     time.Duration
}

// Delay is the wait before attempt n+1, doubling and then holding.
//
// The ceiling is not decoration. An exponential with no cap eventually puts the
// retry after the next scheduled run, and a job that keeps rescheduling itself
// past its own window is a job that has quietly stopped running.
func (p RetryPolicy) Delay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := p.InitialDelay
	for range attempt - 1 {
		d *= 2
		if p.MaxDelay > 0 && d >= p.MaxDelay {
			return p.MaxDelay
		}
	}
	if p.MaxDelay > 0 && d > p.MaxDelay {
		return p.MaxDelay
	}
	return d
}

// Scheduler fires jobs and keeps them from treading on each other.
type Scheduler struct {
	// Execute does the work. Injected so this package holds timing and nothing
	// else: what a backup is belongs to internal/backup.
	Execute func(ctx context.Context, job Job) error

	// Now and Tick are the clock. Tick is how often the timetable is consulted,
	// not how precise it is: a job due at 02:00:00 fires within one tick of it.
	Now  func() time.Time
	Tick time.Duration

	// Location is the timezone the specs are read in (EF-090). Nil means UTC,
	// which is the only choice that does not move twice a year.
	Location *time.Location

	// MaxConcurrent caps how many jobs run at once (EF-093). Zero means no cap.
	MaxConcurrent int

	Retry RetryPolicy

	// Classify says how an error should be treated. Injected because the class
	// lives on a struct field in internal/pipeline and a field cannot also be a
	// method -- and because a scheduler that had to import the pipeline to
	// decide whether to wait five minutes would be the wrong shape.
	//
	// Left nil, only errors that expose a Class() method are classified, which
	// in practice means nothing: the retry policy then treats every failure as
	// worth another go. That default is safe and useless, so the CLI wires the
	// real one.
	Classify func(error) catalog.ErrorClass

	// OnSkip is called when a window is passed over. A skip has to be visible:
	// it is the difference between "the backup ran" and "the backup would have
	// run if the previous one had finished" (EF-092).
	OnSkip func(job Job, why string)

	// OnResult reports every attempt's outcome, for the notifier to pick up.
	OnResult func(job Job, attempt int, err error)

	mu      sync.Mutex
	entries []*entry
	running map[string]bool
}

// entry is a job with its parsed schedule and its next due time.
type entry struct {
	job      Job
	schedule cron.Schedule
	// next is fixed when the timetable is set, not at the first tick.
	//
	// Lazily was the first attempt and it was wrong: between SetJobs and the
	// first tick the clock moves, so a job set at 01:59 and first ticked at
	// 02:00 computed its next window as *tomorrow* and slept through tonight.
	// The bug only appeared when the tick lost the race, which is to say
	// sometimes.
	next time.Time
	// retryAt is when a failed attempt should be tried again, ignored when
	// zero.
	retryAt time.Time
	attempt int
}

var parser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// SetJobs replaces the timetable.
//
// It is also the reload path (EF-104), and it deliberately says nothing about
// what is running: a backup halfway through is work already paid for, and
// killing it because a configuration file changed would throw away a night.
func (s *Scheduler) SetJobs(jobs []Job) error {
	now := s.now()
	entries := make([]*entry, 0, len(jobs))
	for _, j := range jobs {
		schedule, err := parser.Parse(j.Spec)
		if err != nil {
			return fmt.Errorf("scheduler: source %s has schedule %q, which is not a schedule: %w",
				j.SourceID, j.Spec, err)
		}
		entries = append(entries, &entry{
			job:      j,
			schedule: schedule,
			next:     schedule.Next(now),
		})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = entries
	return nil
}

// Run consults the timetable until the context ends.
//
// It returns the context's error, and only once every job it started has
// stopped. Returning earlier would leave a pg_dump running with nobody waiting
// on it -- and for a physical backup, a replication slot held open, which makes
// the source retain WAL until its disk fills.
func (s *Scheduler) Run(ctx context.Context) error {
	if s.Execute == nil {
		return errors.New("scheduler: nothing to execute")
	}

	tick := s.Tick
	if tick <= 0 {
		tick = time.Second
	}

	var wg sync.WaitGroup
	defer wg.Wait()

	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			for _, due := range s.due() {
				s.start(ctx, &wg, due)
			}
		}
	}
}

// due advances the timetable and returns what should start now.
func (s *Scheduler) due() []*entry {
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	var ready []*entry
	for _, e := range s.entries {
		switch {
		case !e.retryAt.IsZero():
			if now.Before(e.retryAt) {
				continue
			}
			e.retryAt = time.Time{}
		case now.Before(e.next):
			continue
		default:
			// A window has arrived. Move to the next one now, whether or not
			// this one is taken: that is what makes an overrun a skip rather
			// than a queue (EF-092).
			e.next = e.schedule.Next(now)
			e.attempt = 0
		}

		if s.running[e.job.SourceID] {
			// EF-045 would refuse this in the repository anyway, but finding
			// out by taking a lock and failing is a worse way to learn it.
			s.skip(e.job, "the previous run has not finished")
			continue
		}
		ready = append(ready, e)
	}
	return ready
}

func (s *Scheduler) start(ctx context.Context, wg *sync.WaitGroup, e *entry) {
	s.mu.Lock()
	if s.MaxConcurrent > 0 && len(s.running) >= s.MaxConcurrent {
		s.mu.Unlock()
		s.skip(e.job, fmt.Sprintf("%d jobs already running", s.MaxConcurrent))
		return
	}
	if s.running == nil {
		s.running = map[string]bool{}
	}
	s.running[e.job.SourceID] = true
	e.attempt++
	attempt := e.attempt
	s.mu.Unlock()

	wg.Add(1)
	go func() {
		defer wg.Done()
		err := s.Execute(ctx, e.job)

		s.mu.Lock()
		delete(s.running, e.job.SourceID)
		if err != nil && s.shouldRetry(err, attempt) {
			e.retryAt = s.now().Add(s.Retry.Delay(attempt))
		} else {
			e.retryAt = time.Time{}
		}
		s.mu.Unlock()

		if s.OnResult != nil {
			s.OnResult(e.job, attempt, err)
		}
	}()
}

// shouldRetry asks the error class, not the message (ENF-011).
//
// A configuration mistake fails identically every time, so retrying it every
// minute produces a week of identical alerts and teaches an operator to ignore
// them. A storage timeout is worth another go.
func (s *Scheduler) shouldRetry(err error, attempt int) bool {
	if attempt >= s.Retry.Attempts {
		return false
	}
	switch s.classOf(err) {
	case catalog.ErrClassConfig, catalog.ErrClassCrypto, catalog.ErrClassCanceled:
		return false
	case catalog.ErrClassStalled:
		// Once, then it is a real problem rather than a slow night (EF-095).
		return attempt < 2
	default:
		return true
	}
}

// classified is any error that knows how it should be treated.
type classified interface{ Class() catalog.ErrorClass }

func (s *Scheduler) classOf(err error) catalog.ErrorClass {
	if errors.Is(err, context.Canceled) {
		return catalog.ErrClassCanceled
	}
	if s.Classify != nil {
		return s.Classify(err)
	}
	var c classified
	if errors.As(err, &c) {
		return c.Class()
	}
	// Unclassified means unknown, and retrying an unknown failure is safer than
	// declaring it permanent.
	return catalog.ErrClassSource
}

func (s *Scheduler) skip(job Job, why string) {
	if s.OnSkip != nil {
		s.OnSkip(job, why)
	}
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	if s.Location != nil {
		return time.Now().In(s.Location)
	}
	return time.Now().UTC()
}

// ValidateSpec reports whether a schedule is one Koffr understands.
//
// Exported so the configuration can refuse a bad one at load time. A schedule
// that does not parse is a source that silently never runs, and finding that
// out is worth more than a tidy dependency graph (PD-006).
func ValidateSpec(spec string) error {
	_, err := parser.Parse(spec)
	return err
}

// NextRun answers "when would this run next", for `koffr schedule --dry-run`.
//
// An operator reading "@daily" cannot tell whether that is midnight UTC or
// midnight local, and the answer decides whether a backup lands during business
// hours.
func NextRun(spec string, after time.Time) (time.Time, error) {
	schedule, err := parser.Parse(spec)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: %q is not a schedule: %w", spec, err)
	}
	return schedule.Next(after), nil
}
