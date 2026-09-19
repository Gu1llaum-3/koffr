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

// RSV-02 amended, from the acceptance session — A-09. On Debian and Ubuntu,
// /usr/bin/pg_dump is a symlink to pg_wrapper, which picks the version to run
// from its own argv[0]. Called by its resolved path it answers:
//
//	Can't exec "--version": No such file or directory at …/pg_wrapper line 153
//
// and koffr read "153" out of that and called it version 153.0. A candidate is
// dropped unless its answer **names the tool or its family**.
func TestACandidateThatNamesNothingIsDropped(t *testing.T) {
	refused := []string{
		`Can't exec "--version": No such file or directory at /usr/share/postgresql-common/pg_wrapper line 153`,
		"error: 42",
		"Usage: foo [options]",
		"1.2.3",
	}

	for _, answer := range refused {
		t.Run(answer[:min(len(answer), 24)], func(t *testing.T) {
			system := t.TempDir()
			fakeTool(t, filepath.Join(system, "pg_dump"), answer)

			found := discover(t, engine.FinderOptions{SystemPaths: []string{system}}, resolve.PostgreSQL, resolve.Dump)

			if len(found) != 0 {
				t.Errorf("a candidate that names nothing was kept: %+v", found)
			}
		})
	}
}

// And the answers of the real tools are still accepted — measured on the
// acceptance instance, not imagined.
func TestTheRealAnswersAreStillAccepted(t *testing.T) {
	accepted := map[string]struct {
		answer string
		family resolve.Family
		major  int
	}{
		"pg_dump":      {"pg_dump (PostgreSQL) 18.6 (Ubuntu 18.6-0ubuntu0.26.04.1)", resolve.PostgreSQL, 18},
		"mariadb-dump": {"mariadb-dump from 11.8.6-MariaDB, client 10.19 for debian-linux-gnu (aarch64)", resolve.MariaDB, 11},
		"mysqldump":    {"mysqldump  Ver 8.4.11 for Linux on aarch64 (MySQL Community Server - GPL)", resolve.MySQL, 8},
	}

	for binary, want := range accepted {
		t.Run(binary, func(t *testing.T) {
			system := t.TempDir()
			fakeTool(t, filepath.Join(system, binary), want.answer)

			found := discover(t, engine.FinderOptions{SystemPaths: []string{system}}, want.family, resolve.Dump)

			if len(found) != 1 {
				t.Fatalf("the answer of a real %s was refused: %+v", binary, found)
			}
			if found[0].Version.Major != want.major {
				t.Errorf("version = %s, want %d.x", found[0].Version, want.major)
			}
		})
	}
}

// N-1 — a tool reached through a symlink is run **by the path it was found at**,
// because that is the name a wrapper dispatches on, and reported under it.
func TestAToolIsRunByThePathItWasFoundAt(t *testing.T) {
	machine := t.TempDir()

	// A wrapper like Debian's: it answers according to how it was called.
	wrapper := filepath.Join(machine, "wrapper")
	script := "#!/bin/sh\n" +
		"case \"$(basename \"$0\")\" in\n" +
		"  pg_dump) echo 'pg_dump (PostgreSQL) 18.6' ;;\n" +
		"  *) echo 'Cannot exec at line 153' ;;\n" +
		"esac\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil { //nolint:gosec // a fixture
		t.Fatalf("write the wrapper: %v", err)
	}

	bin := filepath.Join(machine, "bin")
	if err := os.MkdirAll(bin, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(bin, "pg_dump")
	if err := os.Symlink(wrapper, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	found := discover(t, engine.FinderOptions{SystemPaths: []string{bin}}, resolve.PostgreSQL, resolve.Dump)

	if len(found) != 1 {
		t.Fatalf("the wrapper was not read through its link: %+v", found)
	}
	if found[0].Version.Major != 18 {
		t.Errorf("version = %s, want 18.6 — the wrapper was run under the wrong name", found[0].Version)
	}
	if found[0].Path != link {
		t.Errorf("path = %s, want %s — an operator recognises the path they installed", found[0].Path, link)
	}
}

// N-1 — and two paths to the same binary are still one candidate.
func TestTwoPathsToTheSameBinaryAreOneCandidate(t *testing.T) {
	machine := t.TempDir()

	first := filepath.Join(machine, "a")
	second := filepath.Join(machine, "b")
	for _, dir := range []string{first, second} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	real := filepath.Join(first, "pg_dump")
	fakeTool(t, real, "pg_dump (PostgreSQL) 16.10")
	if err := os.Symlink(real, filepath.Join(second, "pg_dump")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	found := discover(t, engine.FinderOptions{SystemPaths: []string{first, second}}, resolve.PostgreSQL, resolve.Dump)

	if len(found) != 1 {
		t.Errorf("the same binary was counted twice: %+v", found)
	}
}
