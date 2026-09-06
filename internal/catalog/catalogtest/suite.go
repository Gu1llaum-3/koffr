// Package catalogtest is the contract every catalog.MetadataStore must satisfy.
//
// The catalog is a cache, not the truth (DEC-004), and that shapes what is
// worth testing. Losing it must cost time, never data, so nothing here assumes
// it is authoritative. What it must get right is the reasoning built on top of
// it: which backups depend on which, and how old the last good one is. Both
// decide whether data is deleted.
package catalogtest

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
)

// Harness is what a backend provides to be put through the contract.
type Harness struct {
	// New returns an empty store.
	New func(t *testing.T) catalog.MetadataStore

	// Reopen closes a store and opens the same underlying data again. It is
	// how persistence is checked; leave it nil for a backend that keeps
	// nothing.
	Reopen func(t *testing.T, store catalog.MetadataStore) catalog.MetadataStore
}

// Suite runs the whole contract.
func Suite(t *testing.T, h Harness) {
	t.Helper()

	t.Run("RecordAndList", func(t *testing.T) { testRecordAndList(t, h) })
	t.Run("ListFilters", func(t *testing.T) { testListFilters(t, h) })
	t.Run("ListIsNewestFirst", func(t *testing.T) { testListOrder(t, h) })
	t.Run("RecordIsIdempotent", func(t *testing.T) { testIdempotent(t, h) })
	t.Run("RecordUpdatesAnExistingBackup", func(t *testing.T) { testRecordUpdates(t, h) })
	t.Run("Chain", func(t *testing.T) { testChain(t, h) })
	t.Run("ChainOfAnUnknownBackup", func(t *testing.T) { testChainUnknown(t, h) })
	t.Run("ChainStopsOnABrokenLink", func(t *testing.T) { testChainBroken(t, h) })
	t.Run("Jobs", func(t *testing.T) { testJobs(t, h) })
	t.Run("Verifications", func(t *testing.T) { testVerifications(t, h) })
	t.Run("Overview", func(t *testing.T) { testOverview(t, h) })
	t.Run("OverviewOfAnEmptyStore", func(t *testing.T) { testOverviewEmpty(t, h) })
	t.Run("ConcurrentWriters", func(t *testing.T) { testConcurrentWriters(t, h) })
	t.Run("CloseIsIdempotent", func(t *testing.T) { testCloseIdempotent(t, h) })
	t.Run("Persistence", func(t *testing.T) { testPersistence(t, h) })
	t.Run("TimestampsSurviveARoundTrip", func(t *testing.T) { testTimestamps(t, h) })
	t.Run("ForgetBackup", func(t *testing.T) { testForgetBackup(t, h) })
	t.Run("ExportImportRoundTrip", func(t *testing.T) { testExportImport(t, h) })
	t.Run("ImportIsIdempotent", func(t *testing.T) { testImportIdempotent(t, h) })
	t.Run("ImportIsAdditive", func(t *testing.T) { testImportAdditive(t, h) })
	t.Run("ExportOfAnEmptyStore", func(t *testing.T) { testExportEmpty(t, h) })
	t.Run("ImportRefusesANewerFormat", func(t *testing.T) { testImportNewerFormat(t, h) })
}

// at is a fixed instant so ordering assertions do not depend on the clock.
var at = time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC)

func backup(id catalog.ID, source string, offset time.Duration) catalog.Backup {
	return catalog.Backup{
		ID:          id,
		SourceID:    source,
		Kind:        "logical",
		Destination: "s3://backups",
		Status:      catalog.StatusCompleted,
		StartedAt:   at.Add(offset),
		FinishedAt:  at.Add(offset + 10*time.Minute),
		SizeBytes:   1 << 20,
		ManifestKey: fmt.Sprintf("sources/%s/logical/%s/manifest.json", source, id),
	}
}

func record(t *testing.T, s catalog.MetadataStore, b catalog.Backup) {
	t.Helper()
	require.NoError(t, s.RecordBackup(t.Context(), b))
}

func testRecordAndList(t *testing.T, h Harness) {
	s := h.New(t)
	want := backup("B1", "prod", 0)
	want.StartLSN, want.EndLSN = "0/3000060", "0/3000220"
	record(t, s, want)

	got, err := s.ListBackups(t.Context(), catalog.BackupFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, want, got[0], "a backup must come back exactly as it went in")
}

func testListFilters(t *testing.T, h Harness) {
	s := h.New(t)

	b1 := backup("B1", "prod", 0)
	b2 := backup("B2", "prod", time.Hour)
	b2.Kind = "physical"
	b3 := backup("B3", "staging", 2*time.Hour)
	b4 := backup("B4", "prod", 3*time.Hour)
	b4.Status = catalog.StatusFailed
	b4.Destination = "fs:///var/backups"
	for _, b := range []catalog.Backup{b1, b2, b3, b4} {
		record(t, s, b)
	}

	ids := func(f catalog.BackupFilter) []catalog.ID {
		got, err := s.ListBackups(t.Context(), f)
		require.NoError(t, err)
		out := make([]catalog.ID, 0, len(got))
		for _, b := range got {
			out = append(out, b.ID)
		}
		return out
	}

	assert.ElementsMatch(t, []catalog.ID{"B1", "B2", "B4"}, ids(catalog.BackupFilter{SourceID: "prod"}))
	assert.ElementsMatch(t, []catalog.ID{"B2"}, ids(catalog.BackupFilter{Kind: "physical"}))
	assert.ElementsMatch(t, []catalog.ID{"B4"}, ids(catalog.BackupFilter{Status: catalog.StatusFailed}))
	assert.ElementsMatch(t, []catalog.ID{"B4"}, ids(catalog.BackupFilter{Destination: "fs:///var/backups"}))

	// A window is half-open: Since is inclusive, Until is not. Retention walks
	// these boundaries, and an off-by-one there deletes a backup that was meant
	// to be kept.
	assert.ElementsMatch(t, []catalog.ID{"B2", "B3"}, ids(catalog.BackupFilter{
		Since: at.Add(time.Hour),
		Until: at.Add(3 * time.Hour),
	}))

	assert.Len(t, ids(catalog.BackupFilter{Limit: 2}), 2)
	assert.Empty(t, ids(catalog.BackupFilter{SourceID: "does-not-exist"}))
}

// Newest first is what every caller wants: the CLI shows recent backups, and
// retention starts from the newest and works back.
func testListOrder(t *testing.T, h Harness) {
	s := h.New(t)
	record(t, s, backup("B2", "prod", time.Hour))
	record(t, s, backup("B1", "prod", 0))
	record(t, s, backup("B3", "prod", 2*time.Hour))

	got, err := s.ListBackups(t.Context(), catalog.BackupFilter{})
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, []catalog.ID{"B3", "B2", "B1"},
		[]catalog.ID{got[0].ID, got[1].ID, got[2].ID})
}

// A job that is retried records the same backup twice. That must not create a
// duplicate, or retention counts one backup as two and deletes something to get
// back under the limit.
func testIdempotent(t *testing.T, h Harness) {
	s := h.New(t)
	b := backup("B1", "prod", 0)
	record(t, s, b)
	record(t, s, b)

	got, err := s.ListBackups(t.Context(), catalog.BackupFilter{})
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

// A backup is recorded when it starts and again when it finishes, so the second
// write has to replace the first rather than be ignored.
func testRecordUpdates(t *testing.T, h Harness) {
	s := h.New(t)
	running := backup("B1", "prod", 0)
	running.Status = catalog.StatusRunning
	running.SizeBytes = 0
	running.FinishedAt = time.Time{}
	record(t, s, running)

	done := backup("B1", "prod", 0)
	done.SizeBytes = 42 << 20
	record(t, s, done)

	got, err := s.ListBackups(t.Context(), catalog.BackupFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, catalog.StatusCompleted, got[0].Status)
	assert.Equal(t, int64(42<<20), got[0].SizeBytes)
}

// EF-062: retention asks for the chain before deleting anything, because a full
// backup an incremental depends on cannot go. Physical backups arrive in M2,
// but the reasoning is pure and cheap, so it is built and tested now.
func testChain(t *testing.T, h Harness) {
	s := h.New(t)
	full := backup("B1", "prod", 0)
	incr1 := backup("B2", "prod", time.Hour)
	incr1.ParentID = "B1"
	incr2 := backup("B3", "prod", 2*time.Hour)
	incr2.ParentID = "B2"
	for _, b := range []catalog.Backup{full, incr1, incr2} {
		record(t, s, b)
	}

	got, err := s.Chain(t.Context(), "B3")
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, []catalog.ID{"B3", "B2", "B1"},
		[]catalog.ID{got[0].ID, got[1].ID, got[2].ID},
		"the chain runs from the backup asked for back to the full it rests on")

	got, err = s.Chain(t.Context(), "B1")
	require.NoError(t, err)
	require.Len(t, got, 1, "a full backup depends on nothing")
}

func testChainUnknown(t *testing.T, h Harness) {
	s := h.New(t)
	_, err := s.Chain(t.Context(), "nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope")
}

// A parent that is not in the catalog means the chain cannot be proven whole,
// and an unprovable chain must not be reported as a complete one: retention
// would read it as "nothing depends on this" and delete the wrong thing.
func testChainBroken(t *testing.T, h Harness) {
	s := h.New(t)
	orphan := backup("B2", "prod", time.Hour)
	orphan.ParentID = "B1-missing"
	record(t, s, orphan)

	_, err := s.Chain(t.Context(), "B2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "B1-missing")
}

func testJobs(t *testing.T, h Harness) {
	s := h.New(t)
	require.NoError(t, s.RecordJob(t.Context(), catalog.Job{
		ID:          "J1",
		SourceID:    "prod",
		Kind:        "logical",
		Trigger:     catalog.TriggerSchedule,
		Status:      catalog.StatusFailed,
		ErrorClass:  catalog.ErrClassStorage,
		ErrorDetail: "bucket unreachable",
		StartedAt:   at,
		FinishedAt:  at.Add(time.Minute),
	}))

	// Recording the same job twice, as a retry would.
	require.NoError(t, s.RecordJob(t.Context(), catalog.Job{
		ID: "J1", SourceID: "prod", Kind: "logical",
		Trigger: catalog.TriggerSchedule, Status: catalog.StatusCompleted,
		StartedAt: at, FinishedAt: at.Add(2 * time.Minute),
	}))

	overview, err := s.Overview(t.Context())
	require.NoError(t, err)
	require.Len(t, overview.Sources, 1)
	assert.True(t, overview.Sources[0].LastFailureAt.IsZero(),
		"the job succeeded on the second attempt, so no failure stands")
}

func testVerifications(t *testing.T, h Harness) {
	s := h.New(t)
	record(t, s, backup("B1", "prod", 0))

	require.NoError(t, s.RecordVerification(t.Context(), catalog.Verification{
		ID:         "V1",
		BackupID:   "B1",
		Tier:       1,
		Status:     catalog.StatusCompleted,
		Report:     []byte(`{"passed":true}`),
		StartedAt:  at.Add(time.Hour),
		FinishedAt: at.Add(time.Hour + time.Minute),
	}))

	overview, err := s.Overview(t.Context())
	require.NoError(t, err)
	require.Len(t, overview.Sources, 1)
	assert.Equal(t, at.Add(time.Hour+time.Minute).UTC(), overview.Sources[0].LastVerifiedAt.UTC())
}

// The overview answers the only question monitoring really asks: how old is the
// last thing we know to be good (EF-134).
func testOverview(t *testing.T, h Harness) {
	s := h.New(t)

	ok1 := backup("B1", "prod", 0)
	ok2 := backup("B2", "prod", time.Hour)
	failed := backup("B3", "prod", 2*time.Hour)
	failed.Status = catalog.StatusFailed
	other := backup("B4", "staging", 0)
	for _, b := range []catalog.Backup{ok1, ok2, failed, other} {
		record(t, s, b)
	}
	require.NoError(t, s.RecordJob(t.Context(), catalog.Job{
		ID: "J1", SourceID: "prod", Kind: "logical", Trigger: catalog.TriggerSchedule,
		Status: catalog.StatusFailed, ErrorClass: catalog.ErrClassSource,
		StartedAt: at.Add(3 * time.Hour), FinishedAt: at.Add(3 * time.Hour),
	}))

	overview, err := s.Overview(t.Context())
	require.NoError(t, err)
	require.Len(t, overview.Sources, 2)

	bySource := map[string]catalog.SourceOverview{}
	for _, so := range overview.Sources {
		bySource[so.SourceID] = so
	}

	prod := bySource["prod"]
	assert.Equal(t, at.Add(time.Hour+10*time.Minute).UTC(), prod.LastSuccessfulAt.UTC(),
		"a failed backup must not count as the last successful one")
	assert.Equal(t, at.Add(3*time.Hour).UTC(), prod.LastFailureAt.UTC())
	assert.Equal(t, catalog.ErrClassSource, prod.LastFailureClass)
	assert.Equal(t, 2, prod.BackupCount, "only completed backups are countable")
	assert.Equal(t, int64(2<<20), prod.TotalSizeBytes)
	assert.Equal(t, at.UTC(), prod.OldestRestorableAt.UTC())

	assert.Equal(t, 1, bySource["staging"].BackupCount)
}

func testOverviewEmpty(t *testing.T, h Harness) {
	s := h.New(t)
	overview, err := s.Overview(t.Context())
	require.NoError(t, err, "an empty catalog is a normal state, not an error")
	assert.Empty(t, overview.Sources)
}

// The scheduler runs jobs concurrently, so writes overlap by design. A store
// that corrupted or lost one under contention would lose the record of a backup
// that exists, which is how a repository grows objects nothing points at.
func testConcurrentWriters(t *testing.T, h Harness) {
	s := h.New(t)
	const writers = 8
	const each = 10

	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				id := catalog.ID(fmt.Sprintf("B%d-%d", w, i))
				assert.NoError(t, s.RecordBackup(t.Context(), backup(id, "prod", time.Duration(i)*time.Minute)))
			}
		}()
	}
	wg.Wait()

	got, err := s.ListBackups(t.Context(), catalog.BackupFilter{})
	require.NoError(t, err)
	assert.Len(t, got, writers*each, "a write was lost under contention")
}

func testCloseIdempotent(t *testing.T, h Harness) {
	s := h.New(t)
	require.NoError(t, s.Close())
	assert.NoError(t, s.Close(), "Close runs from deferred teardown and may run twice")
}

// EF-141: the catalog survives a restart. It is only a cache of the repository,
// but rebuilding it on every start would make listing a backup an operation
// against object storage.
func testPersistence(t *testing.T, h Harness) {
	if h.Reopen == nil {
		t.Skip("backend keeps nothing across a restart")
	}
	s := h.New(t)
	record(t, s, backup("B1", "prod", 0))

	s = h.Reopen(t, s)
	got, err := s.ListBackups(t.Context(), catalog.BackupFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, catalog.ID("B1"), got[0].ID)
}

// A timestamp that comes back in another zone, or truncated, breaks retention
// by age and misreports how stale a backup is.
func testTimestamps(t *testing.T, h Harness) {
	s := h.New(t)
	b := backup("B1", "prod", 0)
	b.StartedAt = time.Date(2026, 9, 5, 4, 30, 15, 0, time.FixedZone("CEST", 2*60*60))
	b.FinishedAt = b.StartedAt.Add(90 * time.Second)
	record(t, s, b)

	got, err := s.ListBackups(t.Context(), catalog.BackupFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, b.StartedAt.Equal(got[0].StartedAt),
		"want %s, got %s", b.StartedAt, got[0].StartedAt)
	assert.True(t, b.FinishedAt.Equal(got[0].FinishedAt))
}

// The catalog is a cache, and Export is what makes that true rather than
// merely stated. Everything it holds has to survive a round trip through a
// snapshot, or "rebuild from the repository" quietly means "rebuild most of it".
func testExportImport(t *testing.T, h Harness) {
	src := h.New(t)
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	backups := []catalog.Backup{
		{
			ID: "01BACKUPPARENT000000000000", SourceID: "prod", Kind: "logical",
			Destination: "main", Status: catalog.StatusCompleted,
			StartedAt: base, FinishedAt: base.Add(time.Minute),
			SizeBytes: 1024, ManifestKey: "sources/prod/logical/01BACKUPPARENT000000000000/manifest.json",
			StartLSN: "0/1000000", EndLSN: "0/2000000",
		},
		{
			ID: "01BACKUPCHILD0000000000000", SourceID: "prod", Kind: "logical",
			ParentID:    "01BACKUPPARENT000000000000",
			Destination: "main", Status: catalog.StatusCompleted,
			StartedAt: base.Add(time.Hour), FinishedAt: base.Add(time.Hour + time.Minute),
			SizeBytes: 512, ManifestKey: "sources/prod/logical/01BACKUPCHILD0000000000000/manifest.json",
		},
	}
	for _, b := range backups {
		require.NoError(t, src.RecordBackup(t.Context(), b))
	}

	// A failed job is the reason this matters most: it produces no manifest, so
	// it exists in the catalog and nowhere else. Losing it loses the only
	// record that a backup was attempted and did not happen.
	jobs := []catalog.Job{
		{
			ID: "01JOBOK0000000000000000000", SourceID: "prod", Kind: "logical",
			Trigger: catalog.TriggerSchedule, Status: catalog.StatusCompleted,
			StartedAt: base, FinishedAt: base.Add(time.Minute),
		},
		{
			ID: "01JOBFAILED000000000000000", SourceID: "prod", Kind: "logical",
			Trigger: catalog.TriggerSchedule, Status: catalog.StatusFailed,
			ErrorClass: catalog.ErrClassSource, ErrorDetail: "pg_dump exited with status 1",
			StartedAt: base.Add(2 * time.Hour), FinishedAt: base.Add(2*time.Hour + time.Second),
		},
	}
	for _, j := range jobs {
		require.NoError(t, src.RecordJob(t.Context(), j))
	}

	verification := catalog.Verification{
		ID: "01VERIFY000000000000000000", BackupID: "01BACKUPPARENT000000000000",
		Tier: 2, Status: catalog.StatusCompleted, Report: []byte(`{"objects":3}`),
		StartedAt: base.Add(3 * time.Hour), FinishedAt: base.Add(3*time.Hour + time.Minute),
	}
	require.NoError(t, src.RecordVerification(t.Context(), verification))

	snap, err := src.Export(t.Context())
	require.NoError(t, err)
	assert.Equal(t, catalog.SnapshotFormatVersion, snap.FormatVersion)
	assert.False(t, snap.ExportedAt.IsZero(), "a snapshot with no date cannot be compared to another")

	// A snapshot that cannot travel as JSON cannot live in the repository.
	encoded, err := json.Marshal(snap)
	require.NoError(t, err)
	var travelled catalog.Snapshot
	require.NoError(t, json.Unmarshal(encoded, &travelled))

	dst := h.New(t)
	require.NoError(t, dst.Import(t.Context(), travelled))

	gotBackups, err := dst.ListBackups(t.Context(), catalog.BackupFilter{})
	require.NoError(t, err)
	require.Len(t, gotBackups, len(backups))

	byID := map[catalog.ID]catalog.Backup{}
	for _, b := range gotBackups {
		byID[b.ID] = b
	}
	for _, want := range backups {
		got, ok := byID[want.ID]
		require.True(t, ok, "backup %s did not survive the round trip", want.ID)
		assert.Equal(t, want.SourceID, got.SourceID)
		assert.Equal(t, want.ParentID, got.ParentID, "a lost parent breaks retention's chain walk")
		assert.Equal(t, want.SizeBytes, got.SizeBytes)
		assert.Equal(t, want.ManifestKey, got.ManifestKey)
		assert.Equal(t, want.StartLSN, got.StartLSN)
		assert.True(t, want.StartedAt.Equal(got.StartedAt), "want %s got %s", want.StartedAt, got.StartedAt)
	}

	// The chain has to hold on the far side, or an incremental restored from a
	// rebuilt catalog has no ancestors.
	chain, err := dst.Chain(t.Context(), "01BACKUPCHILD0000000000000")
	require.NoError(t, err)
	assert.Len(t, chain, 2)

	after, err := dst.Export(t.Context())
	require.NoError(t, err)
	assert.Len(t, after.Jobs, len(jobs), "the job history is the part that exists nowhere else")
	assert.Len(t, after.Verifications, 1)

	var failed catalog.Job
	for _, j := range after.Jobs {
		if j.Status == catalog.StatusFailed {
			failed = j
		}
	}
	assert.Equal(t, catalog.ErrClassSource, failed.ErrorClass)
	assert.Equal(t, "pg_dump exited with status 1", failed.ErrorDetail,
		"why a job failed is the whole value of keeping it")

	for _, v := range after.Verifications {
		assert.JSONEq(t, string(verification.Report), string(v.Report))
	}
}

// Rebuilding twice must not double anything. A sync is something an operator
// runs when unsure, which means running it again when still unsure.
func testImportIdempotent(t *testing.T, h Harness) {
	src := h.New(t)
	require.NoError(t, src.RecordBackup(t.Context(), sampleBackup()))
	require.NoError(t, src.RecordJob(t.Context(), sampleJob()))

	snap, err := src.Export(t.Context())
	require.NoError(t, err)

	dst := h.New(t)
	require.NoError(t, dst.Import(t.Context(), snap))
	require.NoError(t, dst.Import(t.Context(), snap))

	after, err := dst.Export(t.Context())
	require.NoError(t, err)
	assert.Len(t, after.Backups, 1)
	assert.Len(t, after.Jobs, 1)
}

// Import merges, it does not replace. A rebuild often runs on a catalog that is
// behind rather than empty, and dropping the jobs recorded since the last
// replication would be a repair that loses data.
func testImportAdditive(t *testing.T, h Harness) {
	store := h.New(t)

	local := sampleBackup()
	local.ID = "01LOCALONLY0000000000000000"
	require.NoError(t, store.RecordBackup(t.Context(), local))

	other := h.New(t)
	require.NoError(t, other.RecordBackup(t.Context(), sampleBackup()))
	snap, err := other.Export(t.Context())
	require.NoError(t, err)

	require.NoError(t, store.Import(t.Context(), snap))

	got, err := store.ListBackups(t.Context(), catalog.BackupFilter{})
	require.NoError(t, err)
	assert.Len(t, got, 2, "the row that was only here must survive being told about the other one")
}

func testExportEmpty(t *testing.T, h Harness) {
	snap, err := h.New(t).Export(t.Context())
	require.NoError(t, err)
	assert.Empty(t, snap.Backups)
	assert.Empty(t, snap.Jobs)
	assert.Empty(t, snap.Verifications)

	// Empty, not nil: this gets marshalled into the repository, and `[]` reads
	// as "nothing here" where `null` reads as "something went wrong".
	encoded, err := json.Marshal(snap)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"backups":[]`)
}

// A snapshot from a future Koffr is refused rather than half-understood. Silent
// partial imports are how a catalog ends up wrong in a way nobody notices.
func testImportNewerFormat(t *testing.T, h Harness) {
	store := h.New(t)
	err := store.Import(t.Context(), catalog.Snapshot{
		FormatVersion: catalog.SnapshotFormatVersion + 1,
		Backups:       []catalog.Backup{sampleBackup()},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), strconv.Itoa(catalog.SnapshotFormatVersion))

	got, err := store.ListBackups(t.Context(), catalog.BackupFilter{})
	require.NoError(t, err)
	assert.Empty(t, got, "a refused import must not have applied half of itself")
}

func sampleBackup() catalog.Backup {
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	return catalog.Backup{
		ID: "01SAMPLE00000000000000000A", SourceID: "prod", Kind: "logical",
		Destination: "main", Status: catalog.StatusCompleted,
		StartedAt: at, FinishedAt: at.Add(time.Minute), SizeBytes: 42,
		ManifestKey: "sources/prod/logical/01SAMPLE00000000000000000A/manifest.json",
	}
}

func sampleJob() catalog.Job {
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	return catalog.Job{
		ID: "01SAMPLEJOB0000000000000AA", SourceID: "prod", Kind: "logical",
		Trigger: catalog.TriggerManual, Status: catalog.StatusCompleted,
		StartedAt: at, FinishedAt: at.Add(time.Minute),
	}
}

// testForgetBackup is retention's only deletion, and the contract every backend
// has to honour identically: the row goes, nothing else moves.
func testForgetBackup(t *testing.T, h Harness) {
	s := h.New(t)
	keep, drop := sampleBackup(), sampleBackup()
	drop.ID = "01FORGET000000000000000000"

	require.NoError(t, s.RecordBackup(t.Context(), keep))
	require.NoError(t, s.RecordBackup(t.Context(), drop))
	require.NoError(t, s.ForgetBackup(t.Context(), drop.ID))

	got, err := s.ListBackups(t.Context(), catalog.BackupFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, keep.ID, got[0].ID)

	// Idempotent: a retention pass may be re-run after an interruption, and a
	// backup already forgotten is the state it was trying to reach.
	require.NoError(t, s.ForgetBackup(t.Context(), drop.ID))
	require.NoError(t, s.ForgetBackup(t.Context(), "01NEVEREXISTED000000000000"))
}
