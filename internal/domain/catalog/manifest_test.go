package catalog_test

import (
	"encoding/json"
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
