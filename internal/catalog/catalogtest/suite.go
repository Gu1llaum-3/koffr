// Package catalogtest is the contract every catalog.MetadataStore must satisfy.
//
// The catalog is a cache, not the truth (DEC-004), and that shapes what is
// worth testing. Losing it must cost time, never data, so nothing here assumes
// it is authoritative. What it must get right is the reasoning built on top of
// it: which backups depend on which, and how old the last good one is. Both
// decide whether data is deleted.
package catalogtest

import (
	"fmt"
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
