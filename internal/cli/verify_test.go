package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/domain/catalog"
)

// E-103c, VRF-03 — `koffr verify <backup-id>` re-reads the archive from the
// destination and recomputes its checksum. It does **not** replay the
// structure, and it says so: koffr holds only a public key (ADR-0017, E-113).
func TestVerifyRecomputesTheChecksumAndSaysWhatItCannotDo(t *testing.T) {
	site := newSite(t)
	book := &fakeCatalog{backups: []catalog.Backup{plantedArchive(t, site, "sound")}}

	out, errs, err := executeWith(t, book, site.args("verify", "01K5SOUND")...)
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, errs)
	}

	if !strings.Contains(strings.ToLower(out), "checksum") {
		t.Errorf("the command says nothing about the checksum:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out+errs), "structure") {
		t.Errorf("the command does not say that the structure is not replayed:\n%s\n%s", out, errs)
	}
	if !strings.Contains(strings.ToLower(out+errs), "private key") {
		t.Errorf("the command does not say **why** it cannot replay it:\n%s\n%s", out, errs)
	}

	if book.lastVerification != catalog.Checksum {
		t.Errorf("the catalogue was set to %q, want %q", book.lastVerification, catalog.Checksum)
	}
}

// VRF-03 — an archive corrupted since it was written **fails**, and the
// catalogue records the failure rather than leaving the old verdict standing.
func TestVerifyFailsOnAnArchiveThatChanged(t *testing.T) {
	site := newSite(t)
	planted := plantedArchive(t, site, "corrupt")
	book := &fakeCatalog{backups: []catalog.Backup{planted}}

	// A disk, a year later.
	onDisk := filepath.Join(site.destination, planted.Locations[0].Path)

	contents, err := os.ReadFile(onDisk)
	if err != nil {
		t.Fatalf("read the planted archive: %v", err)
	}

	contents[len(contents)/2] ^= 0xFF
	if err := os.WriteFile(onDisk, contents, 0o600); err != nil {
		t.Fatalf("corrupt it: %v", err)
	}

	_, _, err = executeWith(t, book, site.args("verify", "01K5CORRUPT")...)
	if err == nil {
		t.Fatal("an archive that changed on disk passed its verification")
	}

	if book.lastVerification != catalog.Failed {
		t.Errorf("the catalogue was set to %q, want %q — P4 wants the failure visible",
			book.lastVerification, catalog.Failed)
	}
}

// An identifier nobody knows names the recent archives rather than answering
// "not found": a typo is the likeliest reason to be here.
func TestVerifyNamesTheArchivesItKnows(t *testing.T) {
	site := newSite(t)
	book := &fakeCatalog{backups: []catalog.Backup{plantedArchive(t, site, "sound")}}

	_, _, err := executeWith(t, book, site.args("verify", "01K5TYPO")...)
	if err == nil {
		t.Fatal("an unknown identifier was accepted")
	}
	for _, want := range []string{"01K5TYPO", "01K5SOUND"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q:\n%v", want, err)
		}
	}
}

// An archive the catalogue knows but that is no longer on its destination is a
// finding, not a crash: it is exactly what a verification is for.
func TestVerifyReportsAnArchiveThatIsNoLongerThere(t *testing.T) {
	site := newSite(t)
	missing := plantedArchive(t, site, "sound")

	if err := os.Remove(filepath.Join(site.destination, missing.Locations[0].Path)); err != nil {
		t.Fatalf("remove the archive: %v", err)
	}

	book := &fakeCatalog{backups: []catalog.Backup{missing}}

	_, _, err := executeWith(t, book, site.args("verify", "01K5SOUND")...)
	if err == nil {
		t.Fatal("an archive that is no longer on its destination passed")
	}
	if book.lastVerification != catalog.Failed {
		t.Errorf("the catalogue was set to %q, want %q", book.lastVerification, catalog.Failed)
	}
}

// plantedArchive writes an archive on the site's destination and returns the
// catalogue entry that describes it, checksum included.
func plantedArchive(t *testing.T, on site, which string) catalog.Backup {
	t.Helper()

	contents := []byte("age-encryption.org/v1\n" + strings.Repeat("pretend this is an archive ", 64))
	path := "shop/2026/09/shop_20260925T020003Z_01K5" + strings.ToUpper(which) + ".pgc.zst.age"

	full := filepath.Join(on.destination, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatalf("make the directory: %v", err)
	}
	if err := os.WriteFile(full, contents, 0o600); err != nil {
		t.Fatalf("write the archive: %v", err)
	}

	sum := sha256.Sum256(contents)

	return catalog.Backup{
		ID: "01K5" + strings.ToUpper(which), Database: "shop",
		StartedAt:   time.Date(2026, 9, 25, 2, 0, 3, 0, time.UTC),
		StoredBytes: int64(len(contents)), SHA256Stored: hex.EncodeToString(sum[:]),
		Verified:  catalog.NotVerified,
		Locations: []catalog.Location{{Destination: "local", Path: path}},
	}
}
