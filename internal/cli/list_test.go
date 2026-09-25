package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/domain/catalog"
)

// E-103c — `koffr list` shows what the catalogue holds, most recent first: an
// operator compares one listing to the last.
func TestListShowsTheArchivesOfTheCatalogue(t *testing.T) {
	site := newSite(t)
	book := &fakeCatalog{backups: threeArchives()}

	out := listWith(t, book, site.args("list")...)

	for _, want := range []string{"boutique", "erp", "01K5C", "01K5A"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing lost %q:\n%s", want, out)
		}
	}

	if strings.Index(out, "01K5C") > strings.Index(out, "01K5A") {
		t.Errorf("the listing is not most recent first:\n%s", out)
	}
}

// VRF-04, E-064 — a verified archive is **visually distinct** from one that is
// not, and the absence of a verification is never rendered as a success. This
// is read by somebody who has not followed the project.
func TestVRF04VerifiedAndUnverifiedAreVisuallyDistinct(t *testing.T) {
	site := newSite(t)
	book := &fakeCatalog{backups: threeArchives()}

	out := listWith(t, book, site.args("list")...)

	lines := map[string]string{}

	for _, line := range strings.Split(out, "\n") {
		for _, id := range []string{"01K5A", "01K5B", "01K5C"} {
			if strings.Contains(line, id) {
				lines[id] = line
			}
		}
	}

	if lines["01K5C"] == "" || lines["01K5B"] == "" || lines["01K5A"] == "" {
		t.Fatalf("the listing does not show the three archives:\n%s", out)
	}

	// The three states read differently, and none of them reads like a success
	// it is not.
	verified, failed, never := lines["01K5C"], lines["01K5B"], lines["01K5A"]

	if verified == failed || verified == never || failed == never {
		t.Errorf("two different states render the same way:\n%s", out)
	}

	if !strings.Contains(strings.ToLower(never), "no") && !strings.Contains(never, "—") {
		t.Errorf("an archive nobody verified does not say so:\n%s", never)
	}
	if !strings.Contains(strings.ToLower(failed), "fail") {
		t.Errorf("an archive whose verification failed does not say so:\n%s", failed)
	}
}

// E-103c — the listing narrows to one database, and to one destination.
func TestListNarrowsToADatabaseAndADestination(t *testing.T) {
	site := newSite(t)
	book := &fakeCatalog{backups: threeArchives()}

	out := listWith(t, book, site.args("list", "erp")...)
	if !strings.Contains(out, "erp") || strings.Contains(out, "boutique") {
		t.Errorf("the listing did not narrow to erp:\n%s", out)
	}

	if book.lastFilter.Database != "erp" {
		t.Errorf("the catalogue was asked for %q, want erp", book.lastFilter.Database)
	}

	_ = listWith(t, book, site.args("list", "--destination", "offsite")...)
	if book.lastFilter.Destination != "offsite" {
		t.Errorf("the catalogue was asked for the destination %q, want offsite", book.lastFilter.Destination)
	}
}

// An empty catalogue says so rather than printing a bare header that reads like
// a fleet with nothing in it.
func TestListOnAnEmptyCatalogueSaysSo(t *testing.T) {
	site := newSite(t)

	out := listWith(t, &fakeCatalog{}, site.args("list")...)

	if !strings.Contains(strings.ToLower(out), "no backup") {
		t.Errorf("an empty catalogue printed nothing useful:\n%s", out)
	}
}

func listWith(t *testing.T, book *fakeCatalog, args ...string) string {
	t.Helper()

	out, err := failingWith(t, book, args...)
	if err != nil {
		t.Fatalf("koffr %s: %v", strings.Join(args, " "), err)
	}

	return out
}

func threeArchives() []catalog.Backup {
	at := time.Date(2026, 9, 25, 2, 0, 3, 0, time.UTC)

	return []catalog.Backup{
		{
			ID: "01K5C", Database: "boutique", StartedAt: at.Add(48 * time.Hour),
			StoredBytes: 23101758, Verified: catalog.Structure, VerifiedAt: at.Add(48 * time.Hour),
			Locations: []catalog.Location{{Destination: "local", Path: "boutique/2026/09/a.pgc.zst.age"}},
		},
		{
			ID: "01K5B", Database: "erp", StartedAt: at.Add(24 * time.Hour),
			StoredBytes: 367181, Verified: catalog.Failed,
			Locations: []catalog.Location{{Destination: "local", Path: "erp/2026/09/b.sql.zst.age"}},
		},
		{
			ID: "01K5A", Database: "boutique", StartedAt: at,
			StoredBytes: 1821312, Verified: catalog.NotVerified,
			Locations: []catalog.Location{{Destination: "local", Path: "boutique/2026/09/c.pgc.zst.age"}},
		},
	}
}

// lastFilter is what the command asked the catalogue for.
func (f *fakeCatalog) recordFilter(filter catalog.Filter) { f.lastFilter = filter }
