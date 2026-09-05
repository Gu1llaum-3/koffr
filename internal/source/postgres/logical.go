package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/executor/tunnel"
	"github.com/Gu1llaum-3/koffr/internal/source"
)

// Logical backs up one database with pg_dump.
type Logical struct{ cfg Config }

// NewLogical validates a source's settings and returns it.
func NewLogical(cfg Config) (*Logical, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Logical{cfg: cfg}, nil
}

// Probe reports what this source can produce, and refuses it outright when
// something makes a backup impossible or, worse, silently incomplete.
func (l *Logical) Probe(ctx context.Context, ex executor.Executor) (source.Info, error) {
	// Both tools are checked, and from the same toolchain. pg_dumpall finds its
	// pg_dump beside itself, so a half-installed client is a failure worth
	// reporting now rather than when the globals sidecar is written.
	dumpMajor, err := toolVersion(ctx, l.cfg, "pg_dump")
	if err != nil {
		return source.Info{}, err
	}
	if _, err := toolVersion(ctx, l.cfg, "pg_dumpall"); err != nil {
		return source.Info{}, err
	}

	conn, err := l.cfg.connect(ctx, ex)
	if err != nil {
		return source.Info{}, err
	}
	defer func() { _ = conn.Close(ctx) }()

	var version string
	var versionNum int
	if err := conn.QueryRow(ctx,
		"SELECT current_setting('server_version'), current_setting('server_version_num')::int",
	).Scan(&version, &versionNum); err != nil {
		return source.Info{}, fmt.Errorf("postgres: read server version: %w", err)
	}

	serverMajor := versionNum / 10000
	if dumpMajor < serverMajor {
		return source.Info{}, fmt.Errorf(
			"postgres: %s is version %d but the server is %d; pg_dump must be at least the server's "+
				"version, so point bin_dir at the %d client tools",
			l.cfg.bin("pg_dump"), dumpMajor, serverMajor, serverMajor)
	}

	if err := checkPrivileges(ctx, conn, l.cfg.User); err != nil {
		return source.Info{}, err
	}

	info := source.Info{
		Engine:        source.EnginePostgreSQL,
		ServerVersion: version,
		Kinds:         []source.Kind{source.KindLogical},
		Databases:     []string{l.cfg.Database},
	}

	// P-007: pg_basebackup refuses --pgdata=- on a cluster with more than one
	// tablespace. Physical backup arrives in M2, but the check is one query and
	// belongs where a configuration can still be refused (EF-005, PD-006).
	extra, err := extraTablespaces(ctx, conn)
	if err != nil {
		return source.Info{}, err
	}
	if extra > 0 {
		info.Restrictions = append(info.Restrictions, fmt.Sprintf(
			"physical backup is unavailable: the cluster has %d tablespace(s) beyond the defaults, "+
				"and a streamed base backup can only carry one; use logical backup instead", extra))
	}

	return info, nil
}

func extraTablespaces(ctx context.Context, conn *pgx.Conn) (int, error) {
	var n int
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM pg_tablespace WHERE spcname NOT IN ('pg_default', 'pg_global')`,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: count tablespaces: %w", err)
	}
	return n, nil
}

// checkPrivileges implements EF-019.
//
// pg_dump run by a role that cannot read a table does not warn: depending on
// the case it aborts partway or emits an empty table. Both produce something
// that looks like a backup, and the second is only discovered during a restore.
// So the question is asked before pg_dump starts, and an answer of "no" refuses
// the source rather than producing a partial dump.
func checkPrivileges(ctx context.Context, conn *pgx.Conn, user string) error {
	const q = `
		SELECT n.nspname, c.relname, c.relrowsecurity
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r', 'p', 'm', 'f')
		  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
		  AND n.nspname NOT LIKE 'pg\_toast%'
		  AND n.nspname NOT LIKE 'pg\_temp%'
		  -- pg_dump emits no data for a relation owned by an extension, so
		  -- requiring SELECT on one would reject a perfectly good source.
		  AND NOT EXISTS (
		      SELECT 1 FROM pg_depend d
		      WHERE d.objid = c.oid AND d.classid = 'pg_class'::regclass AND d.deptype = 'e')
		  AND (
		      NOT has_table_privilege(c.oid, 'SELECT')
		      OR (c.relrowsecurity AND NOT COALESCE(
		          (SELECT rolbypassrls OR rolsuper FROM pg_roles WHERE rolname = current_user), false))
		  )
		ORDER BY 1, 2`

	rows, err := conn.Query(ctx, q)
	if err != nil {
		return fmt.Errorf("postgres: check read privileges: %w", err)
	}
	defer rows.Close()

	var unreadable, rlsBlocked []string
	for rows.Next() {
		var schema, name string
		var rls bool
		if err := rows.Scan(&schema, &name, &rls); err != nil {
			return fmt.Errorf("postgres: check read privileges: %w", err)
		}
		qualified := schema + "." + name
		if rls {
			rlsBlocked = append(rlsBlocked, qualified)
		} else {
			unreadable = append(unreadable, qualified)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: check read privileges: %w", err)
	}

	var problems []string
	if len(unreadable) > 0 {
		problems = append(problems, fmt.Sprintf(
			"cannot read %s", summarise(unreadable)))
	}
	if len(rlsBlocked) > 0 {
		problems = append(problems, fmt.Sprintf(
			"row-level security on %s would be applied, and pg_dump refuses to emit a partial table; "+
				"grant BYPASSRLS", summarise(rlsBlocked)))
	}
	if len(problems) > 0 {
		return fmt.Errorf("postgres: role %q %s", user, strings.Join(problems, "; and "))
	}
	return nil
}

// summarise names a few relations and counts the rest: an operator needs enough
// to act, not a list of four hundred tables in a log line.
func summarise(names []string) string {
	const show = 5
	if len(names) <= show {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:show], ", "), len(names)-show)
}

// Open starts pg_dump and hands back its output.
func (l *Logical) Open(ctx context.Context, ex executor.Executor, req source.Request) (*source.Stream, error) {
	if req.Kind != source.KindLogical {
		return nil, fmt.Errorf("postgres: %q is not a logical backup", req.Kind)
	}

	// EF-019 again, not only at configuration time: privileges can be revoked
	// between validation and the nightly run, and the cost is one query.
	conn, err := l.cfg.connect(ctx, ex)
	if err != nil {
		return nil, err
	}
	privErr := checkPrivileges(ctx, conn, l.cfg.User)
	_ = conn.Close(ctx)
	if privErr != nil {
		return nil, privErr
	}

	ep, err := l.endpoint(ctx, ex)
	if err != nil {
		return nil, err
	}

	// Only now, with the port known (P-004).
	cred, err := writeCredentials(l.cfg, ep)
	if err != nil {
		_ = ep.release()
		return nil, err
	}

	args, err := l.dumpArgs(req, ep.port)
	if err != nil {
		cred.remove()
		_ = ep.release()
		return nil, err
	}

	dumpPath, err := l.cfg.resolveBin("pg_dump")
	if err != nil {
		cred.remove()
		_ = ep.release()
		return nil, err
	}
	proc, err := l.cfg.ToolRunner.Start(ctx, executor.Command{
		Path: dumpPath,
		Args: args,
		Env:  l.cfg.env(cred, ep, dumpPath),
	})
	if err != nil {
		cred.remove()
		_ = ep.release()
		return nil, fmt.Errorf("postgres: start pg_dump: %w", err)
	}

	s := &logicalStream{
		cfg:  l.cfg,
		proc: proc,
		cred: cred,
		ep:   ep,
		ctx:  ctx,
		tail: newTailBuffer(),
	}
	// pg_dump's stderr is drained from the start, for two reasons. Leaving it
	// unread would block the dump once the pipe filled, and when the dump fails
	// those lines are the only explanation an operator gets: "exited with
	// status 1" on its own says nothing actionable.
	s.stderrDone = make(chan struct{})
	go func() {
		defer close(s.stderrDone)
		_, _ = io.Copy(s.tail, proc.Stderr())
	}()
	return &source.Stream{
		Reader:   proc.Stdout(),
		Codec:    source.CodecNone,
		Sidecars: s.sidecars,
		Result:   func() source.Result { return source.Result{} },
		Closer:   s,
	}, nil
}

// endpoint decides whether pg_dump needs a tunnel.
func (l *Logical) endpoint(ctx context.Context, ex executor.Executor) (endpoint, error) {
	target := net.JoinHostPort(l.cfg.Host, strconv.Itoa(l.cfg.Port))

	if ex.Capabilities().Direct {
		return endpoint{host: l.cfg.Host, port: l.cfg.Port}, nil
	}

	f, err := tunnel.Forward(ctx, ex, target)
	if err != nil {
		return endpoint{}, fmt.Errorf("postgres: reach %s: %w", target, err)
	}
	_, portStr, err := net.SplitHostPort(f.Addr())
	if err != nil {
		_ = f.Close()
		return endpoint{}, fmt.Errorf("postgres: read tunnel address: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		_ = f.Close()
		return endpoint{}, fmt.Errorf("postgres: read tunnel port: %w", err)
	}
	return endpoint{host: l.cfg.Host, port: port, hostAddr: "127.0.0.1", close: f.Close}, nil
}

// RenderCommand returns the exact argument list pg_dump would be given.
//
// Exported so that a test can assert what ENF-021 promises: no credential
// reaches argv, which is world-readable in /proc/<pid>/cmdline for as long as
// the dump runs.
func (l *Logical) RenderCommand(req source.Request, port int) ([]string, error) {
	args, err := l.dumpArgs(req, port)
	if err != nil {
		return nil, err
	}
	return append([]string{l.cfg.bin("pg_dump")}, args...), nil
}

// ToolPaths reports the absolute paths of the client binaries this source will
// run, so an operator can see which of several installed toolchains is in use
// (CT-001).
func (l *Logical) ToolPaths() (map[string]string, error) {
	paths := make(map[string]string, 2)
	for _, name := range []string{"pg_dump", "pg_dumpall"} {
		path, err := l.cfg.resolveBin(name)
		if err != nil {
			return nil, err
		}
		paths[name] = path
	}
	return paths, nil
}

func (l *Logical) dumpArgs(req source.Request, port int) ([]string, error) {
	args := []string{
		"--format=custom",
		// Uncompressed on purpose. zstd in the pipeline compresses better and
		// far faster than pg_dump's zlib, and compressing twice would only
		// waste CPU (P-003, EF-054).
		"--compress=0",
		// Never prompt: a job that blocks on a password prompt hangs forever
		// with no diagnosis.
		"--no-password",
		"--verbose",
		"--host=" + l.cfg.Host,
		"--port=" + strconv.Itoa(port),
		"--username=" + l.cfg.User,
		"--dbname=" + l.cfg.Database,
	}
	if req.Label != "" {
		// pg_dump has no label option; the label lives in the manifest. Reject
		// rather than silently drop it, so a caller is not misled.
		_ = req.Label
	}

	for _, s := range req.IncludeSchemas {
		if err := rejectNewline("schema", s); err != nil {
			return nil, err
		}
		args = append(args, "--schema="+s)
	}
	for _, s := range req.ExcludeSchemas {
		if err := rejectNewline("schema", s); err != nil {
			return nil, err
		}
		args = append(args, "--exclude-schema="+s)
	}
	for _, tbl := range req.IncludeTables {
		if err := rejectNewline("table", tbl); err != nil {
			return nil, err
		}
		args = append(args, "--table="+tbl)
	}
	for _, tbl := range req.ExcludeTables {
		if err := rejectNewline("table", tbl); err != nil {
			return nil, err
		}
		args = append(args, "--exclude-table="+tbl)
	}
	return args, nil
}

// rejectNewline guards the one character a pattern must not contain.
//
// pg_dump accepts patterns with spaces and quotes, and the SSH transport quotes
// them correctly, but a newline would split a --exclude-table into something
// unrecognisable. Refusing is better than silently dumping the wrong relations.
func rejectNewline(what, value string) error {
	if strings.ContainsAny(value, "\n\r") {
		return fmt.Errorf("postgres: %s pattern %q contains a line break", what, value)
	}
	return nil
}

// lastLines keeps the tail of a tool's diagnostics for an error message.
//
// The tail is where the reason is: everything before it is progress. A whole
// stderr in an error would be unreadable, and none of it would be worse.
func lastLines(b []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "; "))
}

// logicalStream owns everything one dump holds open.
type logicalStream struct {
	cfg  Config
	proc executor.Process
	cred *credentials
	ep   endpoint
	ctx  context.Context

	tail       *tailBuffer
	stderrDone chan struct{}
}

// tailBuffer keeps the last few kilobytes written to it.
//
// pg_dump --verbose emits a line per object, so keeping everything would mean
// holding a schema's worth of text in memory for a message nobody reads unless
// something failed. The tail is where the reason is.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const tailBytes = 8 << 10

func newTailBuffer() *tailBuffer { return &tailBuffer{buf: make([]byte, 0, tailBytes)} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailBytes {
		t.buf = t.buf[len(t.buf)-tailBytes:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// sidecars runs pg_dumpall --globals-only.
//
// Roles and tablespaces live in the cluster, not in a database, so a dump of
// one database does not carry them. Restoring without them produces a database
// whose owners and grants do not exist.
func (s *logicalStream) sidecars() (map[string][]byte, error) {
	dumpallPath, err := s.cfg.resolveBin("pg_dumpall")
	if err != nil {
		return nil, err
	}
	proc, err := s.cfg.ToolRunner.Start(s.ctx, executor.Command{
		Path: dumpallPath,
		Args: []string{
			"--globals-only",
			"--no-password",
			"--host=" + s.cfg.Host,
			"--port=" + strconv.Itoa(s.ep.port),
			"--username=" + s.cfg.User,
			// Without this, pg_dumpall connects to a database called
			// "postgres", which may not exist and is not the one the
			// credentials file was written for.
			"--database=" + s.cfg.Database,
		},
		Env: s.cfg.env(s.cred, s.ep, dumpallPath),
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: start pg_dumpall: %w", err)
	}

	// stderr is drained concurrently: leaving it unread would block pg_dumpall
	// once the pipe filled, and its contents are the only explanation of a
	// failure an operator will get.
	stderrCh := make(chan []byte, 1)
	go func() {
		out, _ := io.ReadAll(proc.Stderr())
		stderrCh <- out
	}()

	globals, readErr := io.ReadAll(proc.Stdout())
	waitErr := proc.Wait()
	stderr := <-stderrCh

	if waitErr != nil {
		return nil, fmt.Errorf("postgres: pg_dumpall: %w: %s", waitErr, lastLines(stderr, 3))
	}
	if readErr != nil {
		return nil, fmt.Errorf("postgres: read globals: %w", readErr)
	}
	return map[string][]byte{"globals.sql": globals}, nil
}

// Close reaps pg_dump and releases the tunnel and the credentials file.
//
// The credentials are removed whatever happens, including on a panic unwinding
// through here, so a crashed job leaves no password on disk (ENF-022).
func (s *logicalStream) Close() error {
	defer s.cred.remove()

	waitErr := s.proc.Wait()
	<-s.stderrDone
	releaseErr := s.ep.release()

	// A dump killed by cancellation is the caller's own doing, not a failure to
	// report on top of whatever made them cancel.
	if waitErr != nil && s.ctx.Err() != nil {
		waitErr = nil
	}
	if waitErr != nil {
		if detail := lastLines([]byte(s.tail.String()), 4); detail != "" {
			waitErr = fmt.Errorf("%w: %s", waitErr, detail)
		}
	}
	return errors.Join(waitErr, releaseErr)
}
