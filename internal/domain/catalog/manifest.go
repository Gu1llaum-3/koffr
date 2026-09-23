package catalog

import "time"

// Manifest is the JSON deposited beside the archive, on **every** destination,
// unencrypted and without a credential (E-057, E-059).
//
// Its shape is the one the § 5.3 shows, field for field. It is what makes the
// catalogue an index rather than the source of truth: with the manifests and a
// private key, a repository is inventoried and restored without koffr, without
// its machine and without its SQLite (ADR-0006).
type Manifest struct {
	BackupID   string `json:"backup_id"`
	DatabaseID string `json:"database_id"`
	Engine     string `json:"engine"`

	StartedAt  string `json:"started_at"`
	DurationMS int64  `json:"duration_ms"`

	ServerVersion string `json:"server_version"`
	Tool          Tool   `json:"tool"`

	Format   string   `json:"format"`
	Pipeline []string `json:"pipeline"`
	Staging  string   `json:"staging"`

	SizeRaw      int64  `json:"size_raw"`
	SizeStored   int64  `json:"size_stored"`
	SHA256Raw    string `json:"sha256_raw"`
	SHA256Stored string `json:"sha256_stored"`

	Recipients []string `json:"recipients"`
	Verified   Verified `json:"verified"`
}

// Tool is what produced the dump: enough to know what can read it back.
type Tool struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Source  string   `json:"source"`
	Path    string   `json:"path"`
	Argv    []string `json:"argv"`
}

// Verified is the state of P4, as the manifest carries it. Two booleans and a
// moment, exactly as the § 5.3 writes it.
type Verified struct {
	Checksum  bool   `json:"checksum"`
	Structure bool   `json:"structure"`
	At        string `json:"at,omitempty"`
}

// Run is what the job knew about itself and that the catalogue does not keep:
// the server, the tool, the shape of the archive and the keys it was written
// for.
type Run struct {
	ServerVersion string
	Tool          Tool
	Format        string
	Pipeline      []string
	Staging       string
	Recipients    []string
}

// ManifestOf assembles the manifest of a finished backup.
func ManifestOf(backup Backup, database Database, run Run) Manifest {
	manifest := Manifest{
		BackupID: backup.ID, DatabaseID: backup.Database, Engine: database.Engine,
		StartedAt:     backup.StartedAt.UTC().Format(time.RFC3339),
		ServerVersion: run.ServerVersion, Tool: run.Tool,
		Format: run.Format, Pipeline: run.Pipeline, Staging: run.Staging,
		SizeRaw: backup.RawBytes, SizeStored: backup.StoredBytes,
		SHA256Raw: backup.SHA256Raw, SHA256Stored: backup.SHA256Stored,
		Recipients: run.Recipients,
		Verified: Verified{
			// P4 — an archive nobody checked claims nothing.
			Checksum:  backup.Verified == Checksum || backup.Verified == Structure,
			Structure: backup.Verified == Structure,
		},
	}

	if !backup.FinishedAt.IsZero() {
		manifest.DurationMS = backup.FinishedAt.Sub(backup.StartedAt).Milliseconds()
	}

	if !backup.VerifiedAt.IsZero() && manifest.Verified.Checksum {
		manifest.Verified.At = backup.VerifiedAt.UTC().Format(time.RFC3339)
	}

	return manifest
}

// Backup rebuilds the catalogue entry from the manifest alone. This is what
// "the catalogue is an index" means, and the only way to state it that proves
// anything: the manifest is read back without the catalogue in sight.
func (m Manifest) Backup() Backup {
	backup := Backup{
		ID: m.BackupID, Database: m.DatabaseID,
		RawBytes: m.SizeRaw, StoredBytes: m.SizeStored,
		SHA256Raw: m.SHA256Raw, SHA256Stored: m.SHA256Stored,
		Verified: NotVerified,
	}

	if at, err := time.Parse(time.RFC3339, m.StartedAt); err == nil {
		backup.StartedAt = at
		backup.FinishedAt = at.Add(time.Duration(m.DurationMS) * time.Millisecond)
	}

	switch {
	case m.Verified.Structure:
		backup.Verified = Structure

	case m.Verified.Checksum:
		backup.Verified = Checksum
	}

	if at, err := time.Parse(time.RFC3339, m.Verified.At); err == nil {
		backup.VerifiedAt = at
	}

	return backup
}
