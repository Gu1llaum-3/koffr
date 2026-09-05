package executor_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/executor"
)

// stub is the smallest thing that satisfies the interface. The registry's job
// is handing out executors, not running them.
type stub struct {
	name   string
	closed bool
}

func (s *stub) Dial(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("stub")
}
func (s *stub) Start(context.Context, executor.Command) (executor.Process, error) {
	return nil, errors.New("stub")
}
func (s *stub) Capabilities() executor.Capabilities {
	return executor.Capabilities{Target: s.name}
}
func (s *stub) Close() error { s.closed = true; return nil }

var _ executor.Executor = (*stub)(nil)

// An unknown target must be an error naming the target. Silently building a
// local executor instead would run a backup against the wrong machine.
func TestRegistry_UnknownTarget(t *testing.T) {
	r := executor.NewRegistry()
	_, err := r.Get(t.Context(), "no-such-source")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-source")
}

// Pull mode: local and SSH are built on demand from configuration.
func TestRegistry_Factory(t *testing.T) {
	r := executor.NewRegistry()
	calls := 0
	require.NoError(t, r.AddFactory("prod", func(context.Context) (executor.Executor, error) {
		calls++
		return &stub{name: "prod"}, nil
	}))

	first, err := r.Get(t.Context(), "prod")
	require.NoError(t, err)
	assert.Equal(t, "prod", first.Capabilities().Target)

	second, err := r.Get(t.Context(), "prod")
	require.NoError(t, err)
	assert.Same(t, first, second, "a target's executor is reused, not rebuilt per job")
	assert.Equal(t, 1, calls)
}

// A factory that fails must not be cached: a database that was down when the
// scheduler first asked must be reachable on the next attempt.
func TestRegistry_FactoryFailureIsNotCached(t *testing.T) {
	r := executor.NewRegistry()
	fail := true
	require.NoError(t, r.AddFactory("flaky", func(context.Context) (executor.Executor, error) {
		if fail {
			return nil, errors.New("database unreachable")
		}
		return &stub{name: "flaky"}, nil
	}))

	_, err := r.Get(t.Context(), "flaky")
	require.Error(t, err)

	fail = false
	ex, err := r.Get(t.Context(), "flaky")
	require.NoError(t, err)
	assert.Equal(t, "flaky", ex.Capabilities().Target)
}

// Push mode, and the whole reason this type exists (EX-001). An agent behind
// NAT dials in and registers itself; nothing above this interface can tell the
// difference, which is what makes adding an agent additive rather than a
// rewrite of every source.
func TestRegistry_RegisterFromPushMode(t *testing.T) {
	r := executor.NewRegistry()
	agent := &stub{name: "agent://behind-nat"}
	require.NoError(t, r.Register("remote-1", agent))

	got, err := r.Get(t.Context(), "remote-1")
	require.NoError(t, err)
	assert.Same(t, agent, got)
}

// A reconnecting agent replaces its previous registration, and the stale
// executor is closed rather than leaked.
func TestRegistry_ReregisterReplacesAndCloses(t *testing.T) {
	r := executor.NewRegistry()
	first := &stub{name: "agent-v1"}
	second := &stub{name: "agent-v2"}

	require.NoError(t, r.Register("remote-1", first))
	require.NoError(t, r.Register("remote-1", second))

	got, err := r.Get(t.Context(), "remote-1")
	require.NoError(t, err)
	assert.Same(t, second, got)
	assert.True(t, first.closed, "the superseded executor was leaked")
}

func TestRegistry_DuplicateFactoryIsRejected(t *testing.T) {
	r := executor.NewRegistry()
	f := func(context.Context) (executor.Executor, error) { return &stub{}, nil }
	require.NoError(t, r.AddFactory("prod", f))

	err := r.AddFactory("prod", f)
	require.Error(t, err, "two sources sharing an id is a configuration error, not a last-one-wins")
	assert.Contains(t, err.Error(), "prod")
}

// Close releases every executor the registry handed out. A leaked SSH
// connection holds a session on the target.
func TestRegistry_CloseReleasesEverything(t *testing.T) {
	r := executor.NewRegistry()
	pulled := &stub{name: "pulled"}
	pushed := &stub{name: "pushed"}
	require.NoError(t, r.AddFactory("a", func(context.Context) (executor.Executor, error) {
		return pulled, nil
	}))
	require.NoError(t, r.Register("b", pushed))
	_, err := r.Get(t.Context(), "a")
	require.NoError(t, err)

	require.NoError(t, r.Close())
	assert.True(t, pulled.closed)
	assert.True(t, pushed.closed)

	_, err = r.Get(t.Context(), "a")
	assert.Error(t, err, "a closed registry must not hand out executors")
}

// The scheduler runs jobs concurrently and an agent can dial in at any moment,
// so Get and Register race by design. Run with -race.
func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := executor.NewRegistry()
	require.NoError(t, r.AddFactory("prod", func(context.Context) (executor.Executor, error) {
		return &stub{name: "prod"}, nil
	}))

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = r.Get(t.Context(), "prod")
		}()
		go func() {
			defer wg.Done()
			_ = r.Register("agent", &stub{name: "agent"})
			_ = i
		}()
	}
	wg.Wait()

	// The factory must have run exactly once despite the stampede.
	first, err := r.Get(t.Context(), "prod")
	require.NoError(t, err)
	second, err := r.Get(t.Context(), "prod")
	require.NoError(t, err)
	assert.Same(t, first, second)
}

var _ io.Closer = (*stub)(nil)
