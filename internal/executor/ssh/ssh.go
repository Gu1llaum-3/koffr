// Package ssh reaches a machine over SSH.
//
// Two capabilities, and the difference matters (CT-002):
//
//   - Dial forwards a TCP connection from the target's point of view. That is
//     enough for everything PostgreSQL needs and for MariaDB logical backups
//     and binlog streaming, none of which require anything on the host.
//   - Start runs a command there. Only MariaDB physical backup needs it, because
//     mariabackup reads the data directory directly, so it is off by default:
//     an executor that can open a tunnel should not silently also be able to run
//     arbitrary commands.
package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Gu1llaum-3/koffr/internal/executor"
)

// Config describes one SSH target.
type Config struct {
	// Address is host:port.
	Address string
	User    string

	// Password and PrivateKey are alternatives. PrivateKey wins when both are
	// set, so a key left in the configuration is never silently ignored.
	Password              string
	PrivateKey            []byte
	PrivateKeyPassword    []byte
	KnownHostsFile        string
	InsecureIgnoreHostKey bool

	// AllowExec opts into running commands. Left false, this executor is a
	// tunnel and says so through Capabilities.
	AllowExec bool

	Timeout time.Duration
}

const defaultTimeout = 30 * time.Second

// reapGrace is how long Wait lets a session finish on its own before tearing it
// down. See process.Wait.
const reapGrace = 2 * time.Second

// Executor is one SSH connection, shared by every tunnel and command that uses
// this target.
type Executor struct {
	client *ssh.Client
	cfg    Config

	closeOnce sync.Once
	closeErr  error
}

// Dial opens the SSH connection.
func Dial(ctx context.Context, cfg Config) (*Executor, error) {
	if cfg.Address == "" {
		return nil, errors.New("executor/ssh: no address configured")
	}
	if cfg.User == "" {
		return nil, errors.New("executor/ssh: no user configured")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}

	auth, err := authMethods(cfg)
	if err != nil {
		return nil, err
	}
	hostKey, err := hostKeyCallback(cfg)
	if err != nil {
		return nil, err
	}

	// The TCP dial honours the context; the SSH handshake that follows honours
	// only the timeout, which is why both are set.
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", cfg.Address, err)
	}
	if err := conn.SetDeadline(time.Now().Add(cfg.Timeout)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("set handshake deadline for %s: %w", cfg.Address, err)
	}

	c, chans, reqs, err := ssh.NewClientConn(conn, cfg.Address, &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auth,
		HostKeyCallback: hostKey,
		Timeout:         cfg.Timeout,
	})
	if err != nil {
		_ = conn.Close()
		// Neither the password nor the key is named: an authentication failure
		// is exactly where a credential would end up in the logs (ENF-021).
		return nil, fmt.Errorf("ssh handshake with %s as %s: %w", cfg.Address, cfg.User, err)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("clear handshake deadline for %s: %w", cfg.Address, err)
	}

	return &Executor{client: ssh.NewClient(c, chans, reqs), cfg: cfg}, nil
}

func authMethods(cfg Config) ([]ssh.AuthMethod, error) {
	switch {
	case len(cfg.PrivateKey) > 0:
		var signer ssh.Signer
		var err error
		if len(cfg.PrivateKeyPassword) > 0 {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(cfg.PrivateKey, cfg.PrivateKeyPassword)
		} else {
			signer, err = ssh.ParsePrivateKey(cfg.PrivateKey)
		}
		if err != nil {
			// The key itself is never echoed.
			return nil, fmt.Errorf("executor/ssh: private key could not be parsed: %w", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	case cfg.Password != "":
		return []ssh.AuthMethod{ssh.Password(cfg.Password)}, nil
	default:
		return nil, errors.New("executor/ssh: no private key and no password configured")
	}
}

// hostKeyCallback implements EF-004: verification is on unless it is turned off
// explicitly, and turning it off requires saying so in the configuration rather
// than happening because a known_hosts file was missing.
func hostKeyCallback(cfg Config) (ssh.HostKeyCallback, error) {
	if cfg.InsecureIgnoreHostKey {
		return ssh.InsecureIgnoreHostKey(), nil //nolint:gosec // opted into explicitly
	}
	path := cfg.KnownHostsFile
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("executor/ssh: no known_hosts file configured and no home directory: %w", err)
		}
		path = home + "/.ssh/known_hosts"
	}
	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf(
			"executor/ssh: cannot read known_hosts at %s: %w; "+
				"point known_hosts_file at the right file, or set insecure_ignore_host_key "+
				"if you accept an unauthenticated connection", path, err)
	}
	return cb, nil
}

// Capabilities reports what this target allows.
func (e *Executor) Capabilities() executor.Capabilities {
	return executor.Capabilities{
		CanDial: true,
		CanExec: e.cfg.AllowExec,
		// User and address only: a target string is written to logs and to the
		// UI, and must never carry a credential.
		Target: "ssh://" + e.cfg.User + "@" + e.cfg.Address,
	}
}

// Dial forwards a TCP connection from the target's point of view.
//
// This is what reaches a database that publishes no port: the connection is
// opened by the SSH server, so the database only ever sees a local client.
func (e *Executor) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := e.client.DialContext(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("dial %s/%s through %s: %w", network, address, e.cfg.Address, err)
	}
	// Wrapped because an SSH channel has no deadlines and pgx sets them. See
	// tunnelConn.
	return newTunnelConn(conn), nil
}

// Start runs a command on the target.
func (e *Executor) Start(ctx context.Context, cmd executor.Command) (executor.Process, error) {
	if !e.cfg.AllowExec {
		return nil, fmt.Errorf(
			"executor/ssh: running commands on %s is not enabled; "+
				"set allow_exec on this source if it needs MariaDB physical backup", e.cfg.Address)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	session, err := e.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("open session on %s: %w", e.cfg.Address, err)
	}

	// The environment goes through the SSH protocol, never through the command
	// line. A value in argv is world-readable in /proc/<pid>/cmdline on the
	// target; a value in the environment is not (ENF-021). If the server
	// refuses, that is a loud failure rather than a silent downgrade.
	for _, kv := range cmd.Env {
		name, value, found := strings.Cut(kv, "=")
		if !found {
			_ = session.Close()
			return nil, fmt.Errorf("executor/ssh: malformed environment entry %q", name)
		}
		if err := session.Setenv(name, value); err != nil {
			_ = session.Close()
			return nil, fmt.Errorf(
				"executor/ssh: %s refused to accept the environment variable %s: %w; "+
					"add it to AcceptEnv in the target's sshd_config. Koffr will not fall back to "+
					"putting it on the command line, where it would be world-readable", e.cfg.Address, name, err)
		}
	}

	session.Stdin = cmd.Stdin

	// Plain OS pipes rather than session.StdoutPipe, because Process.Wait has to
	// be able to close the read ends itself. See process.Wait.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("stdout pipe for %s: %w", cmd.Path, err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = session.Close()
		closeAll(stdoutR, stdoutW)
		return nil, fmt.Errorf("stderr pipe for %s: %w", cmd.Path, err)
	}
	session.Stdout = stdoutW
	session.Stderr = stderrW

	line := commandLine(cmd)
	if err := session.Start(line); err != nil {
		_ = session.Close()
		closeAll(stdoutR, stdoutW, stderrR, stderrW)
		// The path only. The command line is not echoed.
		return nil, fmt.Errorf("start %s on %s: %w", cmd.Path, e.cfg.Address, err)
	}

	p := &process{
		session: session,
		path:    cmd.Path,
		stdout:  stdoutR,
		stderr:  stderrR,
		reaped:  make(chan struct{}),
		ctx:     ctx,
	}

	// Unlike a local child process, nothing here holds the other end of the
	// pipes: the library copies the channel into them from its own goroutine
	// and closes nothing it did not open. Without this reaper, a caller reading
	// Stdout would wait for an EOF that never comes.
	//
	// ssh.Session.Wait returns only once those copies have finished, so closing
	// the write ends after it is what turns the end of the remote command into
	// an EOF on Stdout.
	go func() {
		p.sessionErr = session.Wait()
		closeAll(stdoutW, stderrW)
		close(p.reaped)
	}()

	// An exec channel has no signal delivery worth relying on, so cancellation
	// closes the session. The remote process gets EOF on stdin and a closed
	// channel, and the server reaps it.
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close()
		case <-p.reaped:
		}
	}()

	return p, nil
}

// Close tears down the connection. Sessions opened from it die with it.
func (e *Executor) Close() error {
	e.closeOnce.Do(func() {
		e.closeErr = e.client.Close()
		if errors.Is(e.closeErr, net.ErrClosed) {
			e.closeErr = nil
		}
	})
	return e.closeErr
}

// commandLine turns argv into something a remote shell will take apart the same
// way.
//
// An exec channel carries a string, not an argv, so every argument is quoted.
// This is not cosmetic: pg_dump takes --exclude-table='some table', and an
// argument that loses its quoting either dumps the wrong relations or runs a
// second command entirely.
func commandLine(cmd executor.Command) string {
	parts := make([]string, 0, len(cmd.Args)+1)
	parts = append(parts, shellQuote(cmd.Path))
	for _, a := range cmd.Args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// shellQuote wraps a string in single quotes, which a POSIX shell treats
// entirely literally. The only character needing care is the single quote
// itself, which is escaped by leaving the quoted run and re-entering it.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type process struct {
	session *ssh.Session
	path    string
	stdout  *os.File
	stderr  *os.File

	// reaped is closed once session.Wait has returned and the write ends are
	// closed. sessionErr is only read after it closes.
	reaped     chan struct{}
	sessionErr error

	// ctx is the context the command was started with. Wait watches it so that
	// a cancelled job stops waiting even when the far side does not cooperate.
	ctx context.Context

	once sync.Once
	err  error
}

func (p *process) Stdout() io.Reader { return p.stdout }
func (p *process) Stderr() io.Reader { return p.stderr }

// Wait reaps the remote command.
//
// The read ends are closed first for the same reason as in the local executor:
// the library copies the channel into our pipes from its own goroutine, and a
// pipe nobody drains would block that goroutine and with it Wait. Closing the
// read end makes the copy fail instead, which is the right outcome on the
// teardown path this exists for.
func (p *process) Wait() error {
	p.once.Do(func() {
		// Closing the read ends unblocks our own copy goroutine, but over SSH
		// that is not enough on its own. A remote command whose output nobody
		// consumes is blocked on a channel window that will now never be
		// extended, so it never exits and never sends an exit status, and
		// session.Wait would sit there forever waiting for one.
		//
		// So: close the read ends, give the session a moment to finish on its
		// own, and tear it down if it does not. In the normal flow the caller
		// has already read to EOF and this returns immediately; the grace only
		// elapses for a session that is genuinely stuck, where the exit status
		// was never coming and the storage error that triggered the teardown is
		// the one worth reporting anyway.
		closeAll(p.stdout, p.stderr)

		select {
		case <-p.reaped:
		case <-time.After(reapGrace):
			_ = p.session.Close()
		case <-p.ctx.Done():
			_ = p.session.Close()
		}

		// Second chance after the teardown, then give up waiting.
		//
		// Closing the session is the strongest thing this transport offers: an
		// exec channel has no dependable signal delivery, and sshd decides for
		// itself when to reap the command. Blocking here until it does would
		// turn an uncooperative target into a hung job, which is the failure
		// this whole invariant exists to prevent. Executor.Close drops the
		// connection, and every session with it.
		select {
		case <-p.reaped:
		case <-time.After(reapGrace):
			p.err = fmt.Errorf("%s did not report an exit status after the session was torn down", p.path)
			return
		}
		_ = p.session.Close()

		switch {
		case p.ctx.Err() != nil:
			p.err = fmt.Errorf("wait for %s: %w", p.path, p.ctx.Err())
		case p.sessionErr == nil:
		case isExit(p.sessionErr, &p.err, p.path):
		default:
			p.err = fmt.Errorf("wait for %s: %w", p.path, p.sessionErr)
		}
	})
	return p.err
}

// isExit maps an SSH exit status onto the shared ExitError, and reports whether
// it did. A command killed by a signal, or a session torn down by cancellation,
// is a failure but not an exit status.
func isExit(err error, out *error, path string) bool {
	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		*out = &executor.ExitError{Code: exitErr.ExitStatus(), Path: path}
		return true
	}
	var missing *ssh.ExitMissingError
	if errors.As(err, &missing) {
		*out = fmt.Errorf("%s was terminated without reporting an exit status: %w", path, err)
		return true
	}
	return false
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

var _ executor.Executor = (*Executor)(nil)
