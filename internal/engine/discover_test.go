package engine_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
	"github.com/Gu1llaum-3/koffr/internal/engine"
)

// RSV-01 — every source is visited, and none is preferred at this stage. The
// § 5.2 warns that "priority to the host tools" is exactly how a 14 gets picked
// for a 16: preference is a tie-break, and it belongs to the resolver, not here.
func TestDiscoveryVisitsEverySource(t *testing.T) {
	system := t.TempDir()
	managed := t.TempDir()

	fakeTool(t, filepath.Join(system, "pg_dump"), "pg_dump (PostgreSQL) 14.11")
	fakeTool(t, filepath.Join(managed, "postgresql", "17", "bin", "pg_dump"), "pg_dump (PostgreSQL) 17.2")

	found := discover(t, engine.FinderOptions{
		SystemPaths: []string{system},
		ManagedDir:  managed,
	}, resolve.PostgreSQL, resolve.Dump)

	bySource := map[resolve.Source]resolve.Version{}
	for _, candidate := range found {
		bySource[candidate.Source] = candidate.Version
	}

	if got := bySource[resolve.Host]; got.Major != 14 {
		t.Errorf("the host candidate is %v, want 14 — found %+v", got, found)
	}
	if got := bySource[resolve.Managed]; got.Major != 17 {
		t.Errorf("the managed candidate is %v, want 17 — found %+v", got, found)
	}
}

// RSV-01 — a source that has nothing to offer is not an error. pg_lsclusters
// only exists on Debian and its derivatives; elsewhere it simply returns
// nothing, and discovery carries on.
func TestASourceThatHasNothingIsNotAnError(t *testing.T) {
	found := discover(t, engine.FinderOptions{
		SystemPaths: []string{filepath.Join(t.TempDir(), "never-created")},
		ManagedDir:  filepath.Join(t.TempDir(), "never-created"),
	}, resolve.PostgreSQL, resolve.Dump)

	for _, candidate := range found {
		if candidate.Source != resolve.Host {
			t.Errorf("a candidate appeared out of nowhere: %+v", candidate)
		}
	}
}

// RSV-02 — the version comes from running the tool. This is P3, and the whole
// compatibility matrix rests on it: a name is a claim, an execution is a fact.
func TestTheVersionComesFromRunningTheToolNotFromItsName(t *testing.T) {
	system := t.TempDir()
	fakeTool(t, filepath.Join(system, "pg_dump"), "pg_dump (PostgreSQL) 15.4")

	found := discover(t, engine.FinderOptions{SystemPaths: []string{system}}, resolve.PostgreSQL, resolve.Dump)

	if len(found) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(found), found)
	}
	if found[0].Version.Major != 15 || found[0].Version.Minor != 4 {
		t.Errorf("version = %s, want 15.4 — the name said 16", found[0].Version)
	}
}

// RSV-02 — and so does the family. MariaDB ships a binary called mysqldump;
// believing the name would hand a MariaDB dump to Oracle's tool, which E-041
// forbids. What the tool says about itself is what counts.
func TestTheFamilyOfAToolComesFromRunningIt(t *testing.T) {
	system := t.TempDir()
	fakeTool(t, filepath.Join(system, "mysqldump"), "mysqldump from 10.6.21-MariaDB, client 10.6")

	found := discover(t, engine.FinderOptions{SystemPaths: []string{system}}, resolve.MariaDB, resolve.Dump)

	if len(found) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(found), found)
	}
	if found[0].Family != resolve.MariaDB {
		t.Errorf("family = %q, want %q — a binary named mysqldump that says MariaDB is MariaDB's",
			found[0].Family, resolve.MariaDB)
	}
}

// RSV-02 — a candidate that cannot answer is dropped, never guessed at. An
// archive produced by a tool koffr could not identify is the "archive douteuse"
// P3 refuses.
func TestACandidateThatCannotAnswerIsDropped(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T, path string)
	}{
		{
			name: "not executable",
			build: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte("#!/bin/sh\necho 15.4\n"), 0o600); err != nil {
					t.Fatalf("write: %v", err)
				}
			},
		},
		{
			name:  "says something unreadable",
			build: func(t *testing.T, path string) { fakeTool(t, path, "I am a teapot") },
		},
		{
			name: "never answers",
			build: func(t *testing.T, path string) {
				script := "#!/bin/sh\nsleep 30\n"
				if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // a fixture
					t.Fatalf("write: %v", err)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			system := t.TempDir()
			c.build(t, filepath.Join(system, "pg_dump"))

			found := discover(t, engine.FinderOptions{
				SystemPaths: []string{system},
				Timeout:     500 * time.Millisecond,
			}, resolve.PostgreSQL, resolve.Dump)

			if len(found) != 0 {
				t.Errorf("a candidate koffr cannot identify was kept: %+v", found)
			}
		})
	}
}

// RSV-03 — the second look runs nothing. doctor walks a fleet, and executing
// every candidate of every database twice is time an operator waits for.
func TestTheSecondLookRunsNothing(t *testing.T) {
	system := t.TempDir()
	path := filepath.Join(system, "pg_dump")
	counter := filepath.Join(system, "runs")
	countingTool(t, path, counter, "pg_dump (PostgreSQL) 16.10")

	finder := engine.NewFinder(engine.FinderOptions{SystemPaths: []string{system}, LookPath: noPath})
	for range 3 {
		if _, err := finder.Find(t.Context(), resolve.PostgreSQL, resolve.Dump); err != nil {
			t.Fatalf("Find: %v", err)
		}
	}

	if runs := countRuns(t, counter); runs != 1 {
		t.Errorf("the tool ran %d times, want 1 — the cache did not hold", runs)
	}
}

// RSV-03 — and a binary that changed is looked at again, so that a freshly
// installed tool is seen without restarting koffr.
func TestAChangedBinaryIsLookedAtAgain(t *testing.T) {
	system := t.TempDir()
	path := filepath.Join(system, "pg_dump")
	fakeTool(t, path, "pg_dump (PostgreSQL) 15.4")

	finder := engine.NewFinder(engine.FinderOptions{SystemPaths: []string{system}, LookPath: noPath})

	first, err := finder.Find(t.Context(), resolve.PostgreSQL, resolve.Dump)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if first[0].Version.Major != 15 {
		t.Fatalf("first look = %s, want 15.4", first[0].Version)
	}

	// The same path, a different tool — which is what `tools install` does.
	fakeTool(t, path, "pg_dump (PostgreSQL) 17.2")
	if err := os.Chtimes(path, time.Now().Add(time.Second), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("touch: %v", err)
	}

	second, err := finder.Find(t.Context(), resolve.PostgreSQL, resolve.Dump)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if second[0].Version.Major != 17 {
		t.Errorf("second look = %s, want 17.2 — the cache outlived the binary", second[0].Version)
	}
}

// A finder is a ToolFinder, checked by the compiler.
func TestTheFinderImplementsThePortTheDomainDeclares(t *testing.T) {
	var _ resolve.ToolFinder = engine.NewFinder(engine.FinderOptions{})
}

// noPath describes a machine whose PATH holds none of these tools, so that a
// test measures what it set up and not what the developer happens to have
// installed.
func noPath(string) (string, error) {
	return engine.NoPath("")
}

var errNotOnPath = errors.New("not on PATH")

func discover(t *testing.T, options engine.FinderOptions, family resolve.Family, tool resolve.Tool) []resolve.Candidate {
	t.Helper()

	options.LookPath = noPath

	found, err := engine.NewFinder(options).Find(t.Context(), family, tool)
	if err != nil && !errors.Is(err, resolve.ErrUnsupportedEngine) {
		t.Fatalf("Find: %v", err)
	}

	return found
}

// fakeTool writes an executable that answers --version like the real thing.
func fakeTool(t *testing.T, path, answer string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	script := "#!/bin/sh\necho '" + answer + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // a fixture
		t.Fatalf("write %s: %v", path, err)
	}
}

// countingTool answers like fakeTool and records that it ran.
func countingTool(t *testing.T, path, counter, answer string) {
	t.Helper()

	script := "#!/bin/sh\necho x >> '" + counter + "'\necho '" + answer + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // a fixture
		t.Fatalf("write %s: %v", path, err)
	}
}

func countRuns(t *testing.T, counter string) int {
	t.Helper()

	raw, err := os.ReadFile(counter)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read %s: %v", counter, err)
	}

	return len(slices.DeleteFunc(strings.Split(strings.TrimSpace(string(raw)), "\n"), func(s string) bool {
		return s == ""
	}))
}

// RSV-01 — pg_lsclusters is a source of its own. On Debian and its derivatives
// it knows which PostgreSQL majors are installed, including ones whose bin
// directory a glob would miss.
func TestDiscoveryAsksPgLsclustersWhenItIsThere(t *testing.T) {
	machine := t.TempDir()

	// The real pg_lsclusters prints a header and one line per cluster.
	lsclusters := filepath.Join(machine, "pg_lsclusters")
	fakeTool(t, lsclusters, "Ver Cluster Port Status Owner    Data directory\n"+
		"16  main    5432 online postgres /var/lib/postgresql/16/main\n"+
		"17  main    5433 online postgres /var/lib/postgresql/17/main")

	clusters := t.TempDir()
	fakeTool(t, filepath.Join(clusters, "17", "bin", "pg_dump"), "pg_dump (PostgreSQL) 17.2")

	found := discoverWith(t, engine.FinderOptions{
		SystemPaths: []string{filepath.Join(t.TempDir(), "empty")},
		ClusterRoot: clusters,
		LookPath: func(name string) (string, error) {
			if name == "pg_lsclusters" {
				return lsclusters, nil
			}

			return "", errNotOnPath
		},
	}, resolve.PostgreSQL, resolve.Dump)

	if len(found) != 1 {
		t.Fatalf("got %d candidates, want the one pg_lsclusters pointed at: %+v", len(found), found)
	}
	if found[0].Version.Major != 17 {
		t.Errorf("version = %s, want 17.2", found[0].Version)
	}
}

// And a machine without pg_lsclusters is not a machine with a problem.
func TestDiscoveryWithoutPgLsclustersIsFine(t *testing.T) {
	system := t.TempDir()
	fakeTool(t, filepath.Join(system, "pg_dump"), "pg_dump (PostgreSQL) 16.10")

	found := discover(t, engine.FinderOptions{SystemPaths: []string{system}}, resolve.PostgreSQL, resolve.Dump)

	if len(found) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(found), found)
	}
}

// discoverWith runs a finder whose options the caller fully controls.
func discoverWith(t *testing.T, options engine.FinderOptions, family resolve.Family, tool resolve.Tool) []resolve.Candidate {
	t.Helper()

	found, err := engine.NewFinder(options).Find(t.Context(), family, tool)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}

	return found
}
