package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
)

// Format is the shape pg_dump writes. The specification gives two, and they do
// not have the same nature: one is a stream, the other is a directory (E-054).
type Format string

// The two formats of § 5.3 F3.5.
const (
	// Custom is -Fc: one file, compressed by us, and the default.
	Custom Format = "custom"

	// Directory is -Fd: a directory pg_dump fills in parallel. It is not a
	// stream, which is why E-054 makes it impose the stage mode.
	Directory Format = "directory"
)

// The refusals of E-054. Both are permanent, not gaps: the directory format
// cannot be a stream, and it cannot cross into a container.
var (
	// ErrDirectoryInExec is E-054: -Fd is unavailable through the exec
	// strategy. pg_dump would fill a directory inside the container, where
	// koffr has no way to read it back as one archive.
	ErrDirectoryInExec = errors.New("the directory format is not available through the exec strategy")

	// ErrDirectoryNeedsStaging is E-054 again, from the other side: a directory
	// dump has no standard output to chain onto. It is written to a staging
	// directory and packed afterwards.
	ErrDirectoryNeedsStaging = errors.New("the directory format writes a directory, not a stream")
)

// DumpRequest is one dump to run: which database, with which tool, in which
// shape, and — when the database declares the exec strategy — inside which
// container.
type DumpRequest struct {
	Target resolve.Target

	// Tool is the binary the resolver chose. Its family decides the command
	// line, because the family of a tool is what the tool said it was, never
	// what the configuration declared (E-041).
	Tool resolve.Candidate

	// Format applies to PostgreSQL. Empty means Custom, the default of E-054.
	Format Format

	// Container, when set, runs the dump inside that container — the exec
	// strategy of § 5.2 F2.9, applied to the dump and not only to the
	// resolution (ADR-0015).
	Container string

	// Timeout bounds the whole dump. Zero leaves it unbounded: a dump of a
	// large database legitimately takes hours, and killing it halfway is worse
	// than waiting.
	Timeout time.Duration
}

// DumpCommand builds the argv of a dump. It is exported because it is what
// koffr should be able to show an operator — a dump that fails is diagnosed by
// re-running the command by hand — and because a test can read it without
// starting a server.
//
// The password is never on it: it travels in the environment, where E-115 wants
// it, and not in a command line every process on the machine can read.
func DumpCommand(request DumpRequest) []string {
	host, port := request.Target.Host, request.Target.Port
	if request.Container != "" {
		// Inside the database's own container, the server is on the loopback
		// at its standard port: the address koffr uses from the outside is a
		// published port that means nothing in there.
		host, port = loopback, defaultPort(request.Tool.Family)
	}

	switch request.Tool.Family {
	case resolve.PostgreSQL:
		return []string{
			request.Tool.Path,
			"--host=" + host,
			"--port=" + strconv.Itoa(port),
			"--username=" + request.Target.User,
			// Fail rather than wait for a prompt nobody is there to answer.
			"--no-password",
			"--format=" + string(request.format()),
			// N-3 — the archive restores into any cluster, whatever the roles
			// of the one it came from.
			"--no-owner",
			"--no-privileges",
			request.Target.Database,
		}

	case resolve.MySQL, resolve.MariaDB:
		return []string{
			request.Tool.Path,
			"--host=" + host,
			"--port=" + strconv.Itoa(port),
			"--user=" + request.Target.User,
			// E-056 — consistent on InnoDB, and complete: a schema that comes
			// back without its routines, triggers and events is not a restore.
			"--single-transaction",
			"--routines",
			"--triggers",
			"--events",
			request.Target.Database,
		}

	default:
		return nil
	}
}

// loopback is how a tool running inside the database's container reaches it.
const loopback = "127.0.0.1"

func defaultPort(family resolve.Family) int {
	if family == resolve.PostgreSQL {
		return 5432
	}

	return 3306
}

func (r DumpRequest) format() Format {
	if r.Format == "" {
		return Custom
	}

	return r.Format
}

// passwordEnvironment carries the password to the tool out of sight of `ps`.
func (r DumpRequest) passwordEnvironment() []string {
	if r.Target.Password == "" {
		return nil
	}

	if r.Tool.Family == resolve.PostgreSQL {
		return []string{"PGPASSWORD=" + r.Target.Password}
	}

	return []string{"MYSQL_PWD=" + r.Target.Password}
}

// Dump starts the dump and hands back its output as a stream. Nothing is held:
// the reader is the standard output of the sub-process, which is what E-025
// requires of the whole chain.
//
// Closing the returned reader **waits for the sub-process** and reports what it
// said. That is not a detail of hygiene: a dump whose process failed halfway
// yields a perfectly well-formed archive of nothing, and only the exit code
// tells the difference (BKP-09). It is also what closes the connection to the
// database, which is the whole of E-055.
func (e *Engine) Dump(ctx context.Context, request DumpRequest) (io.ReadCloser, error) {
	if err := request.check(); err != nil {
		return nil, err
	}

	if request.Container != "" {
		return e.dumpInContainer(ctx, request)
	}

	return dumpOnHost(ctx, request)
}

// check refuses what cannot work, before anything is started. E-054 asks that
// the agent "signal it explicitly": an operator who configured -Fd on a
// container gets a sentence, not an empty archive.
func (r DumpRequest) check() error {
	switch r.Tool.Family {
	case resolve.PostgreSQL, resolve.MySQL, resolve.MariaDB:
	default:
		return fmt.Errorf("%w: %q", resolve.ErrUnsupportedEngine, r.Tool.Family)
	}

	if r.Tool.Path == "" {
		return errors.New("no tool was resolved for this dump")
	}

	if r.format() == Directory {
		if r.Tool.Family != resolve.PostgreSQL {
			return fmt.Errorf("the directory format belongs to pg_dump, not to %s", r.Tool.Family)
		}
		if r.Container != "" {
			return fmt.Errorf("%w: %s runs inside the container %s, where koffr cannot read back "+
				"a directory as one archive — use the custom format for this database",
				ErrDirectoryInExec, r.Tool.Path, r.Container)
		}

		return fmt.Errorf("%w: it is written to a staging directory and packed afterwards, "+
			"which is why it requires the stage mode", ErrDirectoryNeedsStaging)
	}

	return nil
}

// dumpOnHost runs the tool of the machine.
func dumpOnHost(ctx context.Context, request DumpRequest) (io.ReadCloser, error) {
	running, cancel := request.bounded(ctx)

	argv := DumpCommand(request)

	command := exec.CommandContext(running, argv[0], argv[1:]...)
	// The sub-process gets **only** what koffr hands it, and never this
	// process's environment: AR-05 reserves reading it to internal/config, and
	// an inherited PGPASSWORD or ~/.my.cnf would quietly change what a dump
	// contains. The tool is invoked by absolute path, so it needs no PATH.
	command.Env = request.passwordEnvironment()
	// A killed dump must not leave its child holding the pipe.
	command.WaitDelay = 5 * time.Second

	said := &tail{}
	command.Stderr = said

	out, err := command.StdoutPipe()
	if err != nil {
		cancel()

		return nil, fmt.Errorf("read the output of %s: %w", argv[0], err)
	}

	if err := command.Start(); err != nil {
		cancel()

		return nil, fmt.Errorf("start %s: %w", argv[0], err)
	}

	return &dumpStream{
		out:    out,
		what:   argv[0],
		said:   said,
		cancel: cancel,
		wait:   command.Wait,
	}, nil
}

func (r DumpRequest) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	if r.Timeout <= 0 {
		return context.WithCancel(ctx)
	}

	return context.WithTimeout(ctx, r.Timeout)
}

// dumpStream is the output of a running dump. It is read by one consumer, like
// the pipe it wraps.
type dumpStream struct {
	out    io.ReadCloser
	what   string
	said   *tail
	cancel context.CancelFunc
	wait   func() error

	drained bool
	closed  bool
}

func (d *dumpStream) Read(into []byte) (int, error) {
	read, err := d.out.Read(into)
	if errors.Is(err, io.EOF) {
		d.drained = true
	}

	return read, err //nolint:wrapcheck // a pass-through of the process pipe
}

// Close waits for the sub-process and turns a non-zero exit into an error that
// carries what the tool said. A dump abandoned before its end is stopped rather
// than left to fill a pipe nobody reads.
func (d *dumpStream) Close() error {
	if d.closed {
		return nil
	}
	d.closed = true

	abandoned := !d.drained
	if abandoned {
		d.cancel()
	}

	err := d.wait()
	d.cancel()

	switch {
	case abandoned:
		return nil // the caller gave up first; its own error is the one that matters

	case err != nil:
		return fmt.Errorf("%s failed: %w%s", d.what, err, d.said.suffix())

	default:
		return nil
	}
}

// tail keeps the end of what a tool wrote on its error output. The end, because
// that is where the reason is, and bounded, because a dump can complain for
// megabytes.
type tail struct {
	kept []byte
}

const keptFromStderr = 4 << 10

func (t *tail) Write(p []byte) (int, error) {
	t.kept = append(t.kept, p...)
	if len(t.kept) > keptFromStderr {
		t.kept = t.kept[len(t.kept)-keptFromStderr:]
	}

	return len(p), nil
}

func (t *tail) suffix() string {
	said := bytes.TrimSpace(t.kept)
	if len(said) == 0 {
		return ""
	}

	return "\n" + string(said)
}
