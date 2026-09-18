package arch

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	module   = "github.com/Gu1llaum-3/koffr"
	repoRoot = "../.."
	fixtures = "testdata/violations"
)

// The adapters a domain module must never reach for.
var adapters = []string{
	"internal/engine",
	"internal/store",
	"internal/pipeline",
	"internal/state",
	"internal/egress",
	"internal/httpd",
	"internal/cli",
}

// Which packages may import a network client. httpd is allowed net/http
// because it *serves* the local interface; it never calls out (N-14).
var networkClients = map[string][]string{
	"net":      {"internal/store", "internal/engine", "internal/egress"},
	"net/smtp": {"internal/store", "internal/engine", "internal/egress"},
	"net/http": {"internal/store", "internal/engine", "internal/egress", "internal/httpd"},
}

type rule struct {
	id       string
	what     string
	violated func(pkg, imp string) bool
}

// The dependency rules of ADR-0010 that an import graph can see. AR-05 is not
// one of them: it is about a call, os.Getenv, and forbidigo carries it (N-8).
var rules = []rule{
	{
		id:   "AR-01",
		what: "a domain module does not import an adapter",
		violated: func(pkg, imp string) bool {
			local, ok := localOf(imp)
			return ok && under(pkg, "internal/domain") &&
				slices.ContainsFunc(adapters, func(a string) bool { return under(local, a) })
		},
	},
	{
		id:   "AR-02",
		what: "a domain module does not import the network, a sub-process or a database",
		violated: func(pkg, imp string) bool {
			forbidden := []string{"net", "net/http", "net/smtp", "os/exec", "database/sql"}
			return under(pkg, "internal/domain") && slices.Contains(forbidden, imp)
		},
	},
	{
		id:   "AR-03",
		what: "a domain module does not know its neighbour; only shared is common",
		violated: func(pkg, imp string) bool {
			local, ok := localOf(imp)
			if !ok {
				return false
			}
			from, to := domainModule(pkg), domainModule(local)

			return from != "" && to != "" && to != from && to != "shared"
		},
	},
	{
		id:   "AR-04",
		what: "a transport does not import the local state; it goes through a use case",
		violated: func(pkg, imp string) bool {
			local, ok := localOf(imp)
			return ok && (under(pkg, "internal/httpd") || under(pkg, "internal/cli")) &&
				under(local, "internal/state")
		},
	},
	{
		id:   "AR-06",
		what: "only store, engine and egress open a network connection",
		violated: func(pkg, imp string) bool {
			allowed, isClient := networkClients[imp]

			return isClient && !slices.ContainsFunc(allowed, func(a string) bool { return under(pkg, a) })
		},
	},
	{
		id:   "AR-07",
		what: "only engine runs a sub-process",
		violated: func(pkg, imp string) bool {
			return imp == "os/exec" && !under(pkg, "internal/engine")
		},
	},
	{
		id:   "AR-08",
		what: "only state touches the database",
		violated: func(pkg, imp string) bool {
			forbidden := []string{"database/sql", "modernc.org/sqlite"}

			return slices.Contains(forbidden, imp) && !under(pkg, "internal/state")
		},
	},
	{
		id:   "AR-09",
		what: "protocol imports nothing from this repository",
		violated: func(pkg, imp string) bool {
			_, ok := localOf(imp)

			return ok && under(pkg, "protocol")
		},
	},
}

// The tree as it is respects every boundary.
func TestTheRepositoryRespectsEveryBoundary(t *testing.T) {
	for _, v := range check(scan(t, repoRoot)) {
		t.Errorf("%s — %s\n  %s imports %q", v.rule, v.what, v.file, v.imp)
	}
}

// And the check is worth something: a fixture that breaks each rule is caught.
func TestTheCheckCatchesAViolationOfEveryRule(t *testing.T) {
	found := check(scan(t, fixtures))
	if len(found) == 0 {
		t.Fatalf("no violation found under %s: the fixtures are missing", fixtures)
	}

	caught := map[string]bool{}
	for _, v := range found {
		caught[v.rule] = true
	}
	for _, r := range rules {
		if !caught[r.id] {
			t.Errorf("%s (%s) has no fixture that breaks it, or the check misses it", r.id, r.what)
		}
	}
}

// AR-05 — only config reads the environment — is about a call, not an import.
// The linter carries it (N-8); this test guards that the linter still does.
func TestTheEnvironmentRuleIsCarriedByTheLinter(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, ".golangci.yml"))
	if err != nil {
		t.Fatalf("read .golangci.yml: %v", err)
	}

	// Substrings, not the exact patterns: the file carries them escaped for a
	// regular expression ("^os\\.Getenv$").
	for _, want := range []string{"forbidigo", "Getenv", "LookupEnv", "internal/config"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf(".golangci.yml no longer mentions %q: AR-05 is no longer enforced", want)
		}
	}
}

type violation struct {
	rule, what, file, imp string
}

func check(imports []imported) []violation {
	var found []violation
	for _, i := range imports {
		for _, r := range rules {
			if r.violated(i.pkg, i.path) {
				found = append(found, violation{rule: r.id, what: r.what, file: i.file, imp: i.path})
			}
		}
	}

	return found
}

type imported struct {
	pkg  string // package directory, relative to the scanned root
	file string // file it was read from
	path string // the imported path
}

// scan reads the import block of every Go file under root. It parses rather
// than compiles, so the violating fixtures under testdata never have to build.
func scan(t *testing.T, root string) []imported {
	t.Helper()

	var found []imported
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".spike", "dist", "vendor":
				return fs.SkipDir
			case "testdata":
				if filepath.Clean(path) != filepath.Clean(root) {
					return fs.SkipDir
				}
			}

			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		pkg := filepath.ToSlash(filepath.Dir(rel))
		if pkg == "." {
			pkg = ""
		}
		for _, spec := range file.Imports {
			found = append(found, imported{
				pkg:  pkg,
				file: filepath.ToSlash(rel),
				path: strings.Trim(spec.Path.Value, `"`),
			})
		}

		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", root, err)
	}

	return found
}

// under reports whether pkg is prefix or lives inside it.
func under(pkg, prefix string) bool {
	return pkg == prefix || strings.HasPrefix(pkg, prefix+"/")
}

// localOf turns an import path of this module into a repository-relative one.
func localOf(imp string) (string, bool) {
	return strings.CutPrefix(imp, module+"/")
}

// domainModule names the domain module a package belongs to, or "".
func domainModule(pkg string) string {
	rest, ok := strings.CutPrefix(pkg, "internal/domain/")
	if !ok {
		return ""
	}

	return strings.SplitN(rest, "/", 2)[0]
}
