package engine

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
)

// defaultTimeout bounds one `--version`. doctor walks a fleet, and a binary
// that hangs must not hang the diagnostic with it.
const defaultTimeout = 5 * time.Second

// defaultClusterRoot is where Debian and its derivatives put one bin directory
// per PostgreSQL major.
const defaultClusterRoot = "/usr/lib/postgresql"

// binaryNames are the names a tool goes by, per family and operation. MariaDB
// answers to both mariadb-dump and the mysqldump it has shipped for years —
// which is exactly why the family is read from what the binary says, not from
// the name koffr found it under (E-041, RSV-02).
var binaryNames = map[resolve.Family]map[resolve.Tool][]string{
	resolve.PostgreSQL: {
		resolve.Dump:    {"pg_dump"},
		resolve.Restore: {"pg_restore"},
	},
	resolve.MySQL: {
		resolve.Dump:    {"mysqldump"},
		resolve.Restore: {"mysql"},
	},
	resolve.MariaDB: {
		resolve.Dump:    {"mariadb-dump", "mysqldump"},
		resolve.Restore: {"mariadb", "mysql"},
	},
}

// systemPaths are the places distributions put these tools. Globs are expanded:
// Debian keeps one directory per major version, and so does RHEL.
var systemPaths = []string{
	"/usr/bin",
	"/usr/local/bin",
	"/usr/lib/postgresql/*/bin", // Debian, Ubuntu
	"/usr/pgsql-*/bin",          // RHEL, Rocky, Fedora
	"/usr/local/mysql/bin",
	"/opt/homebrew/bin",
	"/opt/homebrew/opt/*/bin", // macOS
}

// NoPath is a LookPath that finds nothing. It describes a machine whose PATH
// holds none of these tools — which is what `tools list --search-path` means,
// and what a test needs to measure what it set up rather than what the
// developer happens to have installed.
func NoPath(string) (string, error) {
	return "", errNotOnPath
}

var errNotOnPath = errors.New("not on PATH")

// FinderOptions configures discovery. Its zero value looks at the system paths
// of the host and nothing else, which is what production wants.
type FinderOptions struct {
	// SystemPaths overrides the built-in list. Tests use it; production does
	// not.
	SystemPaths []string

	// ManagedDir is where `koffr tools install` puts what it installs.
	ManagedDir string

	// ClusterRoot is where the PostgreSQL packaging of Debian and its
	// derivatives keeps one directory per major version. pg_lsclusters names
	// the majors; this is where their binaries live.
	ClusterRoot string

	// Timeout bounds one `--version`.
	Timeout time.Duration

	// LookPath asks PATH for a binary. Injected so that a test can describe a
	// machine rather than inherit the one it runs on — without it, discovery
	// finds whatever Homebrew installed on the developer's laptop.
	LookPath func(string) (string, error)
}

// Finder enumerates tools. It caches what it learned for the life of the
// process, keyed by path and invalidated when the binary changes (RSV-03, N-4).
type Finder struct {
	options FinderOptions

	mutex sync.Mutex
	known map[string]cached
}

type cached struct {
	modified time.Time
	size     int64
	family   resolve.Family
	version  resolve.Version
	usable   bool
}

// NewFinder builds the adapter that implements resolve.ToolFinder.
func NewFinder(options FinderOptions) *Finder {
	if options.SystemPaths == nil {
		options.SystemPaths = systemPaths
	}
	if options.Timeout <= 0 {
		options.Timeout = defaultTimeout
	}
	if options.LookPath == nil {
		options.LookPath = exec.LookPath
	}
	if options.ClusterRoot == "" {
		options.ClusterRoot = defaultClusterRoot
	}

	return &Finder{options: options, known: map[string]cached{}}
}

// Find enumerates every source, runs each candidate, and returns those that
// answered. It expresses no preference: the resolver chooses (E-038).
func (f *Finder) Find(ctx context.Context, family resolve.Family, tool resolve.Tool) ([]resolve.Candidate, error) {
	names, known := binaryNames[family][tool]
	if !known {
		return nil, resolve.ErrUnsupportedEngine
	}

	var found []resolve.Candidate

	for path, source := range f.locations(names) {
		candidate, usable := f.identify(ctx, path, source, tool)
		if usable && candidate.Family == family {
			found = append(found, candidate)
		}
	}

	return found, nil
}

// locations lists every path worth trying, with where it came from. A path that
// appears twice is tried once: the same binary reached through two sources is
// one candidate.
func (f *Finder) locations(names []string) map[string]resolve.Source {
	locations := map[string]resolve.Source{}

	// Deduplicated by the resolved path — the same binary reached two ways is
	// one candidate — but kept and executed under the path it was **found** at.
	// Debian's pg_wrapper picks the version to run from its own argv[0]:
	// resolving the link takes away the only thing it goes by (N-1, A-09).
	seen := map[string]bool{}

	add := func(path string, source resolve.Source) {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return
		}
		if seen[resolved] {
			return
		}
		seen[resolved] = true
		locations[path] = source
	}

	for _, name := range names {
		// The managed directory first, so that a tool koffr installed keeps
		// that provenance even when it is also reachable another way.
		for _, path := range f.managedCandidates(name) {
			add(path, resolve.Managed)
		}

		for _, pattern := range f.options.SystemPaths {
			directories, err := filepath.Glob(pattern)
			if err != nil {
				continue
			}
			for _, directory := range directories {
				add(filepath.Join(directory, name), resolve.Host)
			}
		}

		// What pg_lsclusters knows, when the machine has it. § 5.2 F2.1 names
		// it as a source of its own: on Debian it lists the majors that are
		// actually installed, which is more reliable than a glob.
		for _, path := range f.clusterCandidates(name) {
			add(path, resolve.Host)
		}

		// PATH, asked the way a shell would. AR-05 reserves reading the
		// environment to internal/config; LookPath answers what would actually
		// run, which is the question anyway.
		if path, err := f.options.LookPath(name); err == nil {
			add(path, resolve.Host)
		}
	}

	return locations
}

// managedCandidates lists /var/lib/koffr/tools/<engine>/<version>/bin/<name>.
func (f *Finder) managedCandidates(name string) []string {
	if f.options.ManagedDir == "" {
		return nil
	}

	matches, err := filepath.Glob(filepath.Join(f.options.ManagedDir, "*", "*", "bin", name))
	if err != nil {
		return nil
	}

	return matches
}

// clusterCandidates asks pg_lsclusters which PostgreSQL majors are installed,
// and points at their binaries. A machine without it simply has no candidates
// from this source, which is not a failure.
func (f *Finder) clusterCandidates(name string) []string {
	if !strings.HasPrefix(name, "pg_") {
		return nil
	}

	lsclusters, err := f.options.LookPath("pg_lsclusters")
	if err != nil {
		return nil
	}

	bounded, cancel := context.WithTimeout(context.Background(), f.options.Timeout)
	defer cancel()

	command := exec.CommandContext(bounded, lsclusters)
	command.WaitDelay = time.Second

	out, err := command.Output()
	if err != nil {
		return nil
	}

	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		// The first column is the major version; the header says "Ver".
		major := resolve.ParseVersion(fields[0])
		if major.IsZero() {
			continue
		}

		paths = append(paths, filepath.Join(f.options.ClusterRoot, fields[0], "bin", name))
	}

	return paths
}

// identify runs a candidate to learn what it is, or reuses what it learned.
func (f *Finder) identify(ctx context.Context, path string, source resolve.Source, tool resolve.Tool) (resolve.Candidate, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return resolve.Candidate{}, false
	}

	f.mutex.Lock()
	remembered, seen := f.known[path]
	f.mutex.Unlock()

	if !seen || !remembered.modified.Equal(info.ModTime()) || remembered.size != info.Size() {
		remembered = f.run(ctx, path, info)

		f.mutex.Lock()
		f.known[path] = remembered
		f.mutex.Unlock()
	}

	if !remembered.usable {
		return resolve.Candidate{}, false
	}

	return resolve.Candidate{
		Family:  remembered.family,
		Tool:    tool,
		Path:    path,
		Version: remembered.version,
		Source:  source,
	}, true
}

// run executes `--version` and reads the answer. A candidate that cannot be
// executed, answers something unreadable, or does not answer in time is marked
// unusable — never guessed at (RSV-02).
func (f *Finder) run(ctx context.Context, path string, info os.FileInfo) cached {
	unusable := cached{modified: info.ModTime(), size: info.Size()}

	bounded, cancel := context.WithTimeout(ctx, f.options.Timeout)
	defer cancel()

	var out bytes.Buffer

	command := exec.CommandContext(bounded, path, "--version")
	command.Stdout = &out
	command.Stderr = &out
	// Killing the process is not enough to return: a shell that spawned a child
	// leaves the output pipe open, and Wait would block until that child ends.
	// WaitDelay gives up on the pipe shortly after the kill.
	command.WaitDelay = time.Second

	if err := command.Run(); err != nil {
		return unusable
	}

	announced := strings.TrimSpace(out.String())

	version := resolve.ParseVersion(announced)
	if version.IsZero() {
		return unusable
	}

	family, named := toolFamily(announced)
	if !named {
		// The answer contains a number and nothing that says what answered.
		// That is how "at ... line 153" became a version 153.0 (A-09): a
		// candidate koffr cannot identify is dropped, never guessed at.
		return unusable
	}

	return cached{
		modified: info.ModTime(),
		size:     info.Size(),
		family:   family,
		version:  version,
		usable:   true,
	}
}

// toolFamily reads the family out of what the tool said about **itself**, and
// says so when it could not. The binary name gets no vote at all: it is what
// turned pg_wrapper into a PostgreSQL candidate (A-09), and believing a name
// called mysqldump would hand a MariaDB dump to Oracle's tool, which E-041
// forbids.
//
// The markers are the ones the real tools print, measured on the acceptance
// instance:
//
//	pg_dump (PostgreSQL) 18.6
//	mariadb-dump from 11.8.6-MariaDB, client 10.19
//	mysqldump  Ver 8.4.11 for Linux (MySQL Community Server - GPL)
func toolFamily(announced string) (resolve.Family, bool) {
	lowered := strings.ToLower(announced)

	switch {
	case strings.Contains(lowered, "mariadb"):
		return resolve.MariaDB, true

	case strings.Contains(lowered, "postgresql"),
		strings.Contains(lowered, "pg_dump"), strings.Contains(lowered, "pg_restore"):
		return resolve.PostgreSQL, true

	case strings.Contains(lowered, "mysql"):
		return resolve.MySQL, true

	default:
		return "", false
	}
}
