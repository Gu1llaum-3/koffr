package state

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/domain/catalog"
)

var when = time.Date(2026, 9, 23, 2, 0, 3, 0, time.UTC)

// The catalogue writes for real: until this lot, the seven tables of E-028
// existed and **not one line had ever been written** to them outside tests.
func TestADatabaseIsRecordedThenUpdated(t *testing.T) {
	book := NewCatalog(open(t))

	if err := book.RecordDatabase(t.Context(), aDatabase(), when); err != nil {
		t.Fatalf("RecordDatabase: %v", err)
	}

	// Recording the same database again updates it rather than failing: a
	// configuration is re-read at every run.
	changed := aDatabase()
	changed.Port = 5433

	if err := book.RecordDatabase(t.Context(), changed, when.Add(time.Hour)); err != nil {
		t.Fatalf("RecordDatabase twice: %v", err)
	}

	var port int

	var fingerprint string

	row := book.state.DB().QueryRow(`SELECT port, fingerprint FROM databases WHERE id = ?`, "boutique")
	if err := row.Scan(&port, &fingerprint); err != nil {
		t.Fatalf("read it back: %v", err)
	}

	if port != 5433 {
		t.Errorf("port = %d, want the updated 5433", port)
	}
	if fingerprint != changed.Fingerprint() {
		t.Error("the fingerprint was not updated with the configuration")
	}
}

// A backup is recorded with its locations, and both come back.
func TestABackupIsRecordedWithItsLocations(t *testing.T) {
	book := NewCatalog(open(t))

	if err := book.RecordDatabase(t.Context(), aDatabase(), when); err != nil {
		t.Fatalf("RecordDatabase: %v", err)
	}

	written := aBackup()
	if err := book.RecordBackup(t.Context(), written); err != nil {
		t.Fatalf("RecordBackup: %v", err)
	}

	found, err := book.Backups(t.Context(), catalog.Filter{})
	if err != nil {
		t.Fatalf("Backups: %v", err)
	}

	if len(found) != 1 {
		t.Fatalf("got %d backups, want 1", len(found))
	}

	got := found[0]
	if got.ID != written.ID || got.Database != written.Database {
		t.Errorf("got %+v, want the backup that was written", got)
	}
	if got.StoredBytes != written.StoredBytes || got.SHA256Stored != written.SHA256Stored {
		t.Errorf("the sizes or the checksum did not come back: %+v", got)
	}
	if len(got.Locations) != 1 || got.Locations[0].Destination != "local" {
		t.Errorf("the locations did not come back: %+v", got.Locations)
	}
	if got.Verified != catalog.NotVerified {
		t.Errorf("a freshly written backup is %q, want %q — P4", got.Verified, catalog.NotVerified)
	}
}

// The foreign key bites: a backup whose database was never recorded is refused
// by the schema, not by a check in Go we could forget to write.
func TestABackupOfAnUnknownDatabaseIsRefused(t *testing.T) {
	book := NewCatalog(open(t))

	err := book.RecordBackup(t.Context(), aBackup())
	if err == nil {
		t.Fatal("a backup of a database nobody recorded was accepted")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "boutique") {
		t.Errorf("the error does not name the database:\n%v", err)
	}
}

// P4 — the verification state is set afterwards, because the archive is
// verified after it is written (E-024). It never goes backwards by accident:
// what is written is what was asked for.
func TestTheVerificationStateIsSetAfterwards(t *testing.T) {
	book := NewCatalog(open(t))

	if err := book.RecordDatabase(t.Context(), aDatabase(), when); err != nil {
		t.Fatalf("RecordDatabase: %v", err)
	}
	if err := book.RecordBackup(t.Context(), aBackup()); err != nil {
		t.Fatalf("RecordBackup: %v", err)
	}

	verifiedAt := when.Add(3 * time.Minute)
	if err := book.SetVerification(t.Context(), "01K5X8QJ4T7N2M9VWZ3RBGH6CD", catalog.Structure, verifiedAt); err != nil {
		t.Fatalf("SetVerification: %v", err)
	}

	found, err := book.Backups(t.Context(), catalog.Filter{})
	if err != nil {
		t.Fatalf("Backups: %v", err)
	}

	if found[0].Verified != catalog.Structure {
		t.Errorf("verified = %q, want %q", found[0].Verified, catalog.Structure)
	}
	if !found[0].VerifiedAt.Equal(verifiedAt) {
		t.Errorf("verified_at = %s, want %s", found[0].VerifiedAt, verifiedAt)
	}
}

// A state the schema does not know is refused at insert time — ADR-0006 keeps
// the CHECK in the schema rather than in a Go constant nothing enforces.
func TestAnUnknownVerificationStateIsRefused(t *testing.T) {
	book := NewCatalog(open(t))

	if err := book.RecordDatabase(t.Context(), aDatabase(), when); err != nil {
		t.Fatalf("RecordDatabase: %v", err)
	}
	if err := book.RecordBackup(t.Context(), aBackup()); err != nil {
		t.Fatalf("RecordBackup: %v", err)
	}

	err := book.SetVerification(t.Context(), "01K5X8QJ4T7N2M9VWZ3RBGH6CD", catalog.Verification("maybe"), when)
	if err == nil {
		t.Fatal("an unknown verification state was accepted")
	}
	if !strings.Contains(err.Error(), "maybe") {
		t.Errorf("the error does not name the state it refused:\n%v", err)
	}
}

// Setting the state of a backup nobody recorded is an error, not a silent no-op:
// `koffr verify` on a wrong identifier must say so.
func TestSettingTheStateOfAnUnknownBackupIsAnError(t *testing.T) {
	book := NewCatalog(open(t))

	err := book.SetVerification(t.Context(), "01K5NOTHINGHEREATALLXXXXXX", catalog.Checksum, when)
	if err == nil {
		t.Fatal("verifying a backup nobody recorded reported success")
	}
	if !errors.Is(err, catalog.ErrNoSuchBackup) {
		t.Errorf("got %v, want %v", err, catalog.ErrNoSuchBackup)
	}
}

// The listing is ordered, most recent first: an operator compares two listings.
func TestBackupsComeBackMostRecentFirst(t *testing.T) {
	book := NewCatalog(open(t))

	if err := book.RecordDatabase(t.Context(), aDatabase(), when); err != nil {
		t.Fatalf("RecordDatabase: %v", err)
	}

	for index, at := range []time.Time{when, when.Add(48 * time.Hour), when.Add(24 * time.Hour)} {
		backup := aBackup()
		backup.ID = []string{"01K5A", "01K5C", "01K5B"}[index]
		backup.StartedAt = at

		if err := book.RecordBackup(t.Context(), backup); err != nil {
			t.Fatalf("RecordBackup: %v", err)
		}
	}

	found, err := book.Backups(t.Context(), catalog.Filter{})
	if err != nil {
		t.Fatalf("Backups: %v", err)
	}

	if len(found) != 3 || found[0].ID != "01K5C" || found[2].ID != "01K5A" {
		t.Errorf("the listing is not most recent first: %v", ids(found))
	}
}

func ids(of []catalog.Backup) []string {
	out := make([]string, 0, len(of))
	for _, backup := range of {
		out = append(out, backup.ID)
	}

	return out
}

func aDatabase() catalog.Database {
	return catalog.Database{
		ID: "boutique", Engine: "postgresql", Host: "10.0.3.12", Port: 5432,
		Name: "boutique", User: "koffr_backup",
		Destinations: []string{"local"}, Staging: "auto", Schedule: "0 2 * * *",
		Retention: catalog.Retention{Last: 7, Daily: 7, Weekly: 4, Monthly: 6},
	}
}

func aBackup() catalog.Backup {
	return catalog.Backup{
		ID: "01K5X8QJ4T7N2M9VWZ3RBGH6CD", Database: "boutique",
		StartedAt: when, FinishedAt: when.Add(2 * time.Minute),
		RawBytes: 295239908, StoredBytes: 23101758,
		SHA256Raw: "9f2c", SHA256Stored: "3ade",
		Locations: []catalog.Location{{
			Destination: "local",
			Path:        "boutique/2026/09/boutique_20260923T020003Z_01K5X8QJ4T7N2M9VWZ3RBGH6CD.pgc.zst.age",
			Bytes:       23101758, StoredAt: when.Add(2 * time.Minute),
		}},
	}
}
