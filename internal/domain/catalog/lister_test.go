package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/domain/catalog"
)

// CAT-05 — the repository port rends what a destination holds, and a listing is
// **ordered**: most recent first, so that two listings compare line by line.
func TestCAT05ARepositoryListingIsMostRecentFirst(t *testing.T) {
	lister := catalog.Lister{
		Catalog: &fakeCatalog{},
		Destinations: []catalog.Destination{{
			ID: "disque-local",
			Repository: &fakeRepository{archives: []catalog.Archive{
				archiveOf("boutique", "01M4DP260Q2ZK5PKFYYNSQRG08", "2026-10-08T11:58:30Z"),
				archiveOf("boutique", "01M4DPBNJZ8ZZ7RG3PBEVB03Y0", "2026-10-08T12:03:41Z"),
				archiveOf("erp", "01M4DP4RX4MESTXPAYSM3P9QG6", "2026-10-08T11:59:55Z"),
			}},
		}},
	}

	listed, err := lister.List(t.Context(), catalog.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	got := identifiers(listed)
	want := []string{
		"01M4DPBNJZ8ZZ7RG3PBEVB03Y0",
		"01M4DP4RX4MESTXPAYSM3P9QG6",
		"01M4DP260Q2ZK5PKFYYNSQRG08",
	}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("listed %v,\nwant %v — most recent first", got, want)
	}
}

// CAT-06 — an archive the destination holds and the catalogue ignores **is
// shown**, and says it is not in the catalogue.
//
// `A-19`: an archive written by an earlier release sat on the disk and `koffr
// list` did not mention it at all. An operator who upgraded lost sight of every
// archive taken before — and `E-064` says the absence of a check is never a
// success. An absence of mention is worse.
func TestCAT06AnArchiveTheCatalogueIgnoresIsStillShown(t *testing.T) {
	book := &fakeCatalog{backups: []catalog.Backup{{
		ID: "01M4DPBNJZ8ZZ7RG3PBEVB03Y0", Database: "boutique",
		StartedAt: at("2026-10-08T12:03:41Z"), StoredBytes: 20290807,
		Verified: catalog.Structure,
		Locations: []catalog.Location{{
			Destination: "disque-local",
			Path:        "boutique/2026/10/boutique_20261008T120341Z_01M4DPBNJZ8ZZ7RG3PBEVB03Y0.pgc.zst.age",
		}},
	}}}

	lister := catalog.Lister{
		Catalog: book,
		Destinations: []catalog.Destination{{
			ID: "disque-local",
			Repository: &fakeRepository{archives: []catalog.Archive{
				archiveOf("boutique", "01M4DPBNJZ8ZZ7RG3PBEVB03Y0", "2026-10-08T12:03:41Z"),
				archiveOf("boutique", "01M4DP4Y8K35XDW9MBMKA6SGD4", "2026-10-08T12:00:00Z"),
			}},
		}},
	}

	listed, err := lister.List(t.Context(), catalog.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(listed) != 2 {
		t.Fatalf("listed %d archives, want the catalogued one and the one only the disk knows: %+v", len(listed), listed)
	}

	stranger := find(t, listed, "01M4DP4Y8K35XDW9MBMKA6SGD4")
	if stranger.Catalogued {
		t.Error("the archive the catalogue ignores claims to be catalogued")
	}
	if stranger.Database != "boutique" {
		t.Errorf("its database is %q, want boutique — the path says so", stranger.Database)
	}
	if stranger.StoredBytes == 0 {
		t.Error("its size is unknown, and the file on the destination has one")
	}

	known := find(t, listed, "01M4DPBNJZ8ZZ7RG3PBEVB03Y0")
	if !known.Catalogued || known.Verified != catalog.Structure {
		t.Errorf("the catalogued archive lost what the catalogue knew: %+v", known)
	}
}

// CAT-06 — and the manifest fills in what the file name cannot say, when it is
// there. The verification column is **not** one of those: koffr says what it
// knows, and it has no record of having checked this archive (`N-1`).
func TestCAT06TheManifestFillsInWhatTheNameCannotSay(t *testing.T) {
	path := "boutique/2026/10/boutique_20261008T120000Z_01M4DP4Y8K35XDW9MBMKA6SGD4.pgc.zst.age"

	lister := catalog.Lister{
		Catalog: &fakeCatalog{},
		Destinations: []catalog.Destination{{
			ID: "disque-local",
			Repository: &fakeRepository{
				archives: []catalog.Archive{{Path: path, Bytes: 11, At: at("2026-10-08T12:00:00Z")}},
				manifests: map[string]catalog.Manifest{path: {
					BackupID: "01M4DP4Y8K35XDW9MBMKA6SGD4", DatabaseID: "boutique",
					StartedAt: "2026-10-08T12:00:00Z", SizeRaw: 53248457, SizeStored: 20290796,
					SHA256Stored: "8d06b691", Verified: catalog.Verified{Checksum: true, Structure: true},
				}},
			},
		}},
	}

	listed, err := lister.List(t.Context(), catalog.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	only := listed[0]
	if only.RawBytes != 53248457 || only.StoredBytes != 20290796 {
		t.Errorf("the manifest was not read: %+v", only)
	}
	if only.SHA256Stored != "8d06b691" {
		t.Errorf("the checksum of the manifest is lost: %+v", only)
	}
	if only.Catalogued {
		t.Error("a manifest is not a catalogue entry: koffr has no record of checking this archive")
	}
}

// CAT-07 — a destination nobody declared is a **refusal**, naming the ones that
// exist. `A-27`: a typo answered `no backup in the catalogue yet`, with the
// return code of a success, and read exactly like an empty repository.
func TestCAT07AnUnknownDestinationIsRefused(t *testing.T) {
	lister := catalog.Lister{
		Catalog: &fakeCatalog{},
		Destinations: []catalog.Destination{
			{ID: "disque-local", Repository: &fakeRepository{}},
			{ID: "s3-ovh", Repository: &fakeRepository{}},
		},
	}

	_, err := lister.List(t.Context(), catalog.Filter{Destination: "nexistepas"})
	if !errors.Is(err, catalog.ErrNoSuchDestination) {
		t.Fatalf("List of an unknown destination returned %v, want ErrNoSuchDestination", err)
	}

	for _, known := range []string{"disque-local", "s3-ovh"} {
		if !strings.Contains(err.Error(), known) {
			t.Errorf("the refusal does not name the destination %q: %v", known, err)
		}
	}
}

// And an empty repository is still an empty repository: no error, nothing
// listed. The two cases stopped looking alike, they did not swap.
func TestAnEmptyRepositoryIsNotAnError(t *testing.T) {
	lister := catalog.Lister{
		Catalog:      &fakeCatalog{},
		Destinations: []catalog.Destination{{ID: "disque-local", Repository: &fakeRepository{}}},
	}

	listed, err := lister.List(t.Context(), catalog.Filter{Destination: "disque-local"})
	if err != nil {
		t.Fatalf("List of an empty repository: %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("listed %d archives of an empty repository", len(listed))
	}
}

// A catalogue entry whose archive is **gone from the destination** is shown
// too, and says so. Losing a file quietly is the failure retention exists to
// avoid.
func TestACataloguedArchiveMissingFromTheDestinationSaysSo(t *testing.T) {
	book := &fakeCatalog{backups: []catalog.Backup{{
		ID: "01M4DPBNJZ8ZZ7RG3PBEVB03Y0", Database: "boutique",
		StartedAt: at("2026-10-08T12:03:41Z"),
		Locations: []catalog.Location{{
			Destination: "disque-local",
			Path:        "boutique/2026/10/boutique_20261008T120341Z_01M4DPBNJZ8ZZ7RG3PBEVB03Y0.pgc.zst.age",
		}},
	}}}

	lister := catalog.Lister{
		Catalog:      book,
		Destinations: []catalog.Destination{{ID: "disque-local", Repository: &fakeRepository{}}},
	}

	listed, err := lister.List(t.Context(), catalog.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(listed) != 1 {
		t.Fatalf("listed %d archives, want the one the catalogue knows", len(listed))
	}
	if listed[0].Present {
		t.Error("the archive is not on the destination and the listing says it is")
	}
}

func archiveOf(database, id, when string) catalog.Archive {
	moment := at(when)
	path := database + "/2026/10/" + database + "_" +
		moment.UTC().Format("20060102T150405Z") + "_" + id + ".pgc.zst.age"

	return catalog.Archive{Path: path, Bytes: 20290796, At: moment}
}

func at(when string) time.Time {
	moment, err := time.Parse(time.RFC3339, when)
	if err != nil {
		panic(err)
	}

	return moment
}

func identifiers(listed []catalog.Listed) []string {
	found := make([]string, 0, len(listed))
	for _, one := range listed {
		found = append(found, one.ID)
	}

	return found
}

func find(t *testing.T, listed []catalog.Listed, id string) catalog.Listed {
	t.Helper()

	for _, one := range listed {
		if one.ID == id {
			return one
		}
	}

	t.Fatalf("no archive %q in %+v", id, listed)

	return catalog.Listed{}
}

type fakeCatalog struct{ backups []catalog.Backup }

func (f *fakeCatalog) RecordDatabase(context.Context, catalog.Database, time.Time) error { return nil }

func (f *fakeCatalog) RecordBackup(context.Context, catalog.Backup) error { return nil }

func (f *fakeCatalog) SetVerification(context.Context, string, catalog.Verification, time.Time) error {
	return nil
}

func (f *fakeCatalog) Backups(_ context.Context, filter catalog.Filter) ([]catalog.Backup, error) {
	var kept []catalog.Backup

	for _, one := range f.backups {
		if filter.Database != "" && one.Database != filter.Database {
			continue
		}

		kept = append(kept, one)
	}

	return kept, nil
}

type fakeRepository struct {
	archives  []catalog.Archive
	manifests map[string]catalog.Manifest
}

func (f *fakeRepository) Archives(_ context.Context, prefix string) ([]catalog.Archive, error) {
	var kept []catalog.Archive

	for _, one := range f.archives {
		if prefix == "" || strings.HasPrefix(one.Path, prefix) {
			kept = append(kept, one)
		}
	}

	return kept, nil
}

func (f *fakeRepository) ReadManifest(_ context.Context, path string) ([]byte, error) {
	manifest, found := f.manifests[path]
	if !found {
		return nil, catalog.ErrNoManifest
	}

	return json.Marshal(manifest) //nolint:wrapcheck // a test double
}
