package executor

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Factory builds an executor for one target, on demand.
//
// Local and SSH executors are built this way: the configuration says how to
// reach a source, and the connection is opened the first time a job needs it.
type Factory func(ctx context.Context) (Executor, error)

// registry is the default Registry.
//
// It is the insertion point for EX-001. A pull-mode executor is built from a
// Factory; a push-mode one -- an agent behind NAT that dials in -- registers
// itself. Callers cannot tell which they got, which is what makes adding an
// agent an addition rather than a change to every source.
type registry struct {
	mu        sync.Mutex
	factories map[string]Factory
	executors map[string]Executor
	closed    bool
}

// NewRegistry returns an empty registry.
func NewRegistry() Registry {
	return &registry{
		factories: make(map[string]Factory),
		executors: make(map[string]Executor),
	}
}

// ErrRegistryClosed is returned once Close has run.
var ErrRegistryClosed = errors.New("executor: registry is closed")

// AddFactory registers how to build an executor for a target.
//
// A duplicate identifier is refused rather than overwritten: two sources
// sharing an id is a configuration mistake, and last-one-wins would send a
// backup to whichever definition happened to load second.
func (r *registry) AddFactory(targetID string, f Factory) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrRegistryClosed
	}
	if _, exists := r.factories[targetID]; exists {
		return fmt.Errorf("executor: target %q is already configured", targetID)
	}
	r.factories[targetID] = f
	return nil
}

// Register installs an already-connected executor, as an inbound agent does.
//
// Re-registering replaces the previous one and closes it: an agent that
// reconnects would otherwise leave its old connection behind.
func (r *registry) Register(targetID string, ex Executor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrRegistryClosed
	}
	if previous, exists := r.executors[targetID]; exists && previous != ex {
		_ = previous.Close()
	}
	r.executors[targetID] = ex
	return nil
}

// Get returns the executor for a target, building it if a factory was
// registered for it.
//
// A failed build is not remembered: a database that was down when the scheduler
// first asked must be reachable on the next attempt.
func (r *registry) Get(ctx context.Context, targetID string) (Executor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrRegistryClosed
	}
	if ex, ok := r.executors[targetID]; ok {
		return ex, nil
	}
	f, ok := r.factories[targetID]
	if !ok {
		return nil, fmt.Errorf("executor: no target named %q is configured", targetID)
	}

	// The lock is held across the build. That serialises a stampede of
	// concurrent jobs onto one connection attempt, which is the point: without
	// it, ten scheduled jobs would open ten SSH connections to the same host.
	ex, err := f(ctx)
	if err != nil {
		return nil, fmt.Errorf("executor: connect to target %q: %w", targetID, err)
	}
	r.executors[targetID] = ex
	return ex, nil
}

// Close releases every executor handed out. A leaked SSH connection holds a
// session on the target.
func (r *registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true

	var errs []error
	for id, ex := range r.executors {
		if err := ex.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close target %q: %w", id, err))
		}
	}
	clear(r.executors)
	return errors.Join(errs...)
}
