package storage

import (
	"fmt"
	"strings"
	"time"
)

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
// TestLayoutMatchesDocumentedTree asserts this tree rather than trusting it: a
// comment that drifts from the code is worse than no comment, because a human
// restoring by hand at 3 AM will believe it.

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
	RestoreDocFile = "RESTORE.md"
	SourceInfoFile = "source.json"
)

// catalogTimeLayout keeps snapshot keys sortable as plain strings and free of
// characters that are awkward in a filesystem path.
const catalogTimeLayout = "2006-01-02T15-04-05Z"

// Dir is the family of backup that holds artifacts. There are only two:
// an incremental backup lives under DirPhysical, and the manifest records that
// it is incremental. The layout does not need to know.
type Dir string

const (
	DirPhysical Dir = "physical"
	DirLogical  Dir = "logical"
)

func (d Dir) valid() bool { return d == DirPhysical || d == DirLogical }

const (
	walDir     = "wal"
	binlogDir  = "binlog"
	maxSegment = 128
	// walGroupLen groups segments 256 to a prefix, which keeps a listing
	// usable on a cluster that has been running for years.
	walGroupLen = 16
	walNameLen  = 24
)

// CatalogLatestKey is where the most recent replicated catalog lives (EF-141).
func CatalogLatestKey() string { return CatalogDir + "/latest.json.zst.age" }

// CatalogSnapshotKey is a point-in-time copy of the catalog. The timestamp is
// always normalised to UTC: a local one would break lexical ordering twice a
// year, and retention walks this prefix in lexical order.
func CatalogSnapshotKey(at time.Time) string {
	return CatalogDir + "/" + at.UTC().Format(catalogTimeLayout) + ".json.zst.age"
}

// Source builds every key belonging to one backup source. The identifier is
// validated once, here, so the rest of the API cannot fail.
type Source struct{ id string }

// ForSource validates a source identifier and returns its key builder.
func ForSource(id string) (Source, error) {
	if err := validSegment(id, "source id"); err != nil {
		return Source{}, err
	}
	return Source{id: id}, nil
}

// ID returns the validated source identifier.
func (s Source) ID() string { return s.id }

// Prefix lists everything belonging to this source. Rebuilding a lost catalog
// walks it (EF-143).
func (s Source) Prefix() string { return SourcesDir + "/" + s.id + "/" }

// LockKey is the repository-level exclusion marker for this source (EF-045).
func (s Source) LockKey() string { return LocksDir + "/" + s.id + ".lock" }

// InfoKey holds the engine, its version and non-sensitive settings.
func (s Source) InfoKey() string { return s.Prefix() + SourceInfoFile }

// Backup validates a backup identifier and returns its key builder.
func (s Source) Backup(dir Dir, backupID string) (Backup, error) {
	if !dir.valid() {
		return Backup{}, fmt.Errorf("layout: unknown backup dir %q", string(dir))
	}
	if err := validSegment(backupID, "backup id"); err != nil {
		return Backup{}, err
	}
	return Backup{src: s, dir: dir, id: backupID}, nil
}

// WALSegmentKey stores one WAL segment under its original name.
func (s Source) WALSegmentKey(segment string) (string, error) {
	if len(segment) != walNameLen || !isHexUpper(segment) {
		return "", fmt.Errorf("layout: %q is not a WAL segment name (want %d uppercase hex characters)",
			segment, walNameLen)
	}
	return s.Prefix() + walDir + "/" + segment[:walGroupLen] + "/" + segment + ".zst.age", nil
}

// BinlogKey stores one binlog file under its original name.
func (s Source) BinlogKey(name string) (string, error) {
	if err := validSegment(name, "binlog name"); err != nil {
		return "", err
	}
	return s.Prefix() + binlogDir + "/" + name + ".zst.age", nil
}

// Backup builds the keys of one backup's artifacts.
type Backup struct {
	src Source
	dir Dir
	id  string
}

// Prefix lists every artifact of this backup.
func (b Backup) Prefix() string {
	return b.src.Prefix() + string(b.dir) + "/" + b.id + "/"
}

// ManifestKey is the plaintext manifest: structure and digests, enough to list
// and prune without holding any key (EF-055).
func (b Backup) ManifestKey() string { return b.Prefix() + ManifestFile }

// DetailsKey is the encrypted extended metadata: database and table names.
func (b Backup) DetailsKey() string { return b.Prefix() + DetailsFile }

// RestoreDocKey is the generated, backup-specific restore procedure (EF-084).
func (b Backup) RestoreDocKey() string { return b.Prefix() + RestoreDocFile }

// ObjectKey names one artifact inside the backup.
//
// It panics rather than returning an error because every caller passes a
// constant from this package. A traversal here would corrupt a neighbouring
// prefix silently, so it is worth refusing loudly even though no user input
// reaches it.
func (b Backup) ObjectKey(name string) string {
	if err := validSegment(name, "object name"); err != nil {
		panic("layout: " + err.Error())
	}
	return b.Prefix() + name
}

// RefKind says what a parsed key points at.
type RefKind string

const (
	RefDescriptor   RefKind = "descriptor"
	RefRecoveryDoc  RefKind = "recovery-doc"
	RefCatalog      RefKind = "catalog"
	RefLock         RefKind = "lock"
	RefSourceInfo   RefKind = "source-info"
	RefBackupObject RefKind = "backup-object"
	RefWAL          RefKind = "wal"
	RefBinlog       RefKind = "binlog"
)

// Ref is a key decomposed into its parts, so a listing can be interpreted
// without a separate index. This is what makes EF-143 possible: the repository
// describes itself.
type Ref struct {
	Kind     RefKind
	SourceID string
	Dir      Dir
	BackupID string
	// Object is the artifact name for a backup object, or the original segment
	// or binlog name with its .zst.age suffix removed.
	Object string
}

// Parse decomposes a repository key. Every key this package can produce round
// trips; anything else is an error rather than a guess.
func Parse(key string) (Ref, error) {
	switch key {
	case DescriptorFile:
		return Ref{Kind: RefDescriptor}, nil
	case RecoveryDoc:
		return Ref{Kind: RefRecoveryDoc}, nil
	}

	parts := strings.Split(key, "/")
	switch parts[0] {
	case CatalogDir:
		if len(parts) != 2 || !strings.HasSuffix(parts[1], ".json.zst.age") {
			return Ref{}, fmt.Errorf("layout: %q is not a catalog key", key)
		}
		return Ref{Kind: RefCatalog}, nil

	case LocksDir:
		if len(parts) != 2 || !strings.HasSuffix(parts[1], ".lock") {
			return Ref{}, fmt.Errorf("layout: %q is not a lock key", key)
		}
		id := strings.TrimSuffix(parts[1], ".lock")
		if err := validSegment(id, "source id"); err != nil {
			return Ref{}, err
		}
		return Ref{Kind: RefLock, SourceID: id}, nil

	case SourcesDir:
		return parseSourceKey(key, parts)
	}
	return Ref{}, fmt.Errorf("layout: %q is not a repository key", key)
}

func parseSourceKey(key string, parts []string) (Ref, error) {
	if len(parts) < 3 {
		return Ref{}, fmt.Errorf("layout: %q is not a source key", key)
	}
	id := parts[1]
	if err := validSegment(id, "source id"); err != nil {
		return Ref{}, err
	}

	switch {
	case len(parts) == 3 && parts[2] == SourceInfoFile:
		return Ref{Kind: RefSourceInfo, SourceID: id}, nil

	case len(parts) == 5 && Dir(parts[2]).valid():
		if err := validSegment(parts[3], "backup id"); err != nil {
			return Ref{}, err
		}
		if err := validSegment(parts[4], "object name"); err != nil {
			return Ref{}, err
		}
		return Ref{
			Kind: RefBackupObject, SourceID: id,
			Dir: Dir(parts[2]), BackupID: parts[3], Object: parts[4],
		}, nil

	case len(parts) == 5 && parts[2] == walDir:
		name := strings.TrimSuffix(parts[4], ".zst.age")
		if name == parts[4] || len(name) != walNameLen || !isHexUpper(name) {
			return Ref{}, fmt.Errorf("layout: %q is not a WAL key", key)
		}
		if parts[3] != name[:walGroupLen] {
			return Ref{}, fmt.Errorf("layout: %q is filed under the wrong WAL group", key)
		}
		return Ref{Kind: RefWAL, SourceID: id, Object: name}, nil

	case len(parts) == 4 && parts[2] == binlogDir:
		name := strings.TrimSuffix(parts[3], ".zst.age")
		if name == parts[3] {
			return Ref{}, fmt.Errorf("layout: %q is not a binlog key", key)
		}
		if err := validSegment(name, "binlog name"); err != nil {
			return Ref{}, err
		}
		return Ref{Kind: RefBinlog, SourceID: id, Object: name}, nil
	}
	return Ref{}, fmt.Errorf("layout: %q is not a source key", key)
}

// validSegment accepts one path component that is safe in a filesystem path,
// in an S3 key and in a URL. The charset is deliberately narrow: an identifier
// comes from a configuration file written by a person, so there is no reason to
// accept anything that could be mistaken for a path or need escaping.
func validSegment(s, what string) error {
	if s == "" {
		return fmt.Errorf("layout: %s is empty", what)
	}
	if len(s) > maxSegment {
		return fmt.Errorf("layout: %s is %d characters, maximum is %d", what, len(s), maxSegment)
	}
	if s == "." || s == ".." {
		return fmt.Errorf("layout: %s cannot be %q", what, s)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.':
		default:
			return fmt.Errorf("layout: %s %q contains %q; allowed characters are letters, digits, '-', '_' and '.'",
				what, s, string(rune(c)))
		}
	}
	return nil
}

func isHexUpper(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
