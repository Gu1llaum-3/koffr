package engine_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// ADR-0013 opened database/sql to this package so that it can probe a server.
// No import rule can express "for probes only": this test does, by reading the
// SQL this package can possibly send.
//
// The list below is the whole of what koffr asks a database it did not create.
// Adding to it is a decision, not an edit: the dump stays a sub-process, and
// reading the data to back up through a driver is what E-001 forbids.
var allowedStatements = []string{
	// What the server is, for E-041 and the compatibility matrix.
	"SELECT VERSION()",

	// Which tables are not transactional, for E-056. This reads the
	// **catalogue**, never a table koffr backs up: the difference between
	// metadata and data is the line ADR-0013 draws, and it holds here.
	// Added at the lot 2, wave 4 — deliberately, after this guard refused it.
	"SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND engine = 'MyISAM' ORDER BY table_name",

	// How big the database is, for E-061. With no previous backup to
	// extrapolate from, this is all the disk-space check has to work with.
	// Catalogue again, never a table koffr backs up.
	// Added at the lot 2, wave 5 — deliberately, after this guard refused them.
	"SELECT pg_database_size(current_database())",
	"SELECT COALESCE(SUM(data_length + index_length), 0) FROM information_schema.tables WHERE table_schema = DATABASE()",
}

// looksLikeSQL matches a string literal that would reach a server.
var looksLikeSQL = regexp.MustCompile(`(?i)^\s*(SELECT|SHOW|INSERT|UPDATE|DELETE|REPLACE|CREATE|DROP|ALTER|TRUNCATE|GRANT|COPY|CALL|SET|USE|LOCK|FLUSH)\b`)

func TestTheProbesSendNothingButTheirVersionQuery(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	fileSet := token.NewFileSet()

	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}

		parsed, err := parser.ParseFile(fileSet, source, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", source, err)
		}

		ast.Inspect(parsed, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}

			value, err := strconv.Unquote(literal.Value)
			if err != nil || !looksLikeSQL.MatchString(value) {
				return true
			}

			if !slices.Contains(allowedStatements, strings.TrimSpace(value)) {
				position := fileSet.Position(literal.Pos())
				t.Errorf("%s:%d sends %q.\n"+
					"  internal/engine may open a connection to probe a server, never to read\n"+
					"  the data koffr is there to dump — that stays a sub-process (ADR-0013).\n"+
					"  If this query really belongs to a probe, add it to allowedStatements and\n"+
					"  say why in the review.",
					source, position.Line, value)
			}

			return true
		})
	}
}

// And the guard is worth something: a statement that is not allowed is caught.
func TestTheQueryGuardCatchesAStatementItDoesNotKnow(t *testing.T) {
	for _, forbidden := range []string{
		"SELECT * FROM customers",
		"SHOW TABLES",
		"  select id from orders",
		"DROP DATABASE shop",
	} {
		if !looksLikeSQL.MatchString(forbidden) {
			t.Errorf("%q would slip past the guard", forbidden)
		}
		if slices.Contains(allowedStatements, strings.TrimSpace(forbidden)) {
			t.Errorf("%q is in the allowed list and should not be", forbidden)
		}
	}
}
