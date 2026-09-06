package mariadb

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/source"
)

// Logical produces a mariadb-dump stream.
type Logical struct{ cfg Config }

// NewLogical builds the driver.
func NewLogical(cfg Config) (*Logical, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.ToolRunner == nil {
		return nil, fmt.Errorf("mariadb: no tool runner configured")
	}
	return &Logical{cfg: cfg}, nil
}

// transactionalEngines are the storage engines for which --single-transaction
// actually produces a snapshot.
//
// Everything else -- MyISAM and Aria above all -- is copied table by table
// while writes continue, so a dump that spans two of them can hold a state the
// database never had. See Probe.
var transactionalEngines = map[string]bool{
	"INNODB":  true,
	"ROCKSDB": true,
	"TOKUDB":  true,
}

// Probe connects and reports what this server can produce.
func (l *Logical) Probe(ctx context.Context, ex executor.Executor) (source.Info, error) {
	if _, err := l.cfg.ResolveBin(binDump); err != nil {
		return source.Info{}, err
	}

	db, err := l.cfg.Connect(ctx, ex)
	if err != nil {
		return source.Info{}, err
	}
	defer func() { _ = db.Close() }()

	var version string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return source.Info{}, fmt.Errorf("mariadb: read the server version: %w", err)
	}

	info := source.Info{
		Engine:        source.EngineMariaDB,
		ServerVersion: version,
		Databases:     []string{l.cfg.Database},
	}

	nonTx, err := nonTransactionalTables(ctx, db, l.cfg.Database)
	if err != nil {
		return source.Info{}, err
	}
	if len(nonTx) > 0 && !l.cfg.AllowInconsistentSnapshot {
		// PD-006, and the same shape as the tablespace count P-007 forced on
		// PostgreSQL: a restriction of the engine, found while the
		// configuration is being read rather than at three in the morning.
		//
		// --single-transaction is what makes a logical backup a snapshot, and
		// it only covers transactional engines. One MyISAM table is enough for
		// the dump to hold a state the database never had, and nothing in the
		// output says so. Refusing is the only honest default; the escape
		// hatch is named, per-source, and recorded in the manifest.
		return source.Info{}, fmt.Errorf(
			"mariadb: database %q has %d non-transactional table(s) (%s), so --single-transaction "+
				"cannot give a consistent snapshot: converting them to InnoDB is the fix; "+
				"setting allow_inconsistent_snapshot on this source accepts the risk instead, "+
				"and every backup taken that way is marked inconsistent",
			l.cfg.Database, len(nonTx), summarise(nonTx))
	}
	if len(nonTx) > 0 {
		info.Restrictions = append(info.Restrictions, fmt.Sprintf(
			source.NotASnapshot+": %d non-transactional table(s) (%s) are copied while "+
				"writes continue, accepted by allow_inconsistent_snapshot",
			len(nonTx), summarise(nonTx)))
	}

	privs, err := currentPrivileges(ctx, db, l.cfg.Database)
	if err != nil {
		return source.Info{}, err
	}
	hasViews, err := schemaHasViews(ctx, db, l.cfg.Database)
	if err != nil {
		return source.Info{}, err
	}

	if missing := privs.missingForDump(hasViews); len(missing) > 0 {
		return source.Info{}, fmt.Errorf(
			"mariadb: user %q lacks %s on database %q, so a dump would fail partway through: "+
				"GRANT %s ON %s.* TO the backup user (EF-019)",
			l.cfg.User, strings.Join(missing, " and "), l.cfg.Database,
			strings.Join(missing, ", "), l.cfg.Database)
	}
	info.Restrictions = append(info.Restrictions, privs.restrictions()...)

	info.Kinds = []source.Kind{source.KindLogical}
	return info, nil
}

// nonTransactionalTables lists base tables whose engine cannot take part in the
// dump's transaction.
func nonTransactionalTables(ctx context.Context, db *sql.DB, schema string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT TABLE_NAME, ENGINE
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE' AND ENGINE IS NOT NULL
		ORDER BY TABLE_NAME`, schema)
	if err != nil {
		return nil, fmt.Errorf("mariadb: list the storage engines in %q: %w", schema, err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var name, engine string
		if err := rows.Scan(&name, &engine); err != nil {
			return nil, fmt.Errorf("mariadb: read a storage engine: %w", err)
		}
		if !transactionalEngines[strings.ToUpper(engine)] {
			out = append(out, name+" ("+engine+")")
		}
	}
	return out, rows.Err()
}

func schemaHasViews(ctx context.Context, db *sql.DB, schema string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'VIEW'`, schema).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("mariadb: look for views in %q: %w", schema, err)
	}
	return n > 0, nil
}

// summarise keeps an error message readable when a database has many tables.
func summarise(names []string) string {
	const show = 5
	if len(names) <= show {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:show], ", "), len(names)-show)
}

// privileges is what SHOW GRANTS says the current user holds on one schema.
type privileges struct {
	granted map[string]bool
	// inconclusive is set when the grants could not be read in full -- roles
	// this session has not activated, for instance. It changes a refusal into a
	// restriction: blocking a backup because our own parser gave up would be
	// the wrong way to be careful.
	inconclusive bool
}

func (p privileges) has(name string) bool { return p.granted[name] }

// missingForDump names the privileges without which mariadb-dump cannot finish.
//
// SHOW VIEW only when the schema actually holds a view: a view is dumped by its
// definition and a base table by its rows, so demanding it everywhere rejects an
// account perfectly able to do the job.
func (p privileges) missingForDump(hasViews bool) []string {
	if p.inconclusive {
		return nil
	}
	var missing []string
	if !p.has("SELECT") {
		missing = append(missing, "SELECT")
	}
	if hasViews && !p.has("SHOW VIEW") {
		missing = append(missing, "SHOW VIEW")
	}
	return missing
}

// restrictions explains what the dump will leave out, and why.
//
// TRIGGER and EVENT are not required, because their absence is handled by
// telling the tool to skip those objects -- but silently producing a dump
// without triggers is how a restore surprises someone months later, so it is
// said out loud and recorded in the manifest.
func (p privileges) restrictions() []string {
	if p.inconclusive {
		return []string{
			"privileges could not be read in full, so the dump is attempted without a pre-check; " +
				"a missing privilege will show up as a failed backup rather than a partial one",
		}
	}
	var out []string
	if !p.has("TRIGGER") {
		out = append(out, "triggers are not included: the user lacks the TRIGGER privilege")
	}
	if !p.has("EVENT") {
		out = append(out, "events are not included: the user lacks the EVENT privilege")
	}
	return out
}

var (
	grantHead  = regexp.MustCompile(`(?is)^\s*GRANT\s+(.+?)\s+ON\s+(\S+)\s+TO\s`)
	revokeHead = regexp.MustCompile(`(?is)^\s*REVOKE\s+(.+?)\s+ON\s+(\S+)\s+FROM\s`)
	// identifiedClause is what must never reach the repository: a password hash
	// in a backup is a liability nobody asked for.
	identifiedClause = regexp.MustCompile(`(?is)\s+IDENTIFIED\s+(BY\s+PASSWORD\s+'[^']*'|VIA\s+\S+(\s+USING\s+'[^']*')?|BY\s+'[^']*')`)
)

// currentPrivileges reads SHOW GRANTS and works out what applies to one schema.
func currentPrivileges(ctx context.Context, db *sql.DB, schema string) (privileges, error) {
	lines, err := showGrants(ctx, db, "")
	if err != nil {
		return privileges{}, err
	}

	p := privileges{granted: map[string]bool{}}
	for _, line := range lines {
		applyGrantLine(&p, line, schema)
	}
	p.expandAll()
	return p, nil
}

// applyGrantLine folds one SHOW GRANTS line into what is known so far.
func applyGrantLine(p *privileges, line, schema string) {
	if m := revokeHead.FindStringSubmatch(line); m != nil {
		// With partial_revokes -- the default shape on managed MySQL -- a
		// globally granted privilege can be withdrawn on one schema. Ignoring
		// the line counts a privilege the server refuses.
		if !scopeCovers(m[2], schema) {
			return
		}
		for _, name := range splitPrivileges(m[1]) {
			delete(p.granted, name)
		}
		return
	}
	m := grantHead.FindStringSubmatch(line)
	if m == nil {
		// A role granted to the user rather than a privilege. Roles are only in
		// force once SET ROLE has run, and SHOW GRANTS does not expand the ones
		// that are not, so the honest reading is "we cannot tell".
		p.inconclusive = true
		return
	}
	if !scopeCovers(m[2], schema) {
		return
	}
	for _, name := range splitPrivileges(m[1]) {
		p.granted[name] = true
	}
}

// expandAll turns ALL PRIVILEGES into the names the rest of the code asks about.
func (p *privileges) expandAll() {
	if !p.granted["ALL PRIVILEGES"] {
		return
	}
	for _, name := range []string{"SELECT", "SHOW VIEW", "TRIGGER", "EVENT", "RELOAD"} {
		p.granted[name] = true
	}
}

// scopeCovers reports whether a grant's ON clause reaches the schema.
//
// `*.*` is global, `db`.* is schema-wide, and anything else is a single table:
// a table-level grant does not let mariadb-dump read the whole schema, so it is
// not counted.
func scopeCovers(scope, schema string) bool {
	scope = strings.ReplaceAll(scope, "`", "")
	if scope == "*.*" {
		return true
	}
	return strings.EqualFold(scope, schema+".*")
}

// splitPrivileges breaks a grant list into names.
//
// Split rather than searched: "SHOW CREATE ROUTINE" contains "CREATE", so a
// substring test would count a privilege that was never granted.
func splitPrivileges(list string) []string {
	// A column list such as SELECT (col1, col2) narrows a privilege to
	// columns, which is not enough to dump a table; dropping the parenthesised
	// part would silently widen it, so the whole entry is discarded.
	var out []string
	for _, part := range strings.Split(list, ",") {
		name := strings.ToUpper(strings.TrimSpace(part))
		if name == "" || strings.Contains(name, "(") || strings.Contains(name, ")") {
			continue
		}
		out = append(out, name)
	}
	return out
}

func showGrants(ctx context.Context, db *sql.DB, account string) ([]string, error) {
	query := "SHOW GRANTS"
	if account != "" {
		query += " FOR " + account
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("mariadb: read the grants of %s: %w", accountName(account), err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, fmt.Errorf("mariadb: read a grant line: %w", err)
		}
		out = append(out, line)
	}
	return out, rows.Err()
}

func accountName(account string) string {
	if account == "" {
		return "the current user"
	}
	return account
}

// Open starts mariadb-dump and hands back its stdout.
func (l *Logical) Open(ctx context.Context, ex executor.Executor, req source.Request) (*source.Stream, error) {
	if req.Kind != source.KindLogical {
		return nil, fmt.Errorf("mariadb: %q is not a logical backup", req.Kind)
	}

	binPath, err := l.cfg.ResolveBin(binDump)
	if err != nil {
		return nil, err
	}

	sess, err := l.cfg.Open(ctx, ex)
	if err != nil {
		return nil, err
	}

	// The privileges decide the flags, so they are read before the dump starts
	// rather than discovered by it failing: without TRIGGER, mariadb-dump does
	// not quietly omit triggers, it stops on SHOW TRIGGERS.
	db, err := l.cfg.Connect(ctx, ex)
	if err != nil {
		_ = sess.Close()
		return nil, err
	}
	privs, privErr := currentPrivileges(ctx, db, l.cfg.Database)
	if privErr != nil {
		_ = db.Close()
		_ = sess.Close()
		return nil, privErr
	}
	withPosition := canRecordPosition(ctx, db, privs)

	args, err := l.dumpArgs(req, sess, privs, withPosition)
	if err != nil {
		_ = db.Close()
		_ = sess.Close()
		return nil, err
	}

	proc, err := l.cfg.ToolRunner.Start(ctx, executor.Command{
		Path: binPath,
		Args: args,
		Env:  sess.Env(binPath),
	})
	if err != nil {
		_ = db.Close()
		_ = sess.Close()
		return nil, fmt.Errorf("mariadb: start %s: %w", binDump, err)
	}

	s := &logicalStream{
		cfg:  l.cfg,
		sess: sess,
		db:   db,
		proc: proc,
		tail: newTailBuffer(),
	}
	go func() { _, _ = io.Copy(s.tail, proc.Stderr()) }()

	reader := proc.Stdout()
	if withPosition {
		s.head = &headScanner{r: reader}
		reader = s.head
	}

	return &source.Stream{
		Reader:   reader,
		Codec:    source.CodecNone,
		Sidecars: s.sidecars,
		Result:   func() source.Result { return s.result },
		Closer:   s,
	}, nil
}

// RenderCommand returns the argument list mariadb-dump would be given.
//
// Exported so a test can assert what ENF-021 promises: no credential reaches
// argv, which is world-readable in /proc/<pid>/cmdline while the dump runs.
func (l *Logical) RenderCommand(req source.Request, defaultsFile string, granted ...string) ([]string, error) {
	p := privileges{granted: map[string]bool{}}
	for _, g := range granted {
		p.granted[strings.ToUpper(g)] = true
	}
	return l.dumpArgs(req, &Session{cred: &credentials{path: defaultsFile}}, p, false)
}

// dumpArgs builds the command line.
func (l *Logical) dumpArgs(req source.Request, sess *Session, privs privileges, withPosition bool) ([]string, error) {
	if err := rejectNewline("database", l.cfg.Database); err != nil {
		return nil, err
	}

	// First, and it has to be: mariadb-dump reads option files in order and a
	// later --defaults-file is an error, so this is the only position it can
	// occupy. It is also what keeps the password out of argv.
	args := []string{sess.DefaultsFile()}

	args = append(args,
		// Forced, because the client treats the host name "localhost" as an
		// instruction to use a Unix socket rather than as an address. Koffr
		// always reaches a server over TCP -- directly or through a tunnel on
		// loopback -- so a socket is never the right answer, and the failure it
		// produces ("Can't connect to local server through socket") names a
		// file that has nothing to do with the configured server.
		"--protocol=TCP",
		"--single-transaction",
		// Row by row rather than the whole table into client memory: PD-003
		// applied to the client we drive.
		"--quick",
		"--routines",
		"--skip-add-locks",
		// Tablespace definitions make mariadb-dump read INFORMATION_SCHEMA.FILES,
		// which needs a global PROCESS privilege a backup role has no other use
		// for and that managed providers routinely refuse.
		"--no-tablespaces",
		// A single wide row otherwise ends the dump with a message that helps
		// nobody.
		"--max-allowed-packet=1G",
	)

	if privs.has("TRIGGER") {
		args = append(args, "--triggers")
	} else {
		args = append(args, "--skip-triggers")
	}
	if privs.has("EVENT") {
		args = append(args, "--events")
	}
	if withPosition {
		// =2 writes the position as a comment rather than an executable
		// statement: replaying a dump must not turn the restored server into a
		// replica of the one it came from. Taken inside the dump's own
		// transaction, so it names exactly the point this snapshot represents.
		args = append(args, "--master-data=2")
	}

	for _, table := range req.ExcludeTables {
		if err := rejectNewline("excluded table", table); err != nil {
			return nil, err
		}
		args = append(args, "--ignore-table="+l.cfg.Database+"."+table)
	}

	// --databases and not a bare name, so the dump carries its own CREATE
	// DATABASE and USE. That is what makes `zstd -d dump.sql.zst | mariadb`
	// enough on a blank server, with no flag to guess (PD-001).
	args = append(args, "--databases", l.cfg.Database)
	return args, nil
}

// rejectNewline refuses what cannot be escaped.
//
// A newline in an identifier would split an option file line or a command
// argument. Rejecting is right where escaping would be clever: these names come
// from a configuration file, so the answer is to fix the configuration.
func rejectNewline(what, value string) error {
	if strings.ContainsAny(value, "\n\r") {
		return fmt.Errorf("mariadb: %s %q contains a line break", what, value)
	}
	return nil
}

// canRecordPosition reports whether the binlog coordinates can be captured.
//
// Two things have to be true: the server keeps a binary log, and the user may
// read its position. Asking for it otherwise makes mariadb-dump fail outright,
// which would turn a working backup into no backup at all -- so it is checked
// rather than attempted.
func canRecordPosition(ctx context.Context, db *sql.DB, privs privileges) bool {
	if !privs.has("RELOAD") && !privs.inconclusive {
		return false
	}
	var name, value string
	if err := db.QueryRowContext(ctx, "SHOW VARIABLES LIKE 'log_bin'").Scan(&name, &value); err != nil {
		return false
	}
	return strings.EqualFold(value, "ON")
}

// changeMaster matches the coordinates mariadb-dump writes at the top of a dump.
var changeMaster = regexp.MustCompile(
	`(?i)MASTER_LOG_FILE\s*=\s*'([^']*)'\s*,\s*MASTER_LOG_POS\s*=\s*(\d+)`)

// headScanner passes the stream through while keeping its first few kilobytes.
//
// The binlog position is a comment near the top of the dump, and it is the one
// thing in there that a later point-in-time recovery cannot recompute. Reading
// it back later would mean decrypting and scanning a very large object; keeping
// a bounded head as the bytes go past costs nothing and puts the coordinates in
// the manifest, where they can be found without opening anything.
type headScanner struct {
	r    io.Reader
	head []byte
	done bool
}

const headBytes = 32 << 10

func (h *headScanner) Read(p []byte) (int, error) {
	n, err := h.r.Read(p)
	if n > 0 && !h.done {
		room := headBytes - len(h.head)
		if room > n {
			room = n
		}
		h.head = append(h.head, p[:room]...)
		if len(h.head) >= headBytes {
			h.done = true
		}
	}
	return n, err
}

func (h *headScanner) position() (file string, pos uint64) {
	m := changeMaster.FindSubmatch(h.head)
	if m == nil {
		return "", 0
	}
	n, err := strconv.ParseUint(string(m[2]), 10, 64)
	if err != nil {
		return "", 0
	}
	return string(m[1]), n
}

// logicalStream owns the running dump.
type logicalStream struct {
	cfg  Config
	sess *Session
	db   *sql.DB
	proc executor.Process
	tail *tailBuffer
	head *headScanner

	result source.Result

	mu     sync.Mutex
	closed bool
}

const tailBytes = 4 << 10

// tailBuffer keeps the last few kilobytes of stderr, which is where a client
// binary puts the sentence that explains the failure.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

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
	return strings.TrimSpace(string(t.buf))
}

// sidecars produces grants.sql.
//
// It runs against the same session as the dump and must therefore be called
// before Close, which is the mistake PostgreSQL made first: collected after the
// connection went away, it produced an empty file and a backup that silently
// lost half of itself.
func (s *logicalStream) sidecars() (map[string][]byte, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("mariadb: sidecars were asked for after the stream was closed")
	}

	grants, restriction := renderGrants(context.Background(), s.db, s.cfg.Database)
	if restriction != "" {
		// Not an error: a role without access to mysql.* can still take a
		// perfectly good backup of its own data. Saying nothing would be the
		// failure.
		return map[string][]byte{
			"grants.sql": []byte("-- " + restriction + "\n"),
		}, nil
	}
	return map[string][]byte{"grants.sql": grants}, nil
}

func (s *logicalStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	waitErr := s.proc.Wait()
	if s.head != nil {
		s.result.BinlogFile, s.result.BinlogPos = s.head.position()
	}
	if s.db != nil {
		_ = s.db.Close()
	}
	closeErr := s.sess.Close()

	if waitErr != nil {
		if tail := s.tail.String(); tail != "" {
			return fmt.Errorf("mariadb: %s failed: %s: %w", binDump, tail, waitErr)
		}
		return fmt.Errorf("mariadb: %s failed: %w", binDump, waitErr)
	}
	return closeErr
}

// renderGrants builds a replayable grants.sql for the accounts that can reach
// the database being backed up.
//
// No password hash is written, and that is a decision rather than an oversight.
// It is the position already taken for PostgreSQL (--no-role-passwords): a hash
// sitting in a backup repository is a liability nobody asked for. The restore
// procedure says to set the passwords again.
// It returns no error, and that is the point: every way this can go wrong is a
// restriction to report rather than a reason to fail a backup that has all the
// data. Saying so in the signature makes it impossible to get wrong later.
func renderGrants(ctx context.Context, db *sql.DB, schema string) (body []byte, restriction string) {
	accounts, err := accountsFor(ctx, db)
	if err != nil {
		return nil, "grants are not included: the backup user cannot read mysql.user, " +
			"so accounts and privileges must be recreated by hand"
	}

	var b strings.Builder
	b.WriteString("-- Accounts and privileges, rebuilt from SHOW GRANTS.\n")
	b.WriteString("-- Passwords are deliberately absent: set them again after restoring.\n")
	b.WriteString("-- Only privileges scoped to this database are listed. An account that\n")
	b.WriteString("-- reaches it through a server-wide grant is not reproduced here, because\n")
	b.WriteString("-- replaying such a grant would change the security of the target server.\n\n")

	wrote := 0
	for _, a := range accounts {
		lines, err := showGrants(ctx, db, a.quoted())
		if err != nil {
			continue
		}
		relevant := filterGrants(lines, schema)
		if len(relevant) == 0 {
			continue
		}
		fmt.Fprintf(&b, "CREATE USER IF NOT EXISTS %s;\n", a.quoted())
		for _, line := range relevant {
			fmt.Fprintf(&b, "%s;\n", line)
		}
		b.WriteString("\n")
		wrote++
	}
	if wrote == 0 {
		b.WriteString("-- No account holds privileges on this database.\n")
	}
	return []byte(b.String()), ""
}

type account struct{ user, host string }

func (a account) quoted() string {
	return "'" + strings.ReplaceAll(a.user, "'", "''") + "'@'" +
		strings.ReplaceAll(a.host, "'", "''") + "'"
}

func accountsFor(ctx context.Context, db *sql.DB) ([]account, error) {
	rows, err := db.QueryContext(ctx, "SELECT User, Host FROM mysql.user ORDER BY User, Host")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []account
	for rows.Next() {
		var a account
		if err := rows.Scan(&a.user, &a.host); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// scopeIsSchema reports whether a grant is scoped to exactly this schema.
//
// Deliberately narrower than scopeCovers, which answers a different question. A
// global grant does cover the schema, so it counts when deciding whether a dump
// can run -- but exporting it would put "GRANT ALL PRIVILEGES ON *.* TO root"
// in a file whose whole purpose is to be replayed. Replaying that onto a
// restore target changes the security of a server that has nothing to do with
// this backup.
func scopeIsSchema(scope, schema string) bool {
	scope = strings.ReplaceAll(scope, "`", "")
	return strings.EqualFold(scope, schema+".*")
}

// filterGrants keeps the lines that say something about this schema, and only
// about this schema.
//
// A repository full of every account's global grants would be a privilege map
// of the whole server, which is more than a backup of one database should carry
// -- and more than anyone should replay.
func filterGrants(lines []string, schema string) []string {
	var out []string
	for _, line := range lines {
		m := grantHead.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if !scopeIsSchema(m[2], schema) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(m[1]), "USAGE") {
			continue
		}
		// Stripped here rather than at the point of writing, so that the one
		// pure function in this path is the one carrying the guarantee -- and
		// so a test can prove it without a server. It matters on 10.6, where
		// SHOW GRANTS still carries the authentication clause; 11 and later
		// keep it in SHOW CREATE USER, which Koffr never asks for.
		out = append(out, strings.TrimSpace(identifiedClause.ReplaceAllString(line, "")))
	}
	sort.Strings(out)
	return out
}
