package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/manifest"
	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// Mirror copies a finished backup to another destination (EF-044, the 3-2-1
// rule).
//
// Copied rather than backed up again, and that is the whole design. Streaming
// the database twice would read it twice, cost twice, and produce two backups
// that are not the same backup -- different digests, different timestamps,
// nothing to compare. A copy is byte-identical, so the manifest's digests
// verify the second destination as well as the first.
//
// It runs after the manifest, which means after the point of no return: the
// backup already exists and is restorable. A mirror that fails is a warning
// about the second copy, never a failure of the first.
func Mirror(
	ctx context.Context,
	from, to storage.Storage,
	m manifest.Manifest,
	prefix string,
) error {
	// Every object the manifest names, then the manifest itself. The same order
	// as writing one, and for the same reason (ENF-010): until the manifest is
	// there, the destination holds objects and not a backup.
	for _, obj := range m.Objects {
		if err := copyObject(ctx, from, to, prefix+obj.Key, obj.SHA256); err != nil {
			return err
		}
	}
	for _, name := range []string{storage.RestoreDocFile} {
		if err := copyObject(ctx, from, to, prefix+name, ""); err != nil {
			return err
		}
	}

	var encoded bytes.Buffer
	if err := manifest.Encode(&encoded, m); err != nil {
		return fmt.Errorf("backup: mirror %s: %w", m.BackupID, err)
	}
	if _, err := to.Put(ctx, prefix+storage.ManifestFile,
		bytes.NewReader(encoded.Bytes()), storage.PutOptions{}); err != nil {
		return fmt.Errorf("backup: mirror the manifest of %s: %w", m.BackupID, err)
	}
	return nil
}

// copyObject streams one object across, checking the digest on the way when
// the manifest recorded one.
//
// Checked here rather than trusted: a copy is the one moment a corrupted object
// can be noticed for free, and a mirror of a damaged backup is two damaged
// backups.
func copyObject(ctx context.Context, from, to storage.Storage, key, wantSHA256 string) error {
	rc, err := from.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("backup: mirror %s: %w", key, err)
	}
	defer func() { _ = rc.Close() }()

	var src io.Reader = rc
	digest := sha256.New()
	if wantSHA256 != "" {
		src = io.TeeReader(src, digest)
	}

	if _, err := to.Put(ctx, key, src, storage.PutOptions{}); err != nil {
		return fmt.Errorf("backup: mirror %s: %w", key, err)
	}
	if wantSHA256 == "" {
		return nil
	}
	if got := hex.EncodeToString(digest.Sum(nil)); got != wantSHA256 {
		return fmt.Errorf(
			"backup: mirroring %s copied bytes that do not match the manifest; "+
				"the source object is damaged and the copy is now damaged too", key)
	}
	return nil
}

// recordMirror notes the copy in the catalog, as its own row.
//
// Its own row because retention is per destination: keeping seven days locally
// and twelve months offsite is the point of writing to both, and one row could
// not express it.
func (r *Runner) recordMirror(ctx context.Context, b catalog.Backup, destination string) error {
	b.Destination = destination
	return r.Catalog.RecordBackup(ctx, b)
}

// mirror copies the backup to every extra destination and reports what did not
// work.
//
// Warnings rather than errors, and one per destination: a second copy that
// failed is worth telling somebody about, and it is not a reason to call a
// backup that exists a backup that did not happen. Each destination is
// independent, so one refusing does not stop the next.
func (r *Runner) mirror(
	ctx context.Context, req Request, prefix string, m manifest.Manifest,
) []string {
	var warnings []string
	for _, mirror := range req.Mirrors {
		if err := Mirror(ctx, r.Storage, mirror.Storage, m, prefix); err != nil {
			warnings = append(warnings,
				fmt.Sprintf("the copy to %s did not complete: %v", mirror.Name, err))
			continue
		}
		row := catalog.Backup{
			ID: catalog.ID(m.BackupID), SourceID: req.SourceID, Kind: m.Kind,
			Status: catalog.StatusCompleted, StartedAt: m.StartedAt, FinishedAt: m.FinishedAt,
			SizeBytes: totalBytes(m), ManifestKey: prefix + storage.ManifestFile,
		}
		if err := r.recordMirror(ctx, row, mirror.Name); err != nil {
			warnings = append(warnings,
				fmt.Sprintf("the copy to %s was written but not recorded: %v", mirror.Name, err))
		}
	}
	return warnings
}

func totalBytes(m manifest.Manifest) int64 {
	var n int64
	for _, o := range m.Objects {
		n += o.SizeBytes
	}
	return n
}
