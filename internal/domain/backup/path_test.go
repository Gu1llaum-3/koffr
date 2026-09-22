package backup_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/domain/backup"
)

// BKP-10 — the path of an archive is deterministic and readable. § 5.5 F5.5
// asks for it so that a repository stays usable **if the agent disappears**:
// someone with the bucket and the private key must be able to tell what an
// archive is without a catalogue, without koffr, without anything.
func TestBKP10TheArchivePathIsDeterministicAndReadable(t *testing.T) {
	at := time.Date(2026, time.September, 21, 2, 0, 3, 0, time.UTC)

	path := backup.ArchivePath("boutique-prod", at, "01JQ8F3K2M7X9P4W", "zst.age")

	if want := "boutique-prod/2026/09/"; !strings.HasPrefix(path, want) {
		t.Errorf("path = %q, want it to start with %q", path, want)
	}
	for _, want := range []string{"boutique-prod", "2026", "09", "01JQ8F3K2M7X9P4W", ".zst.age"} {
		if !strings.Contains(path, want) {
			t.Errorf("path = %q, want it to carry %q", path, want)
		}
	}

	// Deterministic: the same inputs give the same path, always.
	if again := backup.ArchivePath("boutique-prod", at, "01JQ8F3K2M7X9P4W", "zst.age"); again != path {
		t.Errorf("two calls gave %q and %q", path, again)
	}
}

// BKP-10 — the timestamp is readable and sorts. A directory listing in
// chronological order is what someone reads when koffr is gone.
func TestBKP10TheTimestampSortsAndReads(t *testing.T) {
	first := backup.ArchivePath("shop", time.Date(2026, time.September, 21, 2, 0, 0, 0, time.UTC), "a", "zst.age")
	later := backup.ArchivePath("shop", time.Date(2026, time.September, 21, 3, 0, 0, 0, time.UTC), "b", "zst.age")

	if first >= later {
		t.Errorf("%q does not sort before %q", first, later)
	}
}

// BKP-10 — nothing of the connection ends up in a path someone else can read.
func TestBKP10ThePathCarriesNoCredential(t *testing.T) {
	path := backup.ArchivePath("shop", time.Now(), "01JQ8F3K2M7X9P4W", "zst.age")

	for _, unwanted := range []string{"password", "@", ":5432", "user"} {
		if strings.Contains(path, unwanted) {
			t.Errorf("path = %q carries %q", path, unwanted)
		}
	}
}

// A database identifier with awkward characters does not escape its directory.
func TestBKP10AnAwkwardIdentifierStaysInItsPlace(t *testing.T) {
	for _, id := range []string{"../../etc/passwd", "shop/../..", "a b"} {
		path := backup.ArchivePath(id, time.Now(), "x", "zst.age")

		if strings.Contains(path, "..") {
			t.Errorf("identifier %q produced %q, which climbs out", id, path)
		}
	}
}

// BKP-10, étendue — the extension says what the file **is**, in the order you
// undo it. `boutique_….pgc` named an archive that was compressed and encrypted;
// `pg_restore --list` on it answered "input file does not appear to be a valid
// tar archive", which orients nobody (A-13).
func TestBKP10TheExtensionCarriesTheStackThatWasApplied(t *testing.T) {
	cases := []struct {
		dump     string
		pipeline []string
		want     string
	}{
		{"pgc", []string{"zstd:3", "age:x25519"}, "pgc.zst.age"},
		{"sql", []string{"zstd:3", "age:x25519"}, "sql.zst.age"},
		// A stack that changes changes the name with it — the point of
		// building this from what the pipeline applied rather than writing it
		// in by hand (`N-3`).
		{"pgc", []string{"age:x25519"}, "pgc.age"},
		{"pgc", nil, "pgc"},
	}

	for _, c := range cases {
		if got := backup.ArchiveExtension(c.dump, c.pipeline); got != c.want {
			t.Errorf("ArchiveExtension(%q, %v) = %q, want %q", c.dump, c.pipeline, got, c.want)
		}
	}
}

// And the README procedure follows from the name: age first, then zstd.
func TestBKP10TheExtensionReadsInTheOrderYouUndoIt(t *testing.T) {
	name := backup.ArchivePath("boutique", time.Date(2026, 9, 22, 2, 0, 3, 0, time.UTC),
		"01JQ8F3K2M7X9P4W", backup.ArchiveExtension("pgc", []string{"zstd:3", "age:x25519"}))

	if !strings.HasSuffix(name, ".pgc.zst.age") {
		t.Errorf("the archive is not named after what it is: %s", name)
	}
}
