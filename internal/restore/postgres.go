package restore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/source/postgres"
)

// Postgres restores a logical PostgreSQL backup into a running server.
//
// It drives pg_restore and psql rather than reimplementing them, for the same
// reason the backup side drives pg_dump: the archive format is PostgreSQL's,
// its rules change between majors, and the only implementation that is always
// right about it ships with the server (PD-002).
type Postgres struct {
	// Config points at the server to restore *into*. It is a full source
	// configuration because restoring needs exactly what dumping needs: a
	// tunnel, a credentials file, and the client binaries for that major.
	Config postgres.Config
}

// PostgresRequest is what to restore and where.
type PostgresRequest struct {
	// Database is the database to restore into. It is always given explicitly:
	// the source's own name lives in the encrypted details, and restoring into
	// whatever the dump was called is how a test restore lands on production.
	Database string

	// Create issues CREATE DATABASE before restoring. It fails if the database
	// already exists, because restoring into a populated database silently
	// merges two datasets.
	Create bool

	// Dump is the pg_dump -Fc archive, already decrypted and decompressed.
	Dump io.Reader

	// Globals is the pg_dumpall --globals-only sidecar, replayed before the
	// dump so owners and grants resolve. Optional.
	Globals io.Reader

	// NoOwner restores without reassigning ownership, for a cluster whose roles
	// differ from the source's.
	NoOwner bool

	// Jobs would be pg_restore --jobs. It is refused while restoring from a
	// stream; see the error for why.
	Jobs int
}

// PostgresResult reports what happened.
type PostgresResult struct {
	// Warnings are problems that did not stop the restore, principally from
	// replaying globals into a cluster that already has some of those roles.
	Warnings []string
}

// Restore replays a backup into the configured server.
func (p Postgres) Restore(ctx context.Context, ex executor.Executor, req PostgresRequest) (PostgresResult, error) {
	var res PostgresResult

	if req.Database == "" {
		return res, errors.New(
			"restore: no target database; name the database to restore into, it is never taken from the backup")
	}
	if req.Dump == nil {
		return res, errors.New("restore: no dump to restore")
	}
	if req.Jobs > 1 {
		// pg_restore's parallel path seeks the archive to schedule its workers,
		// so it refuses standard input. Silently dropping the flag would turn a
		// restore sized for eight workers into a single-threaded one that
		// finishes hours late.
		return res, fmt.Errorf(
			"restore: --jobs=%d needs an archive on disk, and this restore is streamed; "+
				"run `koffr fetch` to write the archive out first, then pg_restore --jobs against the file",
			req.Jobs)
	}

	session, err := p.Config.Open(ctx, ex)
	if err != nil {
		return res, err
	}
	defer func() { _ = session.Close() }()

	if req.Create {
		if err := p.createDatabase(ctx, ex, req.Database); err != nil {
			return res, err
		}
	}

	if req.Globals != nil {
		if warn := p.replayGlobals(ctx, session, req.Globals); warn != "" {
			res.Warnings = append(res.Warnings, warn)
		}
	}

	return res, p.runRestore(ctx, session, req)
}

func (p Postgres) runRestore(ctx context.Context, session *postgres.Session, req PostgresRequest) error {
	bin, err := p.Config.ResolveBin("pg_restore")
	if err != nil {
		return err
	}
	args := []string{
		"--host=" + session.Host(),
		"--port=" + strconv.Itoa(session.Port()),
		"--username=" + p.Config.User,
		"--dbname=" + req.Database,
		"--no-password",
		"--exit-on-error",
	}
	if req.NoOwner {
		args = append(args, "--no-owner")
	}
	return run(ctx, p.Config.ToolRunner, executor.Command{
		Path: bin, Args: args, Env: session.Env(bin),
	}, req.Dump, "pg_restore")
}

// replayGlobals runs the roles-and-tablespaces sidecar through psql and returns
// what to tell the operator, empty when it went cleanly.
//
// It cannot fail the restore, and the signature says so. A role that already
// exists is the normal case when restoring into a shared cluster, and psql that
// is not installed means the globals were not replayed -- both are things the
// operator needs told, neither is a reason to withhold the data in the middle
// of an incident.
func (p Postgres) replayGlobals(ctx context.Context, session *postgres.Session, globals io.Reader) string {
	bin, err := p.Config.ResolveBin("psql")
	if err != nil {
		return "roles and tablespaces were not replayed: " + err.Error()
	}
	// No ON_ERROR_STOP, for the reason above. --quiet keeps the output to what
	// went wrong rather than a line per statement.
	cmd := executor.Command{
		Path: bin,
		Args: []string{
			"--host=" + session.Host(),
			"--port=" + strconv.Itoa(session.Port()),
			"--username=" + p.Config.User,
			"--dbname=postgres",
			"--no-password",
			"--quiet",
		},
		Env: session.Env(bin),
	}
	if reported := run(ctx, p.Config.ToolRunner, cmd, globals, "psql"); reported != nil {
		return "replaying roles and tablespaces reported: " + reported.Error()
	}
	return ""
}

// createDatabase makes the target, refusing to touch an existing one.
func (p Postgres) createDatabase(ctx context.Context, ex executor.Executor, name string) error {
	admin := p.Config
	admin.Database = "postgres"
	conn, err := admin.Connect(ctx, ex)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()

	// pgx cannot parameterise an identifier, so it is quoted rather than
	// interpolated. The name comes from a flag, and a flag is not a place to
	// start trusting input.
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		return fmt.Errorf("restore: create database %s: %w", name, err)
	}
	return nil
}

// run starts a client binary, feeds it the archive, and turns a non-zero exit
// into an error carrying what the tool actually said.
func run(ctx context.Context, ex executor.Executor, cmd executor.Command, stdin io.Reader, name string) error {
	cmd.Stdin = stdin
	proc, err := ex.Start(ctx, cmd)
	if err != nil {
		return fmt.Errorf("restore: start %s: %w", name, err)
	}

	// Both streams are drained. Stderr is kept because it is the only place the
	// tool explains itself; stdout is discarded but still read, because a child
	// blocked writing into a full pipe would never reach the exit that Wait is
	// waiting for.
	tail := newTail()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(tail, proc.Stderr()) }()
	go func() { defer wg.Done(); _, _ = io.Copy(io.Discard, proc.Stdout()) }()

	// Draining finishes before Wait, never alongside it. Wait is allowed to
	// close the read ends -- that is what lets the pipeline tear down a job it
	// has abandoned -- so reading concurrently with it loses exactly the stderr
	// this function exists to capture.
	wg.Wait()
	waitErr := proc.Wait()

	if waitErr != nil {
		if msg := tail.String(); msg != "" {
			return fmt.Errorf("restore: %s failed: %w: %s", name, waitErr, msg)
		}
		return fmt.Errorf("restore: %s failed: %w", name, waitErr)
	}
	return nil
}

// tail keeps the last few kilobytes of a tool's stderr. Bounded because psql
// replaying a large globals file can be talkative, and an error message that
// does not fit on a screen is one nobody reads.
type tail struct {
	buf []byte
}

const tailLimit = 4 << 10

func newTail() *tail { return &tail{} }

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailLimit {
		t.buf = t.buf[len(t.buf)-tailLimit:]
	}
	return len(p), nil
}

func (t *tail) String() string { return strings.TrimSpace(string(t.buf)) }
