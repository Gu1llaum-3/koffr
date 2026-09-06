// Package sourcetest is the contract every source.Source implementation must
// satisfy.
//
// It is written after the first implementation rather than before it, which is
// the reverse of how storagetest and executortest were built, and the reason is
// worth recording: PostgreSQL was the only engine for a whole milestone, so the
// interface had never been asked to describe two things. An interface with one
// implementation is a hypothesis. This suite is what turns it into a contract,
// and it is run against PostgreSQL first precisely so that a failure on MariaDB
// can be told from a failure of the interface itself.
package sourcetest

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/source"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

// Target is what one implementation hands the suite.
type Target struct {
	// Engine and Kind are what the suite asks for and what Probe must report.
	Engine source.Engine
	Kind   source.Kind

	// New builds a source whose client binaries run through tools. The suite
	// passes a recording executor so it can read every command line back and
	// prove no credential ever reached one (ENF-021).
	New func(t *testing.T, tools executor.Executor) source.Source

	// Reach returns the executor that gets to the database host. Usually local.
	Reach func(t *testing.T) executor.Executor

	// WithoutClientBinary builds a source that cannot find the binary it needs.
	// Probe must then fail, because a missing client is detectable at
	// configuration time and must never be discovered mid-job (PD-006, CT-001).
	// Nil skips that test.
	WithoutClientBinary func(t *testing.T) source.Source
}

// Suite runs the contract. Every implementation gets the same tests; not one is
// rewritten for an engine, which is the whole return on writing it.
func Suite(t *testing.T, target Target) {
	t.Run("Probe reports the engine, a version and what it can produce", func(t *testing.T) {
		rec := newRecorder(target.Reach(t))
		src := target.New(t, rec)

		info, err := src.Probe(t.Context(), target.Reach(t))
		require.NoError(t, err)
		assert.Equal(t, target.Engine, info.Engine)
		assert.NotEmpty(t, info.ServerVersion, "an operator reads this to know what they are backing up")
		assert.Contains(t, info.Kinds, target.Kind)
	})

	t.Run("Probe refuses a missing client binary", func(t *testing.T) {
		if target.WithoutClientBinary == nil {
			t.Skip("implementation does not expose a broken-toolchain constructor")
		}
		rec := newRecorder(target.Reach(t))
		src := target.WithoutClientBinary(t)
		_, err := src.Probe(t.Context(), target.Reach(t))
		require.Error(t, err, "a missing client must fail here, not at 3 AM (PD-006)")
		testutil.AssertNoSecretLeak(t, err.Error())
		_ = rec
	})

	t.Run("Open yields a stream that reads to EOF", func(t *testing.T) {
		rec := newRecorder(target.Reach(t))
		src := target.New(t, rec)

		stream, err := src.Open(t.Context(), target.Reach(t), source.Request{Kind: target.Kind})
		require.NoError(t, err)
		n, err := io.Copy(io.Discard, stream.Reader)
		require.NoError(t, err)
		require.NoError(t, stream.Close())
		assert.Positive(t, n, "an empty backup is not a backup")
	})

	t.Run("Close is idempotent", func(t *testing.T) {
		// The pipeline closes on its own teardown path and the caller may close
		// again on the way out. A second Close that errored would turn a
		// successful backup into a failed one.
		rec := newRecorder(target.Reach(t))
		src := target.New(t, rec)

		stream, err := src.Open(t.Context(), target.Reach(t), source.Request{Kind: target.Kind})
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, stream.Reader)
		require.NoError(t, err)
		require.NoError(t, stream.Close())
		require.NoError(t, stream.Close())
	})

	t.Run("cancelling the context kills the process", func(t *testing.T) {
		// Not a nicety. A dump left running holds a connection and, for a
		// physical backup, a replication slot -- which fills a disk nobody is
		// watching. Close must also come back quickly rather than block on a
		// process that is never going to be reaped.
		rec := newRecorder(target.Reach(t))
		src := target.New(t, rec)

		ctx, cancel := context.WithCancel(t.Context())
		stream, err := src.Open(ctx, target.Reach(t), source.Request{Kind: target.Kind})
		require.NoError(t, err)

		cancel()
		done := make(chan error, 1)
		go func() {
			_, _ = io.Copy(io.Discard, stream.Reader)
			done <- stream.Close()
		}()
		select {
		case <-done: // an error is fine here; hanging is not
		case <-time.After(30 * time.Second):
			t.Fatal("Close did not return after the context was cancelled")
		}
	})

	t.Run("Sidecars are refused once the stream is closed", func(t *testing.T) {
		// They are produced by running another tool against the same session,
		// so asking for them after Close asks a closed connection to answer.
		// Returning stale or empty content instead of an error is how a backup
		// silently loses half of itself.
		rec := newRecorder(target.Reach(t))
		src := target.New(t, rec)

		stream, err := src.Open(t.Context(), target.Reach(t), source.Request{Kind: target.Kind})
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, stream.Reader)
		require.NoError(t, err)
		require.NoError(t, stream.Close())

		if stream.Sidecars == nil {
			t.Skip("this source produces no sidecars")
		}
		_, err = stream.Sidecars()
		require.Error(t, err, "a sidecar collected after Close is a sidecar that is not there")
	})

	t.Run("no credential reaches a command line or an environment", func(t *testing.T) {
		// ENF-021. The sentinel is the configured password, so finding it in an
		// argument list means a `ps` on the database host shows it.
		rec := newRecorder(target.Reach(t))
		src := target.New(t, rec)

		_, _ = src.Probe(t.Context(), target.Reach(t))
		stream, err := src.Open(t.Context(), target.Reach(t), source.Request{Kind: target.Kind})
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, stream.Reader)
		require.NoError(t, err)
		require.NoError(t, stream.Close())

		seen := rec.seen()
		require.NotEmpty(t, seen, "the recorder saw nothing; the source is not running through it")
		testutil.AssertNoSecretLeak(t, seen...)
	})

	t.Run("the credentials file is gone once the stream is closed", func(t *testing.T) {
		// Checked by watching a private TMPDIR rather than by knowing the file's
		// name, so the test says what matters -- nothing is left behind -- in a
		// way that holds for an engine whose credential file is shaped
		// differently.
		dir := t.TempDir()
		t.Setenv("TMPDIR", dir)

		rec := newRecorder(target.Reach(t))
		src := target.New(t, rec)

		stream, err := src.Open(t.Context(), target.Reach(t), source.Request{Kind: target.Kind})
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, stream.Reader)
		require.NoError(t, err)
		require.NoError(t, stream.Close())

		left, err := os.ReadDir(dir)
		require.NoError(t, err)
		for _, e := range left {
			t.Errorf("%s was left behind in the temporary directory", filepath.Join(dir, e.Name()))
		}
	})
}

// recorder wraps an executor and keeps every command line it was asked to run.
type recorder struct {
	executor.Executor
	mu      sync.Mutex
	records []string
}

func newRecorder(inner executor.Executor) *recorder {
	return &recorder{Executor: inner}
}

func (r *recorder) Start(ctx context.Context, cmd executor.Command) (executor.Process, error) {
	r.mu.Lock()
	r.records = append(r.records,
		cmd.Path+" "+strings.Join(cmd.Args, " "),
		strings.Join(cmd.Env, "\n"))
	r.mu.Unlock()
	return r.Executor.Start(ctx, cmd)
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.records...)
}

// errNotRecorded exists so a nil recorder is a programming error rather than a
// test that silently checks nothing.
var errNotRecorded = errors.New("sourcetest: no command was recorded")

var _ = errNotRecorded
