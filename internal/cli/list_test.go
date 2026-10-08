package cli

import (
	"fmt"
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

	_ = listWith(t, book, site.args("list", "--destination", "local")...)
	if book.lastFilter.Destination != "local" {
		t.Errorf("the catalogue was asked for the destination %q, want local", book.lastFilter.Destination)
	}
}

// CAT-07, A-27 — a destination the configuration never declared is a
// **refusal** naming the ones it does. Answering "nothing" to a typo read
// exactly like an empty repository, with the return code of a success.
func TestListRefusesADestinationNobodyDeclared(t *testing.T) {
	site := newSite(t)
	book := &fakeCatalog{backups: threeArchives()}

	out, err := failingWith(t, book, site.args("list", "--destination", "offsite")...)
	if err == nil {
		t.Fatalf("an unknown destination was answered with a listing:\n%s", out)
	}

	if !strings.Contains(err.Error(), "local") {
		t.Errorf("the refusal does not name the destinations that exist: %v", err)
	}
}

// CAT-06, A-19 — an archive the destination holds and the catalogue ignores is
// **shown**, and says so. An operator who upgrades keeps sight of what an
// earlier release wrote.
func TestListShowsAnArchiveTheCatalogueNeverRecorded(t *testing.T) {
	site := newSite(t)
	book := &fakeCatalog{backups: threeArchives()}
	site.place(t, book.backups...)

	// Written by an earlier release: on the disk, with no manifest beside it.
	stranger := archivePathOf("boutique", "01K5OLD",
		time.Date(2026, 9, 20, 2, 0, 3, 0, time.UTC), "pgc")
	site.placeFile(t, stranger, 4096)

	out := listWith(t, book, site.args("list")...)

	line := lineOf(out, "01K5OLD")
	if line == "" {
		t.Fatalf("the archive the catalogue never recorded is not listed:\n%s", out)
	}
	if !strings.Contains(line, "boutique") {
		t.Errorf("its database is not read from its path:\n%s", line)
	}
	if !strings.Contains(line, "not in the catalogue") {
		t.Errorf("it does not say the catalogue ignores it:\n%s", line)
	}

	// And it does not read like a verified archive, nor like a failed one.
	for _, forbidden := range []string{"yes,", "FAILED"} {
		if strings.Contains(line, forbidden) {
			t.Errorf("it reads like %q, and koffr knows nothing about it:\n%s", forbidden, line)
		}
	}
}

// And a catalogue entry whose archive has left the destination says **that**.
func TestListSaysWhenTheDestinationNoLongerHoldsTheArchive(t *testing.T) {
	site := newSite(t)
	book := &fakeCatalog{backups: threeArchives()}
	site.place(t, book.backups[1:]...) // the most recent one never reached the disk

	out := listWith(t, book, site.args("list")...)

	if line := lineOf(out, "01K5C"); !strings.Contains(line, "gone") {
		t.Errorf("an archive the destination no longer holds does not say so:\n%s", line)
	}
	if line := lineOf(out, "01K5B"); strings.Contains(line, "gone") {
		t.Errorf("an archive that is there is reported gone:\n%s", line)
	}
}

func lineOf(out, id string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, id) {
			return line
		}
	}

	return ""
}

// An empty catalogue says so rather than printing a bare header that reads like
// a fleet with nothing in it.
func TestListOnAnEmptyCatalogueSaysSo(t *testing.T) {
	site := newSite(t)

	out := listWith(t, &fakeCatalog{}, site.args("list")...)

	if !strings.Contains(strings.ToLower(out), "no archive") {
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
			Locations: []catalog.Location{{
				Destination: "local",
				Path:        archivePathOf("boutique", "01K5C", at.Add(48*time.Hour), "pgc"),
			}},
		},
		{
			ID: "01K5B", Database: "erp", StartedAt: at.Add(24 * time.Hour),
			StoredBytes: 367181, Verified: catalog.Failed,
			Locations: []catalog.Location{{
				Destination: "local",
				Path:        archivePathOf("erp", "01K5B", at.Add(24*time.Hour), "sql"),
			}},
		},
		{
			ID: "01K5A", Database: "boutique", StartedAt: at,
			StoredBytes: 1821312, Verified: catalog.NotVerified,
			Locations: []catalog.Location{{
				Destination: "local",
				Path:        archivePathOf("boutique", "01K5A", at, "pgc"),
			}},
		},
	}
}

// archivePathOf is the deterministic path of F5.5, which is what a listing of a
// destination has to go on when no manifest lies beside the archive.
func archivePathOf(database, id string, at time.Time, extension string) string {
	return fmt.Sprintf("%s/%04d/%02d/%s_%s_%s.%s.zst.age",
		database, at.Year(), at.Month(), database, at.UTC().Format("20060102T150405Z"), id, extension)
}

// lastFilter is what the command asked the catalogue for.
func (f *fakeCatalog) recordFilter(filter catalog.Filter) { f.lastFilter = filter }

// Listing archives reads no database, so a secret it cannot open is none of its
// business: `koffr list` must keep working on a machine whose password files
// have gone (CFG-09).
func TestListWorksWhenASecretCannotBeRead(t *testing.T) {
	site := newSite(t)
	book := &fakeCatalog{backups: threeArchives()}
	site.place(t, book.backups...)

	site.breakSecret(t)

	out := listWith(t, book, site.args("list")...)
	if !strings.Contains(out, "01K5C") {
		t.Errorf("the listing stopped at a password it did not need:\n%s", out)
	}
}
