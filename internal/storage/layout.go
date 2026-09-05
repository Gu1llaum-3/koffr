package storage

// This file is the single source of truth for repository paths. No other
// package builds a key by hand; that is what will let the layout evolve without
// a hunt through string literals.
//
// The layout is deliberately readable without Koffr (PD-001). Extensions state
// exactly which operations to reverse, and WAL and binlog segments keep their
// original names so a manual recovery is a download, a decrypt, a decompress
// and a rename.
//
//	<repo>/
//	├── koffr.json                          format_version, repo_id, created_at
//	├── RECOVERY.md                         how to restore without koffr
//	├── locks/<source-id>.lock              repository-level exclusion (EF-045)
//	├── catalog/
//	│   ├── latest.json.zst.age             replicated catalog (EF-141)
//	│   └── <timestamp>.json.zst.age
//	└── sources/<source-id>/
//	    ├── source.json                     engine, version, non-sensitive settings
//	    ├── physical/<backup-id>/
//	    │   ├── manifest.json               PLAINTEXT: structure and digests (EF-055)
//	    │   ├── details.json.age            ENCRYPTED: database and table names
//	    │   ├── base.tar.zst.age            the stream
//	    │   ├── backup_manifest.json.age    reconstructed PG manifest (EF-013)
//	    │   └── RESTORE.md                  exact commands for THIS backup (EF-084)
//	    ├── logical/<backup-id>/
//	    │   ├── manifest.json
//	    │   ├── details.json.age
//	    │   ├── globals.sql.zst.age         roles and tablespaces (pg_dumpall)
//	    │   ├── dump.pgdump.zst.age
//	    │   └── RESTORE.md
//	    ├── wal/<segment-group>/<segment>.zst.age
//	    └── binlog/<binlog-file>.zst.age
//
// Path helpers are added in M1, when the first artifact is actually written.

// FormatVersion is the repository layout version recorded in koffr.json. It is
// bumped only for a change that an older Koffr could not read correctly.
const FormatVersion = 1

// Repository-level entries.
const (
	DescriptorFile = "koffr.json"
	RecoveryDoc    = "RECOVERY.md"
	LocksDir       = "locks"
	CatalogDir     = "catalog"
	SourcesDir     = "sources"
)

// Per-backup entries.
const (
	ManifestFile   = "manifest.json"
	DetailsFile    = "details.json.age"
	RestoreDoc     = "RESTORE.md"
	SourceInfoFile = "source.json"
)
