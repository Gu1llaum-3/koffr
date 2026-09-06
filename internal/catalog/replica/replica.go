// Package replica copies the catalog into the repository, and rebuilds it from
// there.
//
// This is what makes "the catalog is a cache" true rather than merely stated
// (ADR-0004, EF-141). Without it, losing the machine Koffr runs on loses the
// job history -- including the failures, which produce no manifest and exist
// nowhere else -- and the choice of where to put a SQLite file stops being a
// convenience and becomes a durability requirement.
//
// Rebuilding works at two levels, and the second is the one that has to work
// when everything else is gone:
//
//  1. Read the replicated snapshot. Complete, encrypted, needs the identity.
//  2. Walk the sources and read the plaintext manifests. Backups only, no job
//     history, but needs no key and no prior state at all.
package replica

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/klauspost/compress/zstd"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/crypto"
	"github.com/Gu1llaum-3/koffr/internal/manifest"
	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// Write stores a snapshot in the repository, twice.
//
// `latest` is what a rebuild reads. The dated copy exists because a corrupted
// `latest` with no history behind it would be a single point of failure in the
// mechanism whose whole purpose is removing one.
func Write(ctx context.Context, st storage.Storage, sealer crypto.Sealer, snap catalog.Snapshot) error {
	// The snapshot names databases and when each was backed up, so it is
	// encrypted like everything else describing content (EF-055). A repository
	// holder should not learn the shape of the estate from the catalog.
	body, err := seal(sealer, snap)
	if err != nil {
		return err
	}

	for _, key := range []string{
		storage.CatalogLatestKey(),
		storage.CatalogSnapshotKey(snap.ExportedAt),
	} {
		if _, err := st.Put(ctx, key, strings.NewReader(string(body)), storage.PutOptions{}); err != nil {
			return fmt.Errorf("replica: write %s: %w", key, err)
		}
	}
	return nil
}

func seal(sealer crypto.Sealer, snap catalog.Snapshot) ([]byte, error) {
	var out strings.Builder
	w, err := sealer.Seal(&out)
	if err != nil {
		return nil, fmt.Errorf("replica: seal the catalog: %w", err)
	}
	z, err := zstd.NewWriter(w)
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("replica: compress the catalog: %w", err)
	}
	if err := json.NewEncoder(z).Encode(snap); err != nil {
		_ = z.Close()
		_ = w.Close()
		return nil, fmt.Errorf("replica: encode the catalog: %w", err)
	}
	if err := z.Close(); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("replica: compress the catalog: %w", err)
	}
	// Closing writes age's final chunk marker; without it the object reads as
	// truncated.
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("replica: seal the catalog: %w", err)
	}
	return []byte(out.String()), nil
}

// Read returns the replicated snapshot.
func Read(ctx context.Context, st storage.Storage, opener crypto.Opener) (catalog.Snapshot, error) {
	key := storage.CatalogLatestKey()
	rc, err := st.Get(ctx, key)
	if err != nil {
		return catalog.Snapshot{}, fmt.Errorf("replica: read %s: %w", key, err)
	}
	defer func() { _ = rc.Close() }()

	plain, err := opener.Open(rc)
	if err != nil {
		return catalog.Snapshot{}, fmt.Errorf("replica: decrypt %s: %w", key, err)
	}
	dec, err := zstd.NewReader(plain)
	if err != nil {
		return catalog.Snapshot{}, fmt.Errorf("replica: decompress %s: %w", key, err)
	}
	defer dec.Close()

	var snap catalog.Snapshot
	if err := json.NewDecoder(dec).Decode(&snap); err != nil {
		return catalog.Snapshot{}, fmt.Errorf("replica: decode %s: %w", key, err)
	}
	return snap, nil
}

// Rebuilt is a snapshot recovered from manifests, with what could not be read.
type Rebuilt struct {
	catalog.Snapshot
	// Skipped names the manifests that could not be read. They are reported
	// rather than swallowed: refusing the whole rebuild over one damaged
	// manifest would make this useless exactly when it is needed, and ignoring
	// it silently would hide a damaged repository.
	Skipped []string
}

// RebuildFromManifests walks the repository and reconstructs the inventory.
//
// It needs no key and no prior state, which is exactly why manifests are
// plaintext. What it cannot recover is the job history: a job that failed
// produced no manifest, so nothing here can invent one. That is the price of
// having lost both the machine and the replica, and it is worth stating rather
// than papering over.
func RebuildFromManifests(ctx context.Context, st storage.Storage) (Rebuilt, error) {
	out := Rebuilt{Snapshot: catalog.Snapshot{
		FormatVersion: catalog.SnapshotFormatVersion,
		Backups:       []catalog.Backup{},
		Jobs:          []catalog.Job{},
		Verifications: []catalog.Verification{},
	}}

	for info, err := range st.List(ctx, storage.SourcesDir+"/") {
		if err != nil {
			return Rebuilt{}, fmt.Errorf("replica: list the repository: %w", err)
		}
		if !strings.HasSuffix(info.Key, "/"+storage.ManifestFile) {
			continue
		}
		b, readErr := readManifest(ctx, st, info.Key)
		if readErr != nil {
			out.Skipped = append(out.Skipped, info.Key+": "+readErr.Error())
			continue
		}
		out.Backups = append(out.Backups, b)
	}
	return out, nil
}

func readManifest(ctx context.Context, st storage.Storage, key string) (catalog.Backup, error) {
	rc, err := st.Get(ctx, key)
	if err != nil {
		return catalog.Backup{}, err
	}
	defer func() { _ = rc.Close() }()

	m, err := manifest.Decode(io.Reader(rc))
	if err != nil {
		return catalog.Backup{}, err
	}

	var size int64
	for _, o := range m.Objects {
		size += o.SizeBytes
	}
	b := catalog.Backup{
		ID:          catalog.ID(m.BackupID),
		SourceID:    m.SourceID,
		Kind:        m.Kind,
		Status:      catalog.Status(m.Status),
		StartedAt:   m.StartedAt,
		FinishedAt:  m.FinishedAt,
		SizeBytes:   size,
		ManifestKey: key,
	}
	if m.ParentID != nil {
		b.ParentID = catalog.ID(*m.ParentID)
	}
	if m.PostgreSQL != nil {
		b.StartLSN, b.EndLSN = m.PostgreSQL.StartLSN, m.PostgreSQL.EndLSN
	}
	// Destination is deliberately left empty. The repository does not know what
	// an operator calls it -- the same bucket can be "main" in one
	// configuration and "offsite" in another -- and inventing a name here would
	// make a rebuilt catalog disagree with the file.
	return b, nil
}
