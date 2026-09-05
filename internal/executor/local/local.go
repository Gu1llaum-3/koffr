// Package local runs commands on the machine Koffr itself runs on.
//
// This is the executor used when the database is reachable directly: pg_dump
// and pg_basebackup run here and pull over the network, so nothing is installed
// on the database host and nothing is written to its disk (PD-002, PD-003).
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"

	"github.com/Gu1llaum-3/koffr/internal/executor"
)

// Executor runs commands locally.
type Executor struct{}

// New returns a local executor.
func New() *Executor { return &Executor{} }

// Capabilities: a local executor can do everything, because there is no
// transport in the way.
func (e *Executor) Capabilities() executor.Capabilities {
	return executor.Capabilities{CanDial: true, CanExec: true, Target: "local"}
}

// Dial opens a connection from this machine.
func (e *Executor) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("dial %s/%s: %w", network, address, err)
	}
	return conn, nil
}

// Start launches a process.
//
// Stdout and stderr are plain OS pipes rather than exec's own, because Process
// has to be able to close the read ends itself. See process.Wait.
func (e *Executor) Start(ctx context.Context, cmd executor.Command) (executor.Process, error) {
	//nolint:gosec // running a configured backup binary is the entire purpose
	c := exec.CommandContext(ctx, cmd.Path, cmd.Args...)
	c.Env = cmd.Env
	c.Stdin = cmd.Stdin

	// Cancel is SIGKILL by default. That is what we want here: an orphaned
	// pg_basebackup holds a replication slot, and a held slot makes the source
	// retain WAL until its disk fills.
	c.Cancel = func() error { return c.Process.Kill() }

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe for %s: %w", cmd.Path, err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		closeAll(stdoutR, stdoutW)
		return nil, fmt.Errorf("stderr pipe for %s: %w", cmd.Path, err)
	}
	c.Stdout = stdoutW
	c.Stderr = stderrW

	if err := c.Start(); err != nil {
		closeAll(stdoutR, stdoutW, stderrR, stderrW)
		// Only the path is named. The arguments and the environment are not:
		// an error message is exactly where a credential passed the wrong way
		// becomes a leak in the logs (ENF-021).
		return nil, fmt.Errorf("start %s: %w", cmd.Path, err)
	}

	// The parent's copies of the write ends must go, or the read ends never see
	// EOF when the child exits.
	closeAll(stdoutW, stderrW)

	return &process{cmd: c, path: cmd.Path, stdout: stdoutR, stderr: stderrR}, nil
}

// Close is a no-op: a local executor holds nothing.
func (e *Executor) Close() error { return nil }

type process struct {
	cmd    *exec.Cmd
	path   string
	stdout *os.File
	stderr *os.File

	once sync.Once
	err  error
}

func (p *process) Stdout() io.Reader { return p.stdout }
func (p *process) Stderr() io.Reader { return p.stderr }

// Wait reaps the process.
//
// The read ends are closed first, on purpose. A child still writing to a pipe
// nobody reads blocks forever, so waiting on it would hang the caller; closing
// the read end gives the child EPIPE and lets it die. In the normal flow the
// caller has already read to EOF and this closes an exhausted pipe, which costs
// nothing.
//
// This is what makes the invariant in executor.Process hold: the pipeline
// abandons stdout when the storage branch fails, and must still be able to tear
// down and report that failure.
func (p *process) Wait() error {
	p.once.Do(func() {
		closeAll(p.stdout, p.stderr)

		err := p.cmd.Wait()
		if err == nil {
			return
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			p.err = &executor.ExitError{Code: exitErr.ExitCode(), Path: p.path}
			return
		}
		p.err = fmt.Errorf("wait for %s: %w", p.path, err)
	})
	return p.err
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

var _ executor.Executor = (*Executor)(nil)
