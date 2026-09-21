package backup

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// timestampLayout is readable by a human and sorts as text, which is what a
// directory listing needs when nobody has a catalogue any more.
const timestampLayout = "20060102T150405Z"

// unsafeInPath is everything koffr refuses to put in a path it writes. A
// database identifier comes from a configuration file, and a path that climbs
// out of its directory is how a backup overwrites something else.
var unsafeInPath = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// ArchivePath is where an archive goes, on every destination:
//
//	<database>/<YYYY>/<MM>/<database>_<timestamp>_<id>.<ext>
//
// § 5.5 F5.5 asks for it to be deterministic and readable by a human, so that
// a repository stays usable **if the agent disappears**. Someone holding the
// bucket and the private key must be able to tell what an archive is without a
// catalogue and without koffr — which is why the database and the date are in
// the name and not only in a manifest.
func ArchivePath(database string, at time.Time, id, extension string) string {
	safe := safeSegment(database)
	when := at.UTC()

	return fmt.Sprintf("%s/%04d/%02d/%s_%s_%s.%s",
		safe,
		when.Year(), int(when.Month()),
		safe, when.Format(timestampLayout), safeSegment(id),
		strings.TrimPrefix(extension, "."),
	)
}

// safeSegment keeps an identifier inside its own directory.
func safeSegment(of string) string {
	cleaned := unsafeInPath.ReplaceAllString(of, "-")
	cleaned = strings.Trim(strings.ReplaceAll(cleaned, "..", "-"), "-.")

	if cleaned == "" {
		return "unnamed"
	}

	return cleaned
}
