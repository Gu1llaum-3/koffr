package storage_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// The layout is the one thing a human falls back on when Koffr is unavailable
// (PD-001), so these tests pin the exact strings rather than a shape. A path
// that changes silently breaks every RESTORE.md already written.

func TestRepositoryEntries(t *testing.T) {
	if storage.DescriptorFile != "koffr.json" {
		t.Errorf("DescriptorFile = %q", storage.DescriptorFile)
	}
	if storage.RecoveryDoc != "RECOVERY.md" {
		t.Errorf("RecoveryDoc = %q", storage.RecoveryDoc)
	}
	if got := storage.CatalogLatestKey(); got != "catalog/latest.json.zst.age" {
		t.Errorf("CatalogLatestKey() = %q", got)
	}
}

func TestCatalogSnapshotKey(t *testing.T) {
	at := time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC)
	want := "catalog/2026-09-05T02-00-00Z.json.zst.age"
	if got := storage.CatalogSnapshotKey(at); got != want {
		t.Errorf("CatalogSnapshotKey() = %q, want %q", got, want)
	}
}

// A snapshot key must sort chronologically as a plain string: retention walks
// the catalog prefix and relies on lexical order matching time order.
func TestCatalogSnapshotKey_SortsChronologically(t *testing.T) {
	early := storage.CatalogSnapshotKey(time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC))
	late := storage.CatalogSnapshotKey(time.Date(2026, 9, 5, 14, 0, 0, 0, time.UTC))
	nextYear := storage.CatalogSnapshotKey(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	if !(early < late && late < nextYear) {
		t.Errorf("keys do not sort chronologically: %q %q %q", early, late, nextYear)
	}
}

// Snapshot keys are always UTC. A local timestamp would make the ordering above
// wrong twice a year.
func TestCatalogSnapshotKey_NormalisesToUTC(t *testing.T) {
	zone := time.FixedZone("CEST", 2*60*60)
	local := time.Date(2026, 9, 5, 4, 0, 0, 0, zone)
	want := "catalog/2026-09-05T02-00-00Z.json.zst.age"
	if got := storage.CatalogSnapshotKey(local); got != want {
		t.Errorf("CatalogSnapshotKey(local) = %q, want %q", got, want)
	}
}

func TestForSource_Keys(t *testing.T) {
	src, err := storage.ForSource("prod-pg-main")
	if err != nil {
		t.Fatalf("ForSource: %v", err)
	}

	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"lock", src.LockKey(), "locks/prod-pg-main.lock"},
		{"info", src.InfoKey(), "sources/prod-pg-main/source.json"},
		{"prefix", src.Prefix(), "sources/prod-pg-main/"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestBackupKeys(t *testing.T) {
	src, err := storage.ForSource("prod-pg-main")
	if err != nil {
		t.Fatalf("ForSource: %v", err)
	}
	b, err := src.Backup(storage.DirLogical, "01JQ8Z3K5M7P9R2T4V6X8Y0A2B")
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	const dir = "sources/prod-pg-main/logical/01JQ8Z3K5M7P9R2T4V6X8Y0A2B/"
	for _, tc := range []struct{ name, got, want string }{
		{"prefix", b.Prefix(), dir},
		{"manifest", b.ManifestKey(), dir + "manifest.json"},
		{"details", b.DetailsKey(), dir + "details.json.age"},
		{"restore doc", b.RestoreDocKey(), dir + "RESTORE.md"},
		{"object", b.ObjectKey("dump.pgdump.zst.age"), dir + "dump.pgdump.zst.age"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// Incremental backups live under physical/: the manifest records the kind, the
// layout only distinguishes the two families that hold artifacts.
func TestBackupKeys_PhysicalDir(t *testing.T) {
	src, _ := storage.ForSource("s1")
	b, err := src.Backup(storage.DirPhysical, "B1")
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if want := "sources/s1/physical/B1/base.tar.zst.age"; b.ObjectKey("base.tar.zst.age") != want {
		t.Errorf("ObjectKey = %q, want %q", b.ObjectKey("base.tar.zst.age"), want)
	}
}

// WAL segments keep their original name so a manual recovery is a download, a
// decrypt, a decompress and a rename -- no lookup table to understand. They are
// grouped by the first 16 characters, which is 256 segments per prefix.
func TestWALSegmentKey(t *testing.T) {
	src, _ := storage.ForSource("s1")
	got, err := src.WALSegmentKey("000000010000000000000001")
	if err != nil {
		t.Fatalf("WALSegmentKey: %v", err)
	}
	want := "sources/s1/wal/0000000100000000/000000010000000000000001.zst.age"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWALSegmentKey_Rejects(t *testing.T) {
	src, _ := storage.ForSource("s1")
	for _, bad := range []string{
		"",
		"tooshort",
		"00000001000000000000000",   // 23 chars
		"0000000100000000000000012", // 25 chars
		"00000001000000000000000g",  // not hex
		"../../etc/passwd",
	} {
		if _, err := src.WALSegmentKey(bad); err == nil {
			t.Errorf("WALSegmentKey(%q) accepted, want error", bad)
		}
	}
}

func TestBinlogKey(t *testing.T) {
	src, _ := storage.ForSource("s1")
	got, err := src.BinlogKey("mariadb-bin.000123")
	if err != nil {
		t.Fatalf("BinlogKey: %v", err)
	}
	if want := "sources/s1/binlog/mariadb-bin.000123.zst.age"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A source id comes from the configuration file, not from an attacker. But a
// path traversal that reaches an S3 key is a silent corruption of someone
// else's prefix, so it is rejected at the only place keys are built.
func TestForSource_RejectsUnsafeIDs(t *testing.T) {
	for _, bad := range []string{
		"",
		".",
		"..",
		"a/b",
		"a\\b",
		"../escape",
		"trailing/",
		"with space",
		"with\ttab",
		"with\nnewline",
		"with\x00null",
		"héllo",
		strings.Repeat("a", 129),
	} {
		if _, err := storage.ForSource(bad); err == nil {
			t.Errorf("ForSource(%q) accepted, want error", bad)
		}
	}
}

func TestForSource_AcceptsReasonableIDs(t *testing.T) {
	for _, ok := range []string{
		"a",
		"prod-pg-main",
		"pg_17_replica",
		"db.example.com",
		"CustomerA-2026",
		strings.Repeat("a", 128),
	} {
		if _, err := storage.ForSource(ok); err != nil {
			t.Errorf("ForSource(%q) rejected: %v", ok, err)
		}
	}
}

func TestBackup_RejectsUnsafeIDs(t *testing.T) {
	src, _ := storage.ForSource("s1")
	for _, bad := range []string{"", "..", "a/b", "with space"} {
		if _, err := src.Backup(storage.DirLogical, bad); err == nil {
			t.Errorf("Backup(%q) accepted, want error", bad)
		}
	}
	if _, err := src.Backup(storage.Dir("bogus"), "B1"); err == nil {
		t.Error("Backup with unknown dir accepted, want error")
	}
}

// ObjectKey takes a fixed name from our own code, never user input, but a
// traversal there would be just as damaging and just as invisible.
func TestObjectKey_PanicsOnUnsafeName(t *testing.T) {
	src, _ := storage.ForSource("s1")
	b, _ := src.Backup(storage.DirLogical, "B1")
	defer func() {
		if recover() == nil {
			t.Error("ObjectKey(\"../x\") did not panic")
		}
	}()
	_ = b.ObjectKey("../x")
}

// Every key the layout can produce must round-trip back into its parts, so a
// listing can be interpreted without a separate index.
func TestParse(t *testing.T) {
	src, _ := storage.ForSource("prod-pg-main")
	b, _ := src.Backup(storage.DirLogical, "B1")
	walKey, _ := src.WALSegmentKey("000000010000000000000001")
	binlogKey, _ := src.BinlogKey("mariadb-bin.000123")

	for _, tc := range []struct {
		key  string
		want storage.Ref
	}{
		{storage.DescriptorFile, storage.Ref{Kind: storage.RefDescriptor}},
		{storage.RecoveryDoc, storage.Ref{Kind: storage.RefRecoveryDoc}},
		{storage.CatalogLatestKey(), storage.Ref{Kind: storage.RefCatalog}},
		{src.LockKey(), storage.Ref{Kind: storage.RefLock, SourceID: "prod-pg-main"}},
		{src.InfoKey(), storage.Ref{Kind: storage.RefSourceInfo, SourceID: "prod-pg-main"}},
		{b.ManifestKey(), storage.Ref{
			Kind: storage.RefBackupObject, SourceID: "prod-pg-main",
			Dir: storage.DirLogical, BackupID: "B1", Object: "manifest.json",
		}},
		{walKey, storage.Ref{
			Kind: storage.RefWAL, SourceID: "prod-pg-main",
			Object: "000000010000000000000001",
		}},
		{binlogKey, storage.Ref{
			Kind: storage.RefBinlog, SourceID: "prod-pg-main",
			Object: "mariadb-bin.000123",
		}},
	} {
		got, err := storage.Parse(tc.key)
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.key, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Parse(%q) = %+v, want %+v", tc.key, got, tc.want)
		}
	}
}

func TestParse_Rejects(t *testing.T) {
	for _, bad := range []string{
		"",
		"unknown/thing",
		"sources/",
		"sources/s1",
		"sources/s1/logical",
		"sources/s1/bogus/B1/manifest.json",
		"locks/s1",       // missing .lock
		"catalog/x.json", // not the expected suffix
	} {
		if _, err := storage.Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted, want error", bad)
		}
	}
}

// The documented tree in layout.go is what a human reads before touching a
// repository by hand. If the code drifts from it, the documentation becomes a
// trap, so the tree is asserted rather than described.
func TestLayoutMatchesDocumentedTree(t *testing.T) {
	src, _ := storage.ForSource("prod-pg-main")
	logical, _ := src.Backup(storage.DirLogical, "B1")
	physical, _ := src.Backup(storage.DirPhysical, "B0")
	wal, _ := src.WALSegmentKey("000000010000000000000001")
	binlog, _ := src.BinlogKey("mariadb-bin.000123")

	want := []string{
		"catalog/2026-09-05T02-00-00Z.json.zst.age",
		"catalog/latest.json.zst.age",
		"koffr.json",
		"locks/prod-pg-main.lock",
		"RECOVERY.md",
		"sources/prod-pg-main/binlog/mariadb-bin.000123.zst.age",
		"sources/prod-pg-main/logical/B1/RESTORE.md",
		"sources/prod-pg-main/logical/B1/details.json.age",
		"sources/prod-pg-main/logical/B1/dump.pgdump.zst.age",
		"sources/prod-pg-main/logical/B1/globals.sql.zst.age",
		"sources/prod-pg-main/logical/B1/manifest.json",
		"sources/prod-pg-main/physical/B0/backup_manifest.json.age",
		"sources/prod-pg-main/physical/B0/base.tar.zst.age",
		"sources/prod-pg-main/physical/B0/manifest.json",
		"sources/prod-pg-main/source.json",
		"sources/prod-pg-main/wal/0000000100000000/000000010000000000000001.zst.age",
	}

	got := []string{
		storage.CatalogSnapshotKey(time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC)),
		storage.CatalogLatestKey(),
		storage.DescriptorFile,
		src.LockKey(),
		storage.RecoveryDoc,
		binlog,
		logical.RestoreDocKey(),
		logical.DetailsKey(),
		logical.ObjectKey("dump.pgdump.zst.age"),
		logical.ObjectKey("globals.sql.zst.age"),
		logical.ManifestKey(),
		physical.ObjectKey("backup_manifest.json.age"),
		physical.ObjectKey("base.tar.zst.age"),
		physical.ManifestKey(),
		src.InfoKey(),
		wal,
	}

	if len(got) != len(want) {
		t.Fatalf("got %d keys, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("key %d = %q, want %q", i, got[i], want[i])
		}
	}
}
