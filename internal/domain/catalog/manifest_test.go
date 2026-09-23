package catalog_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/domain/catalog"
)

// CAT-02 — the catalogue is an **index**, never the source of truth. Everything
// a restore needs lives in the manifest deposited beside the archive, so that a
// repository stays usable when the agent, its disk and its SQLite are gone
// (ADR-0006, E-059).
//
// The test says it the only way that means anything: build the manifest, throw
// the catalogue away, and rebuild the entry from the manifest alone.
func TestCAT02EverythingARestoreNeedsSurvivesInTheManifest(t *testing.T) {
	original := aBackupEntry()

	manifest := catalog.ManifestOf(original, aDatabase(), aRun())

	// Through JSON, because that is how it reaches the destination.
	written, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var read catalog.Manifest
	if err := json.Unmarshal(written, &read); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	rebuilt := read.Backup()

	for _, field := range []struct {
		what      string
		from, was any
	}{
		{"the identifier", rebuilt.ID, original.ID},
		{"the database", rebuilt.Database, original.Database},
		{"the moment", rebuilt.StartedAt.UTC(), original.StartedAt.UTC()},
		{"the raw size", rebuilt.RawBytes, original.RawBytes},
		{"the stored size", rebuilt.StoredBytes, original.StoredBytes},
		{"the raw checksum", rebuilt.SHA256Raw, original.SHA256Raw},
		{"the stored checksum", rebuilt.SHA256Stored, original.SHA256Stored},
		{"the verification state", rebuilt.Verified, original.Verified},
	} {
		if field.from != field.was {
			t.Errorf("%s did not survive the manifest: got %v, want %v", field.what, field.from, field.was)
		}
	}

	// And what tells an operator **how** to open it, six months later.
	if read.Format == "" || len(read.Pipeline) == 0 {
		t.Error("the manifest does not say how the archive was written")
	}
	if read.Tool.Name == "" || read.Tool.Version == "" {
		t.Error("the manifest does not say which tool produced the dump, so nothing says what can restore it")
	}
	if len(read.Recipients) == 0 {
		t.Error("the manifest does not say which keys open it")
	}
}

// CAT-02 — the verification state travels too, and `P4` holds inside the
// manifest as it does in the catalogue: not verified is never a success.
func TestCAT02TheVerificationStateTravelsWithTheArchive(t *testing.T) {
	unverified := aBackupEntry()
	unverified.Verified = catalog.NotVerified

	manifest := catalog.ManifestOf(unverified, aDatabase(), aRun())

	if manifest.Verified.Checksum || manifest.Verified.Structure {
		t.Error("an unverified archive claims a verification in its manifest")
	}

	verified := aBackupEntry()
	verified.Verified = catalog.Structure
	verified.VerifiedAt = when().Add(3 * time.Minute)

	full := catalog.ManifestOf(verified, aDatabase(), aRun())

	if !full.Verified.Checksum || !full.Verified.Structure {
		t.Error("a fully verified archive does not say so in its manifest")
	}
	if full.Verified.At == "" {
		t.Error("the manifest does not say when it was verified")
	}
}

func when() time.Time { return time.Date(2026, 9, 23, 2, 0, 3, 0, time.UTC) }

func aBackupEntry() catalog.Backup {
	return catalog.Backup{
		ID: "01K5X8QJ4T7N2M9VWZ3RBGH6CD", Database: "boutique",
		StartedAt: when(), FinishedAt: when().Add(2 * time.Minute),
		RawBytes: 295239908, StoredBytes: 23101758,
		SHA256Raw: "9f2ca71b", SHA256Stored: "3adec024",
		Verified: catalog.Checksum, VerifiedAt: when().Add(3 * time.Minute),
		Locations: []catalog.Location{{
			Destination: "local",
			Path:        "boutique/2026/09/boutique_20260923T020003Z_01K5X8QJ4T7N2M9VWZ3RBGH6CD.pgc.zst.age",
			Bytes:       23101758, StoredAt: when().Add(2 * time.Minute),
		}},
	}
}

func aRun() catalog.Run {
	return catalog.Run{
		ServerVersion: "16.15",
		Tool: catalog.Tool{
			Name: "pg_dump", Version: "18.6", Source: "host", Path: "/usr/bin/pg_dump",
			Argv: []string{"--format=custom", "--no-owner", "--no-privileges", "--compress=0"},
		},
		Format:     "pg_custom",
		Pipeline:   []string{"zstd:3", "age:x25519"},
		Staging:    "stage",
		Recipients: []string{"age1ql3zmcac8p"},
	}
}

// CAT-03 — the manifest carries exactly the fields the § 5.3 shows, under the
// names it shows. Enumerated, not counted: a count says nothing about which
// one is missing, and the manifest is what an operator reads six months later
// with nothing else (`E-058`).
func TestCAT03TheManifestCarriesTheFieldsOfTheSpecification(t *testing.T) {
	rendered := renderedManifest(t, aBackupEntry())

	specified := []string{
		"backup_id", "database_id", "engine",
		"started_at", "duration_ms",
		"server_version", "tool",
		"format", "pipeline", "staging",
		"size_raw", "size_stored", "sha256_raw", "sha256_stored",
		"recipients", "verified",
	}

	for _, name := range specified {
		if _, present := rendered[name]; !present {
			t.Errorf("the manifest has no %q, which the § 5.3 shows", name)
		}
	}

	// And nothing beyond: a field koffr invents is a field nobody else can read.
	for name := range rendered {
		if !slices.Contains(specified, name) {
			t.Errorf("the manifest carries %q, which the § 5.3 does not show.\n"+
				"  Adding one is a decision: the manifest is a format others read.", name)
		}
	}

	// The tool is the sub-object of § 5.3, with its argv — what says whether a
	// given pg_restore can read this archive back.
	tool, ok := rendered["tool"].(map[string]any)
	if !ok {
		t.Fatalf("tool is not an object: %T", rendered["tool"])
	}

	for _, name := range []string{"name", "version", "source", "path", "argv"} {
		if _, present := tool[name]; !present {
			t.Errorf("the tool of the manifest has no %q", name)
		}
	}
}

// CAT-03 — the moments are RFC 3339 UTC, as ADR-0006 writes every timestamp.
func TestCAT03TheMomentsAreRFC3339UTC(t *testing.T) {
	entry := aBackupEntry()
	entry.StartedAt = time.Date(2026, 9, 23, 2, 0, 3, 0, time.FixedZone("CEST", 2*3600))

	manifest := catalog.ManifestOf(entry, aDatabase(), aRun())

	if !strings.HasSuffix(manifest.StartedAt, "Z") {
		t.Errorf("started_at is not UTC: %s", manifest.StartedAt)
	}
	if _, err := time.Parse(time.RFC3339, manifest.StartedAt); err != nil {
		t.Errorf("started_at is not RFC 3339: %s", manifest.StartedAt)
	}
	if manifest.DurationMS <= 0 {
		t.Errorf("duration_ms = %d, want the duration of the job", manifest.DurationMS)
	}
}

// CAT-04, E-059 — the manifest never carries a connection credential, and stays
// readable without the private key: that is what makes a repository
// inventoriable. The test looks for the **values**, because a field named
// `password` that is never filled proves nothing.
func TestCAT04TheManifestCarriesNoCredential(t *testing.T) {
	const (
		password  = "hunter2-the-database-password"
		user      = "koffr_backup"
		secretkey = "AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQ"
	)

	database := aDatabase()
	database.User = user

	run := aRun()
	run.Tool.Argv = append(run.Tool.Argv, "--dbname=boutique")

	written, err := json.Marshal(catalog.ManifestOf(aBackupEntry(), database, run))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	for what, value := range map[string]string{
		"the password":     password,
		"a private key":    secretkey,
		"a secret path":    "/run/credentials/koffr.service/boutique",
		"a password file":  "password_file",
		"a connection URL": "postgres://",
	} {
		if strings.Contains(string(written), value) {
			t.Errorf("%s is in the manifest:\n%s", what, written)
		}
	}

	// The recipients are **public** keys, and they belong there: they say which
	// key opens the archive.
	if !strings.Contains(string(written), "age1") {
		t.Error("the manifest does not say which public keys open the archive")
	}
}

// E-113, E-114 — what a stolen repository gives up: metadata, and nothing else.
// The database user is metadata one could argue about; the § 6 assumes names of
// bases, sizes and times, and koffr keeps the user out because nothing needs it
// to restore.
func TestCAT04AStolenRepositoryGivesUpMetadataOnly(t *testing.T) {
	rendered := renderedManifest(t, aBackupEntry())

	for _, forbidden := range []string{"user", "host", "port", "password"} {
		if _, present := rendered[forbidden]; present {
			t.Errorf("the manifest declares %q, which a stolen repository would hand over", forbidden)
		}
	}
}

func renderedManifest(t *testing.T, entry catalog.Backup) map[string]any {
	t.Helper()

	written, err := json.Marshal(catalog.ManifestOf(entry, aDatabase(), aRun()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var fields map[string]any
	if err := json.Unmarshal(written, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	return fields
}
