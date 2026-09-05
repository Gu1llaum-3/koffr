// Package postgres backs up PostgreSQL.
//
// Where things run matters here, and it is the opposite of MariaDB physical
// backup. pg_dump connects over the network, so it runs on the Koffr host and
// pulls: nothing is installed on the database host and nothing is written to
// its disk (PD-002, PD-003). The Executor is the route to the database, not the
// place the tool runs.
//
// That gives two ways in, and they are not interchangeable:
//
//   - Koffr's own queries go through pgx with the executor as its DialFunc, so
//     they need no listener at all.
//   - pg_dump takes a host and a port and knows nothing about SSH, so when the
//     executor is not direct it gets a loopback tunnel to aim at.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/executor/local"
)

// Config describes one PostgreSQL source.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string

	// SSLMode and SSLRootCert are passed to both pgx and the client binaries.
	// verify-full keeps working through a tunnel because the real hostname is
	// what gets verified, not the loopback address (P-004).
	SSLMode     string
	SSLRootCert string

	// BinDir locates the client binaries for this server's major version.
	// Empty means PATH. Supporting PostgreSQL 14 to 18 means five toolchains,
	// which is why this is per-source (CT-001).
	BinDir string

	// ToolRunner runs the client binaries. It is the Koffr host, and is
	// injectable only so tests can watch what gets run.
	ToolRunner executor.Executor

	ConnectTimeout time.Duration
}

const defaultConnectTimeout = 30 * time.Second

func (c *Config) applyDefaults() {
	if c.Port == 0 {
		c.Port = 5432
	}
	if c.SSLMode == "" {
		// libpq's own default is "prefer", which silently accepts plaintext.
		// PD-004 says a weaker setting must be asked for, not inherited.
		c.SSLMode = "verify-full"
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = defaultConnectTimeout
	}
	if c.ToolRunner == nil {
		c.ToolRunner = local.New()
	}
}

func (c *Config) validate() error {
	var problems []string
	if c.Host == "" {
		problems = append(problems, "host is empty")
	}
	if c.User == "" {
		problems = append(problems, "user is empty")
	}
	if c.Database == "" {
		problems = append(problems, "database is empty")
	}
	if len(problems) > 0 {
		return fmt.Errorf("postgres: %s", strings.Join(problems, "; "))
	}
	return nil
}

// bin names a client binary for this source, for use in messages.
func (c *Config) bin(name string) string {
	if c.BinDir == "" {
		return name
	}
	return filepath.Join(c.BinDir, name)
}

// resolveBin returns the absolute path of a client binary.
//
// Absolute on purpose. pg_dumpall locates its own pg_dump relative to argv[0],
// and Koffr hands its tools a curated environment rather than the operator's,
// so a bare name leaves pg_dumpall with nothing to resolve against. Resolving
// here also removes the ambiguity CT-001 is about: with five client toolchains
// installed, "pg_dump" says nothing about which one runs.
// ResolveBin locates a client binary for this source's major version.
func (c *Config) ResolveBin(name string) (string, error) {
	if c.BinDir != "" {
		path := filepath.Join(c.BinDir, name)
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf(
				"postgres: %s is not in the configured bin_dir %s: %w", name, c.BinDir, err)
		}
		return path, nil
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf(
			"postgres: %s was not found on PATH; install the PostgreSQL client tools "+
				"or set bin_dir for this source: %w", name, err)
	}
	return path, nil
}

// dsn builds a connection string for pgx.
//
// The password travels in this string, which lives in memory only. It never
// reaches a command line, where it would be world-readable in /proc (ENF-021).
func (c *Config) dsn() string {
	u := "postgres://" + url(c.User) + ":" + url(c.Password) +
		"@" + net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) + "/" + url(c.Database) +
		"?sslmode=" + url(c.SSLMode) +
		"&connect_timeout=" + strconv.Itoa(int(c.ConnectTimeout.Seconds()))
	if c.SSLRootCert != "" {
		u += "&sslrootcert=" + url(c.SSLRootCert)
	}
	return u
}

// connect opens a pgx connection through the executor.
//
// No tunnel is involved: pgx takes a DialFunc, so the connection is opened by
// the executor directly. Forwarding it through a local listener would add a
// hop for nothing.
func (c *Config) Connect(ctx context.Context, ex executor.Executor) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(c.dsn())
	if err != nil {
		// The DSN carries the password, so it is never echoed.
		return nil, fmt.Errorf("postgres: connection settings for %s are not usable: %w", c.Host, err)
	}
	cfg.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return ex.Dial(ctx, network, addr)
	}

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect to %s as %s: %w",
			net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), c.User, err)
	}
	return conn, nil
}

// url percent-escapes a DSN component.
func url(s string) string {
	var b strings.Builder
	for i := range len(s) {
		ch := s[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9',
			ch == '-', ch == '.', ch == '_', ch == '~':
			b.WriteByte(ch)
		default:
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return b.String()
}

// Session is a client binary's way in to a database: an address to aim at, a
// credentials file, and an environment.
//
// Exported because restoring needs exactly what backing up needs. pg_restore
// takes the same host, the same .pgpass and the same tunnel as pg_dump, and
// duplicating that plumbing is how the two drift until one of them keeps
// working through a tunnel and the other stops.
type Session struct {
	cfg  Config
	ep   endpoint
	cred *credentials
}

// Host is what goes in -h: always the real hostname, because libpq verifies the
// certificate against it and matches .pgpass on it (P-004).
func (s *Session) Host() string { return s.ep.host }

// Port is the tunnel's port when tunnelled, the real one otherwise.
func (s *Session) Port() int { return s.ep.port }

// Env is the environment for a client binary, including the path of a
// credentials file that exists only for the life of this session.
func (s *Session) Env(binPath string) []string { return s.cfg.env(s.cred, s.ep, binPath) }

// Close releases the tunnel and removes the credentials file. It runs from a
// defer, including on a panic, so a crashed job leaves no password on disk
// (ENF-022).
func (s *Session) Close() error {
	s.cred.remove()
	return s.ep.release()
}

// Open builds a session: a tunnel when the executor is not direct, then the
// credentials file, in that order.
//
// The order is the constraint P-004 established. libpq matches .pgpass on host
// AND port, the tunnel's port is chosen by the kernel, so the file can only be
// written once the listener is bound.
func (c Config) Open(ctx context.Context, ex executor.Executor) (*Session, error) {
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
	// host is always the real hostname, because libpq verifies the certificate
	// against it and matches .pgpass on it (P-004).
	host string
	// port is the tunnel's port when tunnelled, the real one otherwise.
	port int
	// hostAddr redirects the connection without changing what is verified. It
	// is empty when no tunnel is involved.
	hostAddr string

	close func() error
}

func (e endpoint) release() error {
	if e.close == nil {
		return nil
	}
	return e.close()
}

// credentials is a .pgpass file that exists only for one command.
type credentials struct {
	dir  string
	path string
}

// writeCredentials writes a .pgpass for one endpoint.
//
// It takes the endpoint rather than the Config because of P-004: libpq matches
// .pgpass on host AND port, and the tunnel's port is chosen by the kernel. A
// file written before the listener is bound carries the wrong port and fails
// with a misleading "no password supplied".
func writeCredentials(cfg Config, ep endpoint) (*credentials, error) {
	// MkdirTemp creates the directory 0700, which is what we want and why no
	// Chmod follows: on a shared host, the directory listing alone would say
	// which databases are being backed up.
	dir, err := os.MkdirTemp("", "koffr-pgpass-")
	if err != nil {
		return nil, fmt.Errorf("postgres: create credentials directory: %w", err)
	}

	// The database field is a wildcard, and that is deliberate.
	//
	// libpq matches .pgpass on host, port, database AND user, so a line naming
	// one database is a credential that works for exactly one connection. That
	// breaks two things Koffr genuinely does: pg_dumpall reads the cluster
	// through a maintenance database, and a restore targets a database chosen
	// at the command line, which is never the one that was dumped. Both failed
	// with "no password supplied" on configurations that were otherwise right.
	//
	// Narrowing it defends against nothing. The line grants the access the
	// configured user already has, in a file that is 0600 inside a 0700
	// directory and is removed when the job ends.
	path := filepath.Join(dir, ".pgpass")
	line := fmt.Sprintf("%s:%d:*:%s:%s\n",
		escapePgpass(ep.host), ep.port,
		escapePgpass(cfg.User), escapePgpass(cfg.Password))
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("postgres: write credentials file: %w", err)
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

// escapePgpass protects the field separator. A password containing a colon
// would otherwise silently truncate the line and fail to match.
func escapePgpass(s string) string {
	return strings.NewReplacer(`\`, `\\`, `:`, `\:`).Replace(s)
}

// env builds the environment for a client binary.
//
// It is a curated environment, not the operator's: an inherited PG* variable
// would silently change where a backup connects. PATH is set to the toolchain's
// own directory so that a tool which shells out finds its siblings there and
// nowhere else.
func (c *Config) env(cred *credentials, ep endpoint, binPath string) []string {
	env := []string{
		"PATH=" + filepath.Dir(binPath),
		"PGPASSFILE=" + cred.path,
		"PGSSLMODE=" + c.SSLMode,
		"PGCONNECT_TIMEOUT=" + strconv.Itoa(int(c.ConnectTimeout.Seconds())),
		"PGCLIENTENCODING=UTF8",
		// Stable, parseable diagnostics whatever the operator's locale.
		"LC_ALL=C",
		"LANG=C",
	}
	if c.SSLRootCert != "" {
		env = append(env, "PGSSLROOTCERT="+c.SSLRootCert)
	}
	if ep.hostAddr != "" {
		// The connection goes to the tunnel while -h keeps the real name, so
		// certificate verification and .pgpass matching both still work. This
		// is the arrangement P-004 confirmed.
		env = append(env, "PGHOSTADDR="+ep.hostAddr)
	}
	return env
}

var versionPattern = regexp.MustCompile(`(\d+)\.(\d+)`)

// toolVersion asks a client binary for its version.
//
// PD-006 and CT-001: pg_dump must be at least the server's major version, and a
// missing or older one is a configuration problem worth reporting while the
// configuration is being loaded.
func toolVersion(ctx context.Context, cfg Config, name string) (major int, err error) {
	path, err := cfg.ResolveBin(name)
	if err != nil {
		return 0, err
	}
	p, err := cfg.ToolRunner.Start(ctx, executor.Command{
		Path: path,
		Args: []string{"--version"},
	})
	if err != nil {
		return 0, fmt.Errorf("postgres: %s could not be run: %w", cfg.bin(name), err)
	}
	out, readErr := readAll(p)
	if waitErr := p.Wait(); waitErr != nil {
		return 0, fmt.Errorf("postgres: %s could not be run: %w", cfg.bin(name), waitErr)
	}
	if readErr != nil {
		return 0, fmt.Errorf("postgres: read %s version: %w", cfg.bin(name), readErr)
	}

	m := versionPattern.FindStringSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("postgres: %s reported an unrecognised version %q", cfg.bin(name), strings.TrimSpace(out))
	}
	major, err = strconv.Atoi(m[1])
	if err != nil {
		return 0, fmt.Errorf("postgres: %s reported an unrecognised version %q", cfg.bin(name), strings.TrimSpace(out))
	}
	return major, nil
}

var errNoOutput = errors.New("no output")

func readAll(p executor.Process) (string, error) {
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := p.Stdout().Read(buf)
		b.Write(buf[:n])
		if err != nil {
			if b.Len() == 0 {
				return "", errNoOutput
			}
			return b.String(), nil
		}
	}
}
