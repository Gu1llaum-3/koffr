// Package executor abstracts reaching a target machine.
//
// Every remote operation in Koffr goes through this interface, and nothing
// above it knows how the target is reached. That is what keeps a future agent
// (EX-001) a purely additive change: an agent is one more implementation, not a
// modification of the sources or of the pipeline.
package executor

import (
	"context"
	"fmt"
	"io"
	"net"
)

// ExitError reports a process that ran and failed, as opposed to one that could
// not be started or was killed.
//
// The distinction drives ENF-011: a non-zero exit is the source's own failure
// and is retried with backoff, while a failure to start is a configuration
// problem and is not retried at all.
type ExitError struct {
	Code int
	// Path is the program that failed. Args are deliberately absent: an error
	// message is exactly where a credential passed the wrong way becomes a leak
	// in the logs (ENF-021).
	Path string
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("%s exited with status %d", e.Path, e.Code)
}

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
	// Stdout carries the backup stream. Stderr carries diagnostics, and for
	// pg_basebackup also the LSNs, so the two are never merged.
	Stdout() io.Reader
	Stderr() io.Reader

	// Wait reports the outcome, returning an *ExitError for a process that ran
	// and failed.
	//
	// It must return even when the caller has stopped reading Stdout. That is
	// not a nicety: when the storage branch fails, the pipeline abandons stdout
	// and tears down, and a Wait that blocked on an unread pipe would hang the
	// job instead of reporting the storage failure. Calling it twice is
	// allowed and reports the same outcome.
	Wait() error
}

// Deliberately absent: a Signal method.
//
// Nothing needs one yet -- the pipeline stops a process by cancelling its
// context. M2 will want a graceful stop for pg_receivewal, so that it finishes
// the segment in flight rather than losing it, and it will arrive then with a
// contract test of its own. An interface method no caller uses is a method no
// implementation is held to.

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

	// AddFactory says how to build an executor for a target, for the pull-mode
	// transports: local and SSH.
	AddFactory(targetID string, f Factory) error

	// Register installs an already-connected executor, as a listener accepting
	// inbound agent connections would.
	Register(targetID string, ex Executor) error

	// Close releases every executor handed out.
	Close() error
}
