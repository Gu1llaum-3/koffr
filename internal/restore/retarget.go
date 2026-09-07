package restore

import (
	"bufio"
	"errors"
	"io"
	"regexp"
	"strings"
)

// retarget rewrites the database a mariadb-dump lands in.
//
// It exists because --into was a lie. A dump taken with --databases carries its
// own CREATE DATABASE and USE, and the USE inside the stream beats the
// --database given to the client: restoring "into" another name created the
// empty target, put every row back into the database the backup came from, and
// reported success. On a source that had moved on since the backup, that is a
// silent revert of live data by a command whose whole purpose was to avoid
// touching it.
//
// Rewriting rather than stripping, because the CREATE DATABASE carries the
// character set and collation. Dropping it would land the data in a database
// created with whatever the target server's defaults happen to be, which is a
// quieter kind of wrong.
//
// Bounded to the header: rewriting stops at the first statement that carries
// data or schema, so nothing in the body can be touched however it is spelled.
func retarget(r io.Reader, database string) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		_ = pw.CloseWithError(rewriteHeader(r, pw, database))
	}()
	return pr
}

var (
	// (?m) is load-bearing: the lines carry their trailing newline, and
	// without it "$" only matches the very end of the text.
	createDatabaseStmt = regexp.MustCompile("(?im)^(CREATE DATABASE\\s.*?)`[^`]*`(.*)$")
	useStmt            = regexp.MustCompile("(?im)^USE\\s+`[^`]*`\\s*;\\s*$")
	// bodyBegins is the first thing that is no longer header. mariadb-dump
	// emits the database statements before any of these, always.
	bodyBegins = regexp.MustCompile(`(?im)^(CREATE TABLE|INSERT |REPLACE |LOCK TABLES|/\*!40000 ALTER TABLE)`)
)

func rewriteHeader(r io.Reader, w io.Writer, database string) error {
	name := "`" + strings.ReplaceAll(database, "`", "``") + "`"

	br := bufio.NewReaderSize(r, 1<<20)
	done := false
	for !done {
		line, err := br.ReadString('\n')
		if line != "" {
			switch {
			case bodyBegins.MatchString(line):
				// From here on the stream is copied untouched, which is what
				// keeps a row of data from ever being rewritten -- whatever it
				// contains.
				done = true
			case createDatabaseStmt.MatchString(line):
				line = createDatabaseStmt.ReplaceAllString(line, "${1}"+name+"${2}")
			case useStmt.MatchString(line):
				line = "USE " + name + ";\n"
			}
			if _, werr := io.WriteString(w, line); werr != nil {
				return werr
			}
		}
		if err != nil {
			return ignoreEOF(err)
		}
	}
	_, err := io.Copy(w, br)
	return err
}

func ignoreEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
