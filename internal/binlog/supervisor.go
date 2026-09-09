package binlog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/notify"
	"github.com/Gu1llaum-3/koffr/internal/source/mariadb"
)

// Supervisor keeps one source's binary log flowing into the repository.
//
// It owns two things and keeps them apart. The receiver -- mariadb-binlog in
// archiving mode -- writes files into the spool and is restarted when it dies.
// The archiver moves closed files from the spool to the repository and is what
// decides whether the receiver may run at all, through the spool's bounds. Its
// shape follows what Databasus arrived at for WAL (ADR-0007): only closed
// files travel, continuity is derived rather than trusted, and a receiver that
// dies quickly and often is a condition to report, not a process to relaunch.
type Supervisor struct {
	SourceID string
	Config   mariadb.Config
	// Tools runs mariadb-binlog; it is the Koffr host. Reach gets to the
	// server, which may be a tunnel.
	Tools executor.Executor
	Reach executor.Executor

	Spool    string
	Bounds   Bounds
	ServerID uint32
	// Rotate, when set, asks the server to close its current file this often
	// -- but only if it has grown since the last time, because rotating an
	// idle server archives a padded, empty file every interval for nothing.
	// Zero is off, and off is the default (decided 2026-09-08).
	Rotate time.Duration

	Archive *Archive
	Notify  func(notify.Event)
	Logf    func(string, ...any)
	Now     func() time.Time

	// Tick is how often the spool is scanned. Short in tests, seconds in
	// production.
	Tick time.Duration

	// Backoff and thresholds for the receiver's supervision.
	MinBackoff, MaxBackoff time.Duration
	// TransientUptime is how long a receiver has to have run for its death to
	// count as an accident rather than as a symptom. CrashLoopAfter is how many
	// consecutive short-lived runs end the supervisor with an error.
	TransientUptime time.Duration
	CrashLoopAfter  int

	mu             sync.Mutex
	lastGapEnd     uint64
	lastRotateAt   time.Time
	lastRotateFile string
	lastRotatePos  uint64
	lastRotateGTID string
}

// ErrCrashLoop says the receiver kept dying too quickly to be a network problem.
var ErrCrashLoop = errors.New("binlog: the receiver keeps dying at once; the cause is upstream and retrying will not fix it")

func (s *Supervisor) defaults() {
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.Logf == nil {
		s.Logf = func(string, ...any) {}
	}
	if s.Notify == nil {
		s.Notify = func(notify.Event) {}
	}
	if s.Tick == 0 {
		s.Tick = 5 * time.Second
	}
	if s.MinBackoff == 0 {
		s.MinBackoff = time.Second
	}
	if s.MaxBackoff == 0 {
		// Half a minute is neither hammering a dead host nor a wait anyone
		// notices on a healed one.
		s.MaxBackoff = 30 * time.Second
	}
	if s.TransientUptime == 0 {
		s.TransientUptime = time.Minute
	}
	if s.CrashLoopAfter == 0 {
		s.CrashLoopAfter = 5
	}
	s.Bounds = s.Bounds.WithDefaultLow()
}

// Run streams until the context ends or the receiver proves unrecoverable.
func (s *Supervisor) Run(ctx context.Context) error {
	s.defaults()
	if err := s.Bounds.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.Spool, 0o700); err != nil {
		return fmt.Errorf("binlog: create spool %s: %w", s.Spool, err)
	}

	gate := NewGate(s.Bounds)
	backoff := s.MinBackoff
	shortRuns := 0

	// A clean stop finishes what it trivially can. Files the receiver closed
	// just before the signal are complete and correct; leaving them for the
	// next start would be right too, but it makes a stop look like it lost
	// something. Bounded and detached from ctx, which is already done.
	defer func() { //nolint:contextcheck // ctx is done; the drain is meant to outlive it, and is bounded

		drain, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.archiveClosed(drain); err != nil {
			s.Logf("binlog: %s: final drain: %v", s.SourceID, err)
		}
	}()

	for {
		// Archive first, always: whatever the receiver is doing, a closed file
		// in the spool is a file the repository does not have yet.
		if err := s.archiveClosed(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.Logf("binlog: %s: archiving: %v", s.SourceID, err)
		}

		used, err := spoolBytes(s.Spool)
		if err != nil {
			return err
		}
		if !gate.Allow(used) {
			// The spool is full. The receiver stays down until the archiver has
			// drained it below the low mark; meanwhile the server keeps the
			// files, which is what its own retention is for.
			s.Logf("binlog: %s: spool holds %d bytes, receiver paused until it drains", s.SourceID, used)
			if err := sleep(ctx, s.Tick); err != nil {
				return err
			}
			continue
		}

		started := s.Now()
		err = s.runReceiverOnce(ctx, gate)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		uptime := s.Now().Sub(started)

		// Accident or symptom. A receiver that streamed for a while and then
		// died met a network that dropped or a server that restarted; that is
		// what the loop is for. One that died at once, several times running,
		// met a changed password or a revoked grant, and relaunching it every
		// second turns one alert into a night of them.
		switch {
		case uptime >= s.TransientUptime:
			shortRuns = 0
			backoff = s.MinBackoff
		case !s.reachable(ctx):
			// The receiver died at once because the server cannot be reached
			// at all -- a link that is down, a server that is restarting. That
			// is the accident this loop exists for, and a run that never got
			// past connecting is not weighed against the crash-loop count
			// (Databasus draws the same line for a bastion that is gone).
			s.Logf("binlog: %s: server unreachable, waiting %s", s.SourceID, backoff)
		default:
			shortRuns++
			if shortRuns >= s.CrashLoopAfter {
				s.Notify(notify.Event{
					Severity: notify.SeverityError, Kind: "binlog.crashloop",
					SourceID: s.SourceID, OccurredAt: s.Now(),
					Message: fmt.Sprintf("the binary log receiver for %s died %d times in a row within %s of starting; last error: %v",
						s.SourceID, shortRuns, s.TransientUptime, err),
				})
				return fmt.Errorf("%w: %w", ErrCrashLoop, err)
			}
		}
		if err != nil {
			s.Logf("binlog: %s: receiver exited: %v; restarting in %s", s.SourceID, err, backoff)
		}
		if err := sleep(ctx, backoff); err != nil {
			return err
		}
		backoff = min(backoff*2, s.MaxBackoff)
	}
}

// reachable tells a link that is down from a server that answered and refused.
//
// Only the second is a symptom. The distinction is made on the error, not on
// success: a server that replies "access denied" is reachable -- the receiver
// died because of something on the server, and that counts. A dial that times
// out or is refused at the TCP level is the network, and does not. Judging by
// success alone would have called a wrong password "unreachable" and relaunched
// the receiver for ever, which is what this first did.
func (s *Supervisor) reachable(ctx context.Context) bool {
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := s.Config.Binlog(probe, s.Reach)
	if err == nil {
		return true
	}
	var serverErr *mysql.MySQLError
	return errors.As(err, &serverErr)
}

// runReceiverOnce starts mariadb-binlog from the first file the archive lacks
// and tends the spool until the receiver exits or the gate closes.
func (s *Supervisor) runReceiverOnce(ctx context.Context, gate *Gate) error {
	bin, err := s.Config.ResolveBin(mariadb.BinBinlog)
	if err != nil {
		return err
	}
	start, err := s.startFile(ctx)
	if err != nil {
		return err
	}

	sess, err := s.Config.Open(ctx, s.Reach)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()

	// A partial file left by a previous receiver is not to be trusted: it is
	// asked for again from the server, which still has the closed original.
	if err := s.discardPartial(start); err != nil {
		return err
	}

	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	proc, err := s.Tools.Start(rctx, executor.Command{
		Path: bin,
		Args: s.Config.ReceiverArgs(sess, s.Spool, start.String(), s.ServerID),
		Env:  sess.Env(bin),
	})
	if err != nil {
		return fmt.Errorf("binlog: start %s: %w", mariadb.BinBinlog, err)
	}
	s.Logf("binlog: %s: receiving from %s", s.SourceID, start)

	// The receiver explains itself on stderr, and the explanation is what
	// turns "exited with status 1" into "could not find first log file name".
	// Wait for the copy to finish before reading it: the process can be
	// reaped before its last line has crossed the pipe, and the first real
	// purge produced eight restart lines that said nothing at all.
	tail := newTail()
	copied := make(chan struct{})
	go func() { defer close(copied); _, _ = io.Copy(tail, proc.Stderr()) }()
	go func() { _, _ = io.Copy(io.Discard, proc.Stdout()) }()

	exited := make(chan error, 1)
	go func() { exited <- proc.Wait() }()

	ticker := time.NewTicker(s.Tick)
	defer ticker.Stop()
	for {
		select {
		case err := <-exited:
			<-copied
			if msg := tail.String(); msg != "" && err != nil {
				return fmt.Errorf("%w: %s", err, msg)
			}
			return err
		case <-ctx.Done():
			cancel()
			<-exited
			return ctx.Err()
		case <-ticker.C:
			if err := s.archiveClosed(ctx); err != nil && ctx.Err() == nil {
				s.Logf("binlog: %s: archiving: %v", s.SourceID, err)
			}
			s.maybeRotate(ctx)
			used, err := spoolBytes(s.Spool)
			if err != nil {
				return err
			}
			if !gate.Allow(used) {
				// Stop the receiver rather than let the spool grow; the outer
				// loop restarts it once the archiver has caught up.
				cancel()
				<-exited
				return nil
			}
		}
	}
}

// startFile is where the receiver begins: the first file the archive lacks,
// or the oldest the server still has when nothing is archived yet.
func (s *Supervisor) startFile(ctx context.Context) (Name, error) {
	st, err := s.Config.Binlog(ctx, s.Reach)
	if err != nil {
		return Name{}, err
	}
	if !st.Enabled {
		return Name{}, errors.New("binlog: the server keeps no binary log (log_bin is off); nothing to archive")
	}
	if len(st.Files) == 0 {
		return Name{}, errors.New("binlog: the server lists no binary log files")
	}
	oldest, err := Parse(st.Files[0].Name)
	if err != nil {
		return Name{}, err
	}
	archived, err := s.Archive.Archived(ctx)
	if err != nil {
		return Name{}, err
	}
	from, ok := ResumeFrom(archived)
	if !ok {
		return oldest, nil
	}
	// The server may have purged the file the archive stops before -- an
	// outage longer than expire_logs_days, and the first run of this. Asking
	// for it is a refusal from the server on every start, which looks like a
	// crash loop and ends with the receiver stopped for good, when what was
	// lost is lost either way and everything the server still has is worth
	// archiving. So: say so, once and loudly (EF-063 will refuse any recovery
	// across the hole), and go on from what is there.
	if from.Base == oldest.Base && from.Seq < oldest.Seq {
		s.gap(Gap{Start: from, End: oldest},
			fmt.Sprintf("the server no longer has %s through %s: they were purged before they were archived; "+
				"a recovery across them is refused, and archiving goes on from %s. "+
				"The server's expire_logs_days is shorter than this outage was",
				from, oldest.Prev(), oldest))
		return oldest, nil
	}
	return from, nil
}

// archiveClosed moves every closed spool file to the repository, in order,
// and reports a hole in the sequence once.
func (s *Supervisor) archiveClosed(ctx context.Context) error {
	present, err := s.spoolNames()
	if err != nil {
		return err
	}
	closed, err := Closed(present)
	if err != nil {
		return err
	}
	for _, n := range closed {
		if _, err := s.Archive.Store(ctx, spoolName(s.Spool, n), n, s.Now()); err != nil {
			return err
		}
		if err := os.Remove(spoolName(s.Spool, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("binlog: remove %s from the spool: %w", n, err)
		}
		s.Logf("binlog: %s: archived %s", s.SourceID, n)
	}
	if len(closed) > 0 {
		s.checkContinuity(ctx)
	}
	return nil
}

// checkContinuity looks for holes and says so once per hole.
//
// Derived from the archive's names on every pass, never remembered as a fact:
// a remembered "intact" can be wrong. The notification is deduplicated so a
// backlog drained after an outage produces one message, not one per file.
// gap reports a hole once. Continuity is derived from the archive's names on
// every check, so the same hole comes back every time; the highest end seen
// is what tells a new hole from the old one.
func (s *Supervisor) gap(g Gap, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.End.Seq <= s.lastGapEnd {
		return
	}
	s.lastGapEnd = g.End.Seq
	s.Notify(notify.Event{
		Severity: notify.SeverityWarning, Kind: "binlog.gap", SourceID: s.SourceID,
		OccurredAt: s.Now(), Message: message,
	})
}

func (s *Supervisor) checkContinuity(ctx context.Context) {
	archived, err := s.Archive.Archived(ctx)
	if err != nil {
		return
	}
	gaps, err := Gaps(archived)
	if err != nil {
		s.Notify(notify.Event{
			Severity: notify.SeverityError, Kind: "binlog.gap", SourceID: s.SourceID,
			OccurredAt: s.Now(), Message: err.Error(),
		})
		return
	}
	for _, g := range gaps {
		s.gap(g, fmt.Sprintf("the binary log archive of %s is missing %s; a point-in-time recovery cannot cross it",
			s.SourceID, g))
	}
}

// maybeRotate asks the server to close its current file when rotation is on
// and something was written since the last rotation.
//
// "Something was written" is judged on the GTID position when the server has
// one, and on the (file, position) pair otherwise. Not on the position alone:
// a rotation opens a new file whose position starts near zero, and the server
// then appends its own bookkeeping to it -- a format description, a binlog
// checkpoint once the previous file's transactions are durable -- so the byte
// position moves on a server nobody wrote to. The first version of this read
// that as growth and rotated an idle server on every interval. Only a committed
// transaction advances the GTID, which is exactly the question being asked.
// The first pass only takes the baseline; nothing is rotated on the strength
// of never having looked.
func (s *Supervisor) maybeRotate(ctx context.Context) {
	if s.Rotate <= 0 {
		return
	}
	s.mu.Lock()
	first := s.lastRotateAt.IsZero()
	due := first || s.Now().Sub(s.lastRotateAt) >= s.Rotate
	s.mu.Unlock()
	if !due {
		return
	}
	st, err := s.Config.Binlog(ctx, s.Reach)
	if err != nil || !st.Enabled {
		return
	}
	s.mu.Lock()
	grew := !first && wrote(s.lastRotateFile, s.lastRotatePos, s.lastRotateGTID, st)
	s.lastRotateAt = s.Now()
	s.lastRotateFile, s.lastRotatePos, s.lastRotateGTID = st.File, st.Position, st.GTID
	s.mu.Unlock()
	if !grew {
		return
	}
	if err := s.Config.RotateBinlog(ctx, s.Reach); err != nil {
		s.Logf("binlog: %s: rotation: %v", s.SourceID, err)
		return
	}
	// The baseline is the state after the rotation, so that an idle server
	// compares equal next time.
	if after, err := s.Config.Binlog(ctx, s.Reach); err == nil {
		s.mu.Lock()
		s.lastRotateFile, s.lastRotatePos, s.lastRotateGTID = after.File, after.Position, after.GTID
		s.mu.Unlock()
	}
}

// wrote says whether a transaction was committed since the baseline.
func wrote(file string, pos uint64, gtid string, now mariadb.BinlogStatus) bool {
	if gtid != "" || now.GTID != "" {
		return now.GTID != gtid
	}
	return now.File != file || now.Position != pos
}

func (s *Supervisor) spoolNames() ([]Name, error) {
	entries, err := os.ReadDir(s.Spool)
	if err != nil {
		return nil, fmt.Errorf("binlog: read the spool: %w", err)
	}
	var names []Name
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n, err := Parse(e.Name())
		if err != nil {
			continue
		}
		names = append(names, n)
	}
	return names, nil
}

// discardPartial removes from the spool the file the receiver is about to
// rewrite, and anything after it: whatever is there is a partial download from
// a previous receiver, and mariadb-binlog would otherwise append to it.
func (s *Supervisor) discardPartial(from Name) error {
	present, err := s.spoolNames()
	if err != nil {
		return err
	}
	for _, n := range present {
		if n.Base == from.Base && n.Seq >= from.Seq {
			if err := os.Remove(spoolName(s.Spool, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("binlog: discard partial %s: %w", n, err)
			}
		}
	}
	return nil
}

func spoolBytes(dir string) (uint64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("binlog: read the spool: %w", err)
	}
	var total uint64
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && !e.IsDir() && info.Size() > 0 {
			total += uint64(info.Size()) //nolint:gosec // guarded: a size above zero fits
		}
	}
	return total, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// tail keeps the last few kilobytes of the receiver's stderr, where it explains
// itself.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

const tailLimit = 4 << 10

func newTail() *tail { return &tail{} }

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailLimit {
		t.buf = t.buf[len(t.buf)-tailLimit:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

// sortedSpool lists spool files oldest first, for tests and diagnostics.
func sortedSpool(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		out = append(out, filepath.Join(dir, e.Name()))
	}
	sort.Strings(out)
	return out, nil
}

var _ = sortedSpool
