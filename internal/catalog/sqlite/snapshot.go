package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/version"
)

// Export reads the whole catalog out.
//
// Everything, without a filter or a limit: a snapshot that kept only the recent
// rows would rebuild a catalog that looks complete and is not, which is the
// worst of the available outcomes. Catalogs hold thousands of rows, not
// millions, so reading them whole costs nothing worth optimising.
func (s *Store) Export(ctx context.Context) (catalog.Snapshot, error) {
	snap := catalog.Snapshot{
		FormatVersion: catalog.SnapshotFormatVersion,
		ExportedAt:    time.Now().UTC(),
		KoffrVersion:  version.Value,

		// Empty rather than nil, so the JSON in the repository says "nothing
		// here" instead of "null", which reads as something having gone wrong.
		Backups:       []catalog.Backup{},
		Jobs:          []catalog.Job{},
		Verifications: []catalog.Verification{},
	}

	backups, err := s.ListBackups(ctx, catalog.BackupFilter{})
	if err != nil {
		return catalog.Snapshot{}, err
	}
	snap.Backups = append(snap.Backups, backups...)

	if err := s.exportJobs(ctx, &snap); err != nil {
		return catalog.Snapshot{}, err
	}
	if err := s.exportVerifications(ctx, &snap); err != nil {
		return catalog.Snapshot{}, err
	}
	return snap, nil
}

func (s *Store) exportJobs(ctx context.Context, snap *catalog.Snapshot) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, source_id, kind, trigger, status, error_class, error_detail,
		        started_at, finished_at
		 FROM jobs ORDER BY started_at DESC, id`)
	if err != nil {
		return fmt.Errorf("catalog/sqlite: export jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var j catalog.Job
		var started, finished int64
		if err := rows.Scan(&j.ID, &j.SourceID, &j.Kind, &j.Trigger, &j.Status,
			&j.ErrorClass, &j.ErrorDetail, &started, &finished); err != nil {
			return fmt.Errorf("catalog/sqlite: export jobs: %w", err)
		}
		j.StartedAt, j.FinishedAt = fromUnix(started), fromUnix(finished)
		snap.Jobs = append(snap.Jobs, j)
	}
	return rows.Err()
}

func (s *Store) exportVerifications(ctx context.Context, snap *catalog.Snapshot) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, backup_id, tier, status, report, started_at, finished_at
		 FROM verifications ORDER BY started_at DESC, id`)
	if err != nil {
		return fmt.Errorf("catalog/sqlite: export verifications: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var v catalog.Verification
		var started, finished int64
		var report []byte
		if err := rows.Scan(&v.ID, &v.BackupID, &v.Tier, &v.Status, &report,
			&started, &finished); err != nil {
			return fmt.Errorf("catalog/sqlite: export verifications: %w", err)
		}
		v.Report = report
		v.StartedAt, v.FinishedAt = fromUnix(started), fromUnix(finished)
		snap.Verifications = append(snap.Verifications, v)
	}
	return rows.Err()
}

// Import merges a snapshot in.
//
// One transaction, so a snapshot that turns out to be damaged halfway leaves
// the catalog as it was. A partially applied rebuild is worse than a failed
// one: it looks like it worked.
func (s *Store) Import(ctx context.Context, snap catalog.Snapshot) error {
	// A snapshot from a newer Koffr is refused rather than half-understood.
	// Fields this version does not know about would be dropped silently, and a
	// catalog that is quietly wrong is the thing hardest to notice.
	if snap.FormatVersion > catalog.SnapshotFormatVersion {
		return fmt.Errorf(
			"catalog/sqlite: snapshot is format version %d and this Koffr understands %d; "+
				"upgrade Koffr rather than importing part of it",
			snap.FormatVersion, catalog.SnapshotFormatVersion)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("catalog/sqlite: import: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := importBackups(ctx, tx, snap.Backups); err != nil {
		return err
	}
	if err := importJobs(ctx, tx, snap.Jobs); err != nil {
		return err
	}
	if err := importVerifications(ctx, tx, snap.Verifications); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("catalog/sqlite: import: %w", err)
	}
	return nil
}

// The three importers use ON CONFLICT DO UPDATE, which is what makes Import
// both idempotent and additive: a row already present is refreshed, a row this
// catalog has and the snapshot does not is left alone. A rebuild often runs on
// a catalog that is merely behind, and deleting what it did not hear about
// would be a repair that loses data.

func importBackups(ctx context.Context, tx *sql.Tx, backups []catalog.Backup) error {
	for _, b := range backups {
		pos, err := binlogPosOf(b)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO backups (id, source_id, kind, parent_id, destination, status,
			                     started_at, finished_at, size_bytes, manifest_key,
			                     start_lsn, end_lsn, binlog_file, binlog_pos)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET
				source_id=excluded.source_id, kind=excluded.kind, parent_id=excluded.parent_id,
				destination=excluded.destination, status=excluded.status,
				started_at=excluded.started_at, finished_at=excluded.finished_at,
				size_bytes=excluded.size_bytes, manifest_key=excluded.manifest_key,
				start_lsn=excluded.start_lsn, end_lsn=excluded.end_lsn,
				binlog_file=excluded.binlog_file, binlog_pos=excluded.binlog_pos`,
			string(b.ID), b.SourceID, b.Kind, string(b.ParentID), b.Destination, string(b.Status),
			toUnix(b.StartedAt), toUnix(b.FinishedAt), b.SizeBytes, b.ManifestKey,
			b.StartLSN, b.EndLSN, b.BinlogFile, pos); err != nil {
			return fmt.Errorf("catalog/sqlite: import backup %s: %w", b.ID, err)
		}
	}
	return nil
}

func importJobs(ctx context.Context, tx *sql.Tx, jobs []catalog.Job) error {
	for _, j := range jobs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO jobs (id, source_id, kind, trigger, status,
			                  error_class, error_detail, started_at, finished_at)
			VALUES (?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET
				source_id=excluded.source_id, kind=excluded.kind, trigger=excluded.trigger,
				status=excluded.status, error_class=excluded.error_class,
				error_detail=excluded.error_detail,
				started_at=excluded.started_at, finished_at=excluded.finished_at`,
			j.ID, j.SourceID, j.Kind, string(j.Trigger), string(j.Status),
			string(j.ErrorClass), j.ErrorDetail,
			toUnix(j.StartedAt), toUnix(j.FinishedAt)); err != nil {
			return fmt.Errorf("catalog/sqlite: import job %s: %w", j.ID, err)
		}
	}
	return nil
}

func importVerifications(ctx context.Context, tx *sql.Tx, verifications []catalog.Verification) error {
	for _, v := range verifications {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO verifications (id, backup_id, tier, status, report, started_at, finished_at)
			VALUES (?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET
				backup_id=excluded.backup_id, tier=excluded.tier, status=excluded.status,
				report=excluded.report,
				started_at=excluded.started_at, finished_at=excluded.finished_at`,
			v.ID, string(v.BackupID), v.Tier, string(v.Status), v.Report,
			toUnix(v.StartedAt), toUnix(v.FinishedAt)); err != nil {
			return fmt.Errorf("catalog/sqlite: import verification %s: %w", v.ID, err)
		}
	}
	return nil
}
