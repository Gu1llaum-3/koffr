package backup_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/Gu1llaum-3/koffr/internal/domain/backup"
)

// BKP-19 — a job and its archive are identified by a **ULID**, as ADR-0006
// fixes. What koffr produced before only looked like one: a hexadecimal
// timestamp glued to base32 randomness, 27 characters long because %010X sets
// a minimum width and not a maximum (A-14).
func TestBKP19AnIdentifierIsARealULID(t *testing.T) {
	id := backup.NewJobID()

	if len(id) != 26 {
		t.Errorf("the identifier is %d characters long, want the 26 of a ULID: %s", len(id), id)
	}

	parsed, err := ulid.ParseStrict(id)
	if err != nil {
		t.Fatalf("the identifier is not a ULID: %v", err)
	}

	// And the timestamp can be read back out of it, which is the point of a
	// public format: any tool can tell when an archive was written.
	written := ulid.Time(parsed.Time())
	if written.IsZero() {
		t.Error("the identifier carries no timestamp")
	}
}

// BKP-19 — identifiers made in the same millisecond are **different and
// ordered**. The old format left them to sort on their random part, which is to
// say at random; the catalogue of the lot 3 will list archives by identifier.
func TestBKP19IdentifiersMadeTogetherStaySorted(t *testing.T) {
	const count = 64

	made := make([]string, 0, count)
	for range count {
		made = append(made, backup.NewJobID())
	}

	sorted := slices.Clone(made)
	slices.Sort(sorted)

	if !slices.Equal(made, sorted) {
		t.Errorf("identifiers produced in order do not sort in order:\n produced %v\n trié     %v",
			made[:4], sorted[:4])
	}

	if len(slices.Compact(sorted)) != count {
		t.Error("two identifiers collided")
	}
}

// `N-2` — the entropy is cryptographic and nothing panics. ulid.Make is the
// convenient call and the wrong one: it draws from math/rand — a regression on
// what koffr did before — and goes through MustNew, which panics when the
// source fails. A backup agent does not panic at two in the morning.
func TestNothingCallsTheConvenientULIDHelpers(t *testing.T) {
	forbidden := map[string]string{
		"Make":           "ulid.New with ulid.Monotonic(crypto/rand.Reader, 0)",
		"MustNew":        "ulid.New, which returns an error",
		"MustNewDefault": "ulid.New, which returns an error",
		"MustParse":      "ulid.Parse, which returns an error",
	}

	fileSet := token.NewFileSet()

	err := filepath.WalkDir(filepath.Join("..", "..", ".."), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if slices.Contains([]string{".git", "dist", ".spike", "testdata"}, entry.Name()) {
				return fs.SkipDir
			}

			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}

		parsed, parseErr := parser.ParseFile(fileSet, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}

		ast.Inspect(parsed, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			receiver, ok := selector.X.(*ast.Ident)
			if !ok || receiver.Name != "ulid" {
				return true
			}

			if instead, banned := forbidden[selector.Sel.Name]; banned {
				t.Errorf("%s:%d calls ulid.%s.\n  Use %s (`N-2`).",
					path, fileSet.Position(selector.Pos()).Line, selector.Sel.Name, instead)
			}

			return true
		})

		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
