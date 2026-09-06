package replica_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/catalog/replica"
	"github.com/Gu1llaum-3/koffr/internal/crypto"
	"github.com/Gu1llaum-3/koffr/internal/manifest"
	"github.com/Gu1llaum-3/koffr/internal/storage"
	"github.com/Gu1llaum-3/koffr/internal/storage/memory"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

func keys(t *testing.T) (crypto.Sealer, crypto.Opener) {
	t.Helper()
	identity, recipient := testutil.AgeIdentity(t)
	_, recovery := testutil.AgeIdentity(t)
	sealer, err := crypto.NewSealer([]string{recipient, recovery})
	require.NoError(t, err)
	opener, err := crypto.NewOpener(identity)
	require.NoError(t, err)
	return sealer, opener
}

func snapshot() catalog.Snapshot {
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	return catalog.Snapshot{
		FormatVersion: catalog.SnapshotFormatVersion,
		ExportedAt:    at,
		KoffrVersion:  "test",
		Backups: []catalog.Backup{{
			ID: "01BACKUP0000000000000000AA", SourceID: "prod", Kind: "logical",
			Destination: "main", Status: catalog.StatusCompleted,
			StartedAt: at, FinishedAt: at.Add(time.Minute), SizeBytes: 1024,
			ManifestKey: "sources/prod/logical/01BACKUP0000000000000000AA/manifest.json",
		}},
		Jobs: []catalog.Job{{
			ID: "01JOBFAILED000000000000000", SourceID: "prod", Kind: "logical",
			Trigger: catalog.TriggerSchedule, Status: catalog.StatusFailed,
			ErrorClass: catalog.ErrClassSource, ErrorDetail: "pg_dump exited with status 1",
			StartedAt: at, FinishedAt: at.Add(time.Second),
		}},
		Verifications: []catalog.Verification{},
	}
}

func TestWriteThenRead(t *testing.T) {
	st := memory.New()
	sealer, opener := keys(t)

	require.NoError(t, replica.Write(t.Context(), st, sealer, snapshot()))

	got, err := replica.Read(t.Context(), st, opener)
	require.NoError(t, err)
	assert.Equal(t, snapshot().Backups[0].ID, got.Backups[0].ID)
	assert.Equal(t, "pg_dump exited with status 1", got.Jobs[0].ErrorDetail,
		"the job history is the part that exists nowhere else")
}

// The replica names the databases that were backed up and when, so it is
// encrypted like everything else that describes content (EF-055). A repository
// holder must not learn the shape of the estate from the catalog.
func TestWrite_IsEncrypted(t *testing.T) {
	st := memory.New()
	sealer, _ := keys(t)
	require.NoError(t, replica.Write(t.Context(), st, sealer, snapshot()))

	rc, err := st.Get(t.Context(), storage.CatalogLatestKey())
	require.NoError(t, err)
	raw, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())

	assert.NotContains(t, string(raw), "prod")
	assert.NotContains(t, string(raw), "pg_dump")
	assert.True(t, bytes.HasPrefix(raw, []byte("age-encryption.org/")),
		"a reader with the age CLI has to recognise this without Koffr (PD-001)")
}

// Every write also leaves a dated copy. `latest` is what a rebuild reads, and
// a corrupted `latest` with no history behind it is a single point of failure
// for the thing that exists to remove single points of failure.
func TestWrite_KeepsADatedSnapshot(t *testing.T) {
	st := memory.New()
	sealer, opener := keys(t)
	require.NoError(t, replica.Write(t.Context(), st, sealer, snapshot()))

	var dated []string
	for info, err := range st.List(t.Context(), storage.CatalogDir+"/") {
		require.NoError(t, err)
		if info.Key != storage.CatalogLatestKey() {
			dated = append(dated, info.Key)
		}
	}
	require.Len(t, dated, 1)

	rc, err := st.Get(t.Context(), dated[0])
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	plain, err := opener.Open(rc)
	require.NoError(t, err)
	// The key says latest.json.zst.age, and the layers have to be in that
	// order: a reader following the name gets json, zstd, age from the inside.
	dec, err := zstd.NewReader(plain)
	require.NoError(t, err)
	defer dec.Close()

	var snap catalog.Snapshot
	require.NoError(t, json.NewDecoder(dec).Decode(&snap))
	assert.Len(t, snap.Backups, 1)
}

func TestRead_MissingReplica(t *testing.T) {
	_, opener := keys(t)
	_, err := replica.Read(t.Context(), memory.New(), opener)
	require.ErrorIs(t, err, storage.ErrNotFound)
}

// The second level of rebuild, and the one that has to work when everything
// else is gone: manifests are plaintext precisely so an inventory can be
// rebuilt with no key and no prior state (ADR-0004).
func TestRebuildFromManifests(t *testing.T) {
	st := memory.New()
	putManifest(t, st, "prod", "01BACKUP0000000000000000AA", 1024)
	putManifest(t, st, "prod", "01BACKUP0000000000000000BB", 2048)
	putManifest(t, st, "staging", "01BACKUP0000000000000000CC", 512)

	snap, err := replica.RebuildFromManifests(t.Context(), st)
	require.NoError(t, err)
	require.Len(t, snap.Backups, 3)

	byID := map[catalog.ID]catalog.Backup{}
	for _, b := range snap.Backups {
		byID[b.ID] = b
	}
	got := byID["01BACKUP0000000000000000AA"]
	assert.Equal(t, "prod", got.SourceID)
	assert.Equal(t, "logical", got.Kind)
	assert.Equal(t, catalog.StatusCompleted, got.Status)
	assert.Equal(t, int64(1024), got.SizeBytes)
	assert.Equal(t, "sources/prod/logical/01BACKUP0000000000000000AA/manifest.json", got.ManifestKey)

	assert.Empty(t, snap.Jobs,
		"a failed job leaves no manifest, so this level of rebuild cannot invent one")
}

// A half-written manifest is what an interrupted upload leaves. Refusing the
// whole rebuild over one would make the fallback useless exactly when it is
// needed; ignoring it silently would hide a damaged repository.
func TestRebuildFromManifests_SkipsUnreadableOnesAndSaysSo(t *testing.T) {
	st := memory.New()
	putManifest(t, st, "prod", "01BACKUP0000000000000000AA", 1024)
	_, err := st.Put(t.Context(),
		"sources/prod/logical/01BACKUP0000000000000000ZZ/manifest.json",
		bytes.NewReader([]byte("{ truncated")), storage.PutOptions{})
	require.NoError(t, err)

	snap, err := replica.RebuildFromManifests(t.Context(), st)
	require.NoError(t, err)
	assert.Len(t, snap.Backups, 1)
	require.Len(t, snap.Skipped, 1)
	assert.Contains(t, snap.Skipped[0], "01BACKUP0000000000000000ZZ")
}

func TestRebuildFromManifests_EmptyRepository(t *testing.T) {
	snap, err := replica.RebuildFromManifests(t.Context(), memory.New())
	require.NoError(t, err)
	assert.Empty(t, snap.Backups)
}

func putManifest(t *testing.T, st storage.Storage, source, backupID string, size int64) {
	t.Helper()
	src, err := storage.ForSource(source)
	require.NoError(t, err)
	b, err := src.Backup(storage.DirLogical, backupID)
	require.NoError(t, err)

	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	m := manifest.Manifest{
		FormatVersion: manifest.FormatVersion,
		BackupID:      backupID,
		SourceID:      source,
		Engine:        "postgresql",
		ServerVersion: "17.0",
		Kind:          "logical",
		StartedAt:     at,
		FinishedAt:    at.Add(time.Minute),
		Status:        string(catalog.StatusCompleted),
		Objects: []manifest.Object{{
			Key: "dump.pgdump.zst.age", SizeBytes: size,
			SHA256: "0000000000000000000000000000000000000000000000000000000000000000",
			Codec:  "zstd", Encryption: "age", Recipients: []string{"age1x"},
		}},
		Tool:         manifest.Tool{Name: "postgresql", Version: "17.0"},
		KoffrVersion: "test",
	}
	var buf bytes.Buffer
	require.NoError(t, manifest.Encode(&buf, m))
	_, err = st.Put(context.Background(), b.ManifestKey(), bytes.NewReader(buf.Bytes()), storage.PutOptions{})
	require.NoError(t, err)
}
