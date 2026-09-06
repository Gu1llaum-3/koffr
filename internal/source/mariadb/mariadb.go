// Package mariadb turns a MariaDB server into a backup stream.
//
// It mirrors internal/source/postgres deliberately: the two engines differ in
// almost every detail and in none of the shape, and a reader who knows one
// should not have to relearn the other.
package mariadb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/executor/tunnel"
)

// Config describes one MariaDB source.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string

	// TLS is "disable", "require" or "verify" -- the three states that mean
	// something to the client. An empty value is "disable", to match what a
	// MariaDB on a private network almost always is.
	TLS string

	// BinDir locates mariadb-dump and mariadb for this server. Empty means
	// PATH. Per-source for the same reason as PostgreSQL: one Koffr can back up
	// servers of different majors and the client has to match (CT-001).
	BinDir string

	// AllowInconsistentSnapshot lets a source with non-transactional tables be
	// backed up anyway. Off by default and never inferred: see Probe.
	AllowInconsistentSnapshot bool

	// ToolRunner runs the client binaries. It is the Koffr host, and is
	// injectable only so tests can watch what gets run.
	ToolRunner executor.Executor

	ConnectTimeout time.Duration
}

const defaultConnectTimeout = 30 * time.Second

func (c *Config) applyDefaults() {
	if c.Port == 0 {
		c.Port = 3306
	}
	if c.TLS == "" {
		c.TLS = "disable"
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = defaultConnectTimeout
	}
}

func (c *Config) validate() error {
	var problems []string
	if c.Host == "" {
		problems = append(problems, "no host")
	}
	if c.User == "" {
		problems = append(problems, "no user")
	}
	if c.Database == "" {
		problems = append(problems, "no database")
	}
	switch c.TLS {
	case "disable", "require", "verify":
	default:
		problems = append(problems, fmt.Sprintf("%q is not a TLS mode", c.TLS))
	}
	if len(problems) > 0 {
		return fmt.Errorf("mariadb: %s", strings.Join(problems, ", "))
	}
	return nil
}

// Client binaries. MariaDB renamed them in 10.5 and kept the old names as
// symlinks; the new ones are used and the old ones are the fallback, because a
// 10.6 server on a host with only the legacy package is a real configuration.
const (
	binDump   = "mariadb-dump"
	binClient = "mariadb"

	legacyDump   = "mysqldump"
	legacyClient = "mysql"
)

func legacyName(name string) string {
	switch name {
	case binDump:
		return legacyDump
	case binClient:
		return legacyClient
	}
	return ""
}

func (c *Config) bin(name string) string {
	if c.BinDir == "" {
		return name
	}
	return filepath.Join(c.BinDir, name)
}

// ResolveBin returns the path of a client binary, or says plainly that it is
// missing.
//
// Called from Probe, so a toolchain that is not installed is a configuration
// error rather than a job that dies halfway (PD-006, CT-001).
func (c *Config) ResolveBin(name string) (string, error) {
	candidates := []string{name}
	if legacy := legacyName(name); legacy != "" {
		candidates = append(candidates, legacy)
	}

	var tried []string
	for _, candidate := range candidates {
		p := c.bin(candidate)
		if c.BinDir == "" {
			found, err := exec.LookPath(p)
			if err == nil {
				return found, nil
			}
			tried = append(tried, p)
			continue
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
		tried = append(tried, p)
	}

	where := "on PATH"
	if c.BinDir != "" {
		where = "in " + c.BinDir
	}
	return "", fmt.Errorf(
		"mariadb: %s not found %s (looked for %s); install the MariaDB client package, "+
			"or set bin_dir for this source", name, where, strings.Join(tried, ", "))
}

// Session is a live connection context for one job: an endpoint to talk to and
// a credentials file that exists only while it does.
type Session struct {
	cfg  Config
	ep   endpoint
	cred *credentials
}

// Host is the name the client is pointed at.
func (s *Session) Host() string { return s.ep.host }

// Port is the effective port, which is the tunnel's when tunnelled.
func (s *Session) Port() int { return s.ep.port }

// DefaultsFile is the argument that carries the credentials, and the reason no
// password ever reaches argv (PD-004, ENF-021).
func (s *Session) DefaultsFile() string { return "--defaults-file=" + s.cred.path }

// Env is the curated environment for a client binary.
func (s *Session) Env(binPath string) []string { return s.cfg.env(binPath) }

// Close releases the tunnel and removes the credentials file.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.cred.remove()
	return s.ep.release()
}

// Open binds whatever is needed to reach the server and writes the credentials.
//
// The order is load-bearing. The credentials file names the host and the port,
// and the client matches on both, so a file written before the tunnel is bound
// carries a port the kernel had not yet chosen. That is P-004, found on
// PostgreSQL and true here for the same reason.
func (c Config) Open(ctx context.Context, ex executor.Executor) (*Session, error) {
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	ep, err := c.endpoint(ctx, ex)
	if err != nil {
		return nil, err
	}
	cred, err := writeCredentials(c, ep)
	if err != nil {
		_ = ep.release()
		return nil, err
	}
	return &Session{cfg: c, ep: ep, cred: cred}, nil
}

// endpoint is the address a client binary should be pointed at.
type endpoint struct {
	host string
	port int
	// direct says the connection is not tunnelled, which decides whether the
	// client should be told to skip name resolution.
	direct bool
	close  func() error
}

func (e endpoint) release() error {
	if e.close == nil {
		return nil
	}
	return e.close()
}

func (c Config) endpoint(ctx context.Context, ex executor.Executor) (endpoint, error) {
	target := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))

	if ex.Capabilities().Direct {
		return endpoint{host: c.Host, port: c.Port, direct: true}, nil
	}

	f, err := tunnel.Forward(ctx, ex, target)
	if err != nil {
		return endpoint{}, fmt.Errorf("mariadb: reach %s: %w", target, err)
	}
	_, portStr, err := net.SplitHostPort(f.Addr())
	if err != nil {
		_ = f.Close()
		return endpoint{}, fmt.Errorf("mariadb: read tunnel address: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		_ = f.Close()
		return endpoint{}, fmt.Errorf("mariadb: read tunnel port: %w", err)
	}
	// 127.0.0.1 and not the real hostname: the tunnel listens on loopback, and
	// unlike libpq the MariaDB client has no way to verify a certificate
	// against a name other than the one it dialled.
	return endpoint{host: "127.0.0.1", port: port, close: f.Close}, nil
}

// credentials is a .my.cnf that exists only for one job.
type credentials struct {
	dir  string
	path string
}

// writeCredentials writes the client options file for one endpoint.
//
// Under the OS temporary directory rather than anywhere of ours: the client
// refuses an options file it considers group- or world-readable, and some
// filesystems ignore chmod, so a directory the OS made for us is the one most
// likely to have the permissions the client wants.
func writeCredentials(cfg Config, ep endpoint) (*credentials, error) {
	// MkdirTemp creates the directory 0700. The listing alone would say which
	// databases are being backed up, so it is not left readable.
	dir, err := os.MkdirTemp("", "koffr-mycnf-")
	if err != nil {
		return nil, fmt.Errorf("mariadb: create credentials directory: %w", err)
	}

	var b strings.Builder
	b.WriteString("[client]\n")
	fmt.Fprintf(&b, "user=%s\n", escapeMyCnf(cfg.User))
	fmt.Fprintf(&b, "password=%s\n", escapeMyCnf(cfg.Password))
	fmt.Fprintf(&b, "host=%s\n", escapeMyCnf(ep.host))
	fmt.Fprintf(&b, "port=%d\n", ep.port)
	switch cfg.TLS {
	case "require":
		b.WriteString("ssl=1\nssl-verify-server-cert=0\n")
	case "verify":
		b.WriteString("ssl=1\nssl-verify-server-cert=1\n")
	default:
		b.WriteString("ssl=0\n")
	}

	path := filepath.Join(dir, "koffr.cnf")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("mariadb: write credentials file: %w", err)
	}
	return &credentials{dir: dir, path: path}, nil
}

// remove deletes the credentials. It runs from a defer, including on a panic,
// so a crashed job does not leave a password on disk (ENF-022).
func (c *credentials) remove() {
	if c == nil {
		return
	}
	_ = os.RemoveAll(c.dir)
}

// escapeMyCnf makes a value safe for an option file.
//
// The parser strips a trailing comment introduced by an unquoted "#", treats a
// backslash as an escape, and ends the value at a newline. A password
// containing any of those would otherwise be silently truncated -- and a
// truncated password fails as "access denied", which sends an operator looking
// at the wrong thing entirely.
func escapeMyCnf(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\r", `\r`,
	)
	return `"` + r.Replace(s) + `"`
}

// env builds the environment for a client binary.
//
// Curated, not inherited: a stray MYSQL_HOST or MYSQL_TCP_PORT would silently
// change where a backup connects, and MYSQL_PWD is emptied rather than left
// alone because an inherited one would take precedence over the options file
// and turn a wrong password into a mystery.
func (c *Config) env(binPath string) []string {
	return []string{
		"PATH=" + filepath.Dir(binPath),
		"MYSQL_PWD=",
		"MYSQL_HOST=",
		"MYSQL_TCP_PORT=",
		// Deterministic encoding: a dump whose character set depends on the
		// caller's locale is a dump that does not reproduce.
		"LC_ALL=C.UTF-8",
		"LANG=C.UTF-8",
	}
}

// Connect opens a SQL connection through the executor.
//
// The dialler goes through the executor rather than through the process's own
// network stack, so an SSH-reached server needs no tunnel for queries -- the
// same arrangement pgx gets from its DialFunc.
func (c Config) Connect(ctx context.Context, ex executor.Executor) (*sql.DB, error) {
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}

	cfg := mysql.NewConfig()
	cfg.User = c.User
	cfg.Passwd = c.Password
	cfg.Net = "koffr"
	cfg.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	cfg.DBName = c.Database
	cfg.Timeout = c.ConnectTimeout
	cfg.ParseTime = true
	// No charset or collation is forced here. This connection only reads
	// metadata; what the dump is encoded in is decided by mariadb-dump and by
	// LC_ALL, not by a session variable set over here.
	switch c.TLS {
	case "require":
		cfg.TLSConfig = "skip-verify"
	case "verify":
		cfg.TLSConfig = "true"
	}

	name := "koffr-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	mysql.RegisterDialContext(name, func(ctx context.Context, addr string) (net.Conn, error) {
		return ex.Dial(ctx, "tcp", addr)
	})
	cfg.Net = name

	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		mysql.DeregisterDialContext(name)
		return nil, fmt.Errorf("mariadb: connect to %s as %s: %w", cfg.Addr, c.User, err)
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(ctx, c.ConnectTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		mysql.DeregisterDialContext(name)
		// The error carries the address and the user, never the password: a
		// connection string with a credential in it is exactly what PD-004
		// forbids in a message.
		//
		// 1044 is "access denied to this database", which the server answers
		// before any privilege of ours can be checked. Left as it comes, it
		// tells an operator that something is denied but not what to grant.
		if isDatabaseAccessDenied(err) {
			return nil, fmt.Errorf(
				"mariadb: user %q cannot open database %q on %s: the account needs at least "+
					"SELECT on it (see docs/mariadb-privileges.md): %w",
				c.User, c.Database, cfg.Addr, err)
		}
		return nil, fmt.Errorf("mariadb: connect to %s as %s: %w", cfg.Addr, c.User, err)
	}
	return db, nil
}

// isDatabaseAccessDenied recognises error 1044, which the server answers when an
// account may log in but not open the database it asked for.
func isDatabaseAccessDenied(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1044
}
