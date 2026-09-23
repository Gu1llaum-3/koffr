package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/domain/catalog"
)

// Catalog is the index of E-028, on SQLite. It is the implementation of the
// port the domain declares: nothing above it knows there is a database here.
type Catalog struct {
	state *State
}

// NewCatalog builds the index on an open state.
func NewCatalog(state *State) *Catalog {
	return &Catalog{state: state}
}

// stamp is how ADR-0006 writes a timestamp: RFC 3339, UTC, and the schema has a
// GLOB that refuses anything else.
func stamp(at time.Time) string {
	return at.UTC().Format(time.RFC3339)
}

func parseStamp(written sql.NullString) time.Time {
	if !written.Valid {
		return time.Time{}
	}

	at, err := time.Parse(time.RFC3339, written.String)
	if err != nil {
		return time.Time{}
	}

	return at
}

// RecordDatabase stores the resolved configuration, or updates it in place.
func (c *Catalog) RecordDatabase(ctx context.Context, database catalog.Database, at time.Time) error {
	const statement = `
INSERT INTO databases (id, engine, host, port, "database", "user", fingerprint, resolved, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    engine = excluded.engine, host = excluded.host, port = excluded.port,
    "database" = excluded."database", "user" = excluded."user",
    fingerprint = excluded.fingerprint, resolved = excluded.resolved,
    updated_at = excluded.updated_at`

	written := stamp(at)

	_, err := c.state.DB().ExecContext(ctx, statement,
		database.ID, database.Engine, database.Host, database.Port,
		database.Name, database.User, database.Fingerprint(), database.Resolved(),
		written, written)
	if err != nil {
		return fmt.Errorf("record the database %s: %w", database.ID, err)
	}

	return nil
}

// RecordBackup indexes an archive and its copies, in one transaction: a backup
// half-indexed is worse than one not indexed at all.
func (c *Catalog) RecordBackup(ctx context.Context, backup catalog.Backup) error {
	transaction, err := c.state.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record the backup %s: %w", backup.ID, err)
	}
	defer func() { _ = transaction.Rollback() }()

	verified := backup.Verified
	if verified == "" {
		verified = catalog.NotVerified
	}

	const insertBackup = `
INSERT INTO backups (id, database_id, job_id, started_at, finished_at,
                     size_bytes, stored_bytes, sha256_raw, sha256_stored,
                     manifest, verified, verified_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	written := stamp(backup.StartedAt)

	if _, err := transaction.ExecContext(ctx, insertBackup,
		backup.ID, backup.Database, nullable(backup.Job),
		written, nullableStamp(backup.FinishedAt),
		backup.RawBytes, backup.StoredBytes,
		nullable(backup.SHA256Raw), nullable(backup.SHA256Stored),
		nullable(backup.Manifest), string(verified), nullableStamp(backup.VerifiedAt),
		written, written,
	); err != nil {
		return fmt.Errorf("record the backup %s of %s: %w", backup.ID, backup.Database, err)
	}

	const insertLocation = `
INSERT INTO backup_locations (backup_id, destination_id, status, remote_path, size_bytes, stored_at, created_at, updated_at)
VALUES (?, ?, 'stored', ?, ?, ?, ?, ?)`

	for _, location := range backup.Locations {
		if _, err := transaction.ExecContext(ctx, insertLocation,
			backup.ID, location.Destination, location.Path, location.Bytes,
			nullableStamp(location.StoredAt), written, written,
		); err != nil {
			return fmt.Errorf("record the copy of %s on %s: %w", backup.ID, location.Destination, err)
		}
	}

	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("record the backup %s: %w", backup.ID, err)
	}

	return nil
}

// SetVerification records what a verification concluded.
func (c *Catalog) SetVerification(
	ctx context.Context, id string, verified catalog.Verification, at time.Time,
) error {
	const statement = `UPDATE backups SET verified = ?, verified_at = ?, updated_at = ? WHERE id = ?`

	written := stamp(at)

	done, err := c.state.DB().ExecContext(ctx, statement, string(verified), written, written, id)
	if err != nil {
		return fmt.Errorf("record the verification %q of %s: %w", verified, id, err)
	}

	changed, err := done.RowsAffected()
	if err != nil {
		return fmt.Errorf("record the verification of %s: %w", id, err)
	}

	if changed == 0 {
		return fmt.Errorf("%w: %s", catalog.ErrNoSuchBackup, id)
	}

	return nil
}

// Backups lists what is indexed, most recent first.
func (c *Catalog) Backups(ctx context.Context, filter catalog.Filter) ([]catalog.Backup, error) {
	statement := `
SELECT id, database_id, COALESCE(job_id, ''), started_at, finished_at,
       COALESCE(size_bytes, 0), COALESCE(stored_bytes, 0),
       COALESCE(sha256_raw, ''), COALESCE(sha256_stored, ''),
       COALESCE(manifest, ''), verified, verified_at
FROM backups`

	var arguments []any

	if filter.Database != "" {
		statement += ` WHERE database_id = ?`

		arguments = append(arguments, filter.Database)
	}

	statement += ` ORDER BY started_at DESC, id DESC`

	if filter.Limit > 0 {
		statement += ` LIMIT ?`

		arguments = append(arguments, filter.Limit)
	}

	rows, err := c.state.DB().QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list the backups: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var listed []catalog.Backup

	for rows.Next() {
		var (
			backup                 catalog.Backup
			startedAt              string
			finishedAt, verifiedAt sql.NullString
			verified               string
		)

		if err := rows.Scan(&backup.ID, &backup.Database, &backup.Job, &startedAt, &finishedAt,
			&backup.RawBytes, &backup.StoredBytes, &backup.SHA256Raw, &backup.SHA256Stored,
			&backup.Manifest, &verified, &verifiedAt); err != nil {
			return nil, fmt.Errorf("read a backup: %w", err)
		}

		backup.StartedAt = parseStamp(sql.NullString{String: startedAt, Valid: true})
		backup.FinishedAt = parseStamp(finishedAt)
		backup.VerifiedAt = parseStamp(verifiedAt)
		backup.Verified = catalog.Verification(verified)

		listed = append(listed, backup)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list the backups: %w", err)
	}

	return c.withLocations(ctx, listed, filter)
}

// withLocations fills in where each archive is. A listing that needed one query
// per archive would be a listing nobody runs on a fleet.
func (c *Catalog) withLocations(
	ctx context.Context, listed []catalog.Backup, filter catalog.Filter,
) ([]catalog.Backup, error) {
	if len(listed) == 0 {
		return nil, nil
	}

	statement := `
SELECT backup_id, destination_id, COALESCE(remote_path, ''), COALESCE(size_bytes, 0), stored_at
FROM backup_locations`

	var arguments []any

	if filter.Destination != "" {
		statement += ` WHERE destination_id = ?`

		arguments = append(arguments, filter.Destination)
	}

	statement += ` ORDER BY destination_id`

	rows, err := c.state.DB().QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list the copies: %w", err)
	}
	defer func() { _ = rows.Close() }()

	copies := map[string][]catalog.Location{}

	for rows.Next() {
		var (
			of       string
			location catalog.Location
			storedAt sql.NullString
		)

		if err := rows.Scan(&of, &location.Destination, &location.Path, &location.Bytes, &storedAt); err != nil {
			return nil, fmt.Errorf("read a copy: %w", err)
		}

		location.StoredAt = parseStamp(storedAt)
		copies[of] = append(copies[of], location)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list the copies: %w", err)
	}

	kept := listed[:0]

	for _, backup := range listed {
		backup.Locations = copies[backup.ID]

		// A filter on a destination keeps only the archives that are there.
		if filter.Destination != "" && len(backup.Locations) == 0 {
			continue
		}

		kept = append(kept, backup)
	}

	return kept, nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}

	return value
}

func nullableStamp(at time.Time) any {
	if at.IsZero() {
		return nil
	}

	return stamp(at)
}
