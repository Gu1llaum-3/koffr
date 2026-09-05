// Package executor abstracts reaching a target machine.
//
// Every remote operation in Koffr goes through this interface, and nothing
// above it knows how the target is reached. That is what keeps a future agent
// (EX-001) a purely additive change: an agent is one more implementation, not a
// modification of the sources or of the pipeline.
package executor

import (
	"context"
	"io"
	"net"
	"os"
)

// Executor reaches a target machine. Implementations: local, SSH, and later a
// reverse-connected agent.
type Executor interface {
	// Dial opens a network connection from the target's point of view. For the
	// local executor this is a plain net.Dial; for SSH it is a direct-tcpip
	// channel; for an agent it is a multiplexed stream over the existing link.
	Dial(ctx context.Context, network, address string) (net.Conn, error)

	// Start launches a process on the target. It does not return until the
	// process has actually started, so a failure to spawn is distinguishable
	// from a failure during execution.
	Start(ctx context.Context, cmd Command) (Process, error)

	// Capabilities reports what this executor can actually do. A tunnel-only
	// executor reports CanExec false, which is how a MariaDB physical backup
	// gets rejected at configuration load rather than at 3 AM (PD-006).
	Capabilities() Capabilities

	io.Closer
}

// Command describes a process to run on the target.
//
// Args never carries a secret: credentials travel through Env, or through the
// path of a temporary 0600 file (ENF-021). The struct holds no handles and no
// closures, so it can be serialised as-is over a future agent transport.
type Command struct {
	Path  string
	Args  []string
	Env   []string
	Stdin io.Reader
}

// Process is a running command on the target.
type Process interface {
	Stdout() io.Reader
	Stderr() io.Reader

	// Wait returns the process outcome. It must be safe to call after the
	// caller has stopped reading Stdout, so teardown never deadlocks.
	Wait() error

	Signal(sig os.Signal) error
}

// Capabilities reports what an executor supports.
type Capabilities struct {
	CanDial bool
	CanExec bool

	// Target is human-readable and for diagnostics only. It must never be
	// parsed, and must never contain a credential.
	Target string
}

// Registry hands out executors by target id.
//
// A pull-mode executor (local, SSH) is built on demand; a push-mode one (an
// agent dialling in) registers itself when it connects. Callers cannot tell the
// difference, which is the whole point: this is the insertion point for EX-001.
type Registry interface {
	Get(ctx context.Context, targetID string) (Executor, error)

	// Register is used by a listener accepting inbound agent connections.
	Register(targetID string, ex Executor) error
}
