package retention_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/binlog"
	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/retention"
)

func names(t *testing.T, ss ...string) []binlog.Name {
	t.Helper()
	out := make([]binlog.Name, 0, len(ss))
	for _, s := range ss {
		n, err := binlog.Parse(s)
		require.NoError(t, err)
		out = append(out, n)
	}
	return out
}

func strs(ns []binlog.Name) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.String())
	}
	return out
}

// The floor is the anchor of the oldest kept backup. Everything before it is
// history nobody can replay from; everything at or after it is the road from a
// backup to now.
func TestPlanBinlog_KeepsFromTheOldestAnchorOnwards(t *testing.T) {
	plan, err := retention.PlanBinlog(
		names(t, "b.000040", "b.000041", "b.000042", "b.000043", "b.000044"),
		[]catalog.Backup{
			{ID: "new", BinlogFile: "b.000044"},
			{ID: "old", BinlogFile: "b.000042"},
		})
	require.NoError(t, err)
	assert.Equal(t, []string{"b.000040", "b.000041"}, strs(plan.Delete))
	assert.Equal(t, []string{"b.000042", "b.000043", "b.000044"}, strs(plan.Keep))
	require.NotNil(t, plan.Floor)
	assert.Equal(t, "b.000042", plan.Floor.String())
}

// EF-063's whole point. A kept backup without an anchor means the floor is
// unknown, and an unknown floor deletes nothing -- guessing here is how a
// point-in-time recovery discovers a hole at three in the morning.
func TestPlanBinlog_AKeptBackupWithoutAnAnchorFreezesTheArchive(t *testing.T) {
	plan, err := retention.PlanBinlog(
		names(t, "b.000040", "b.000041", "b.000042"),
		[]catalog.Backup{
			{ID: "anchored", BinlogFile: "b.000042"},
			{ID: "blind"},
		})
	require.NoError(t, err)
	assert.Empty(t, plan.Delete)
	assert.Len(t, plan.Keep, 3)
	assert.Contains(t, plan.Reason, "blind")
	assert.Contains(t, plan.Reason, "nothing is deleted")
}

// The archive outlives any one backup: with none kept, the files written since
// are still the only copy of what the next backup will need.
func TestPlanBinlog_NoKeptBackupKeepsEverything(t *testing.T) {
	plan, err := retention.PlanBinlog(names(t, "b.000040", "b.000041"), nil)
	require.NoError(t, err)
	assert.Empty(t, plan.Delete)
	assert.Len(t, plan.Keep, 2)
	assert.NotEmpty(t, plan.Reason)
}

func TestPlanBinlog_AnAnchorFromAnotherLogBaseFreezesTheArchive(t *testing.T) {
	// log_bin was renamed between the backup and now. The archive on disk is
	// one sequence, the anchor is in another; comparing their numbers would be
	// comparing nothing.
	plan, err := retention.PlanBinlog(
		names(t, "new.000001", "new.000002"),
		[]catalog.Backup{{ID: "x", BinlogFile: "old.000099"}})
	require.NoError(t, err)
	assert.Empty(t, plan.Delete)
	assert.Contains(t, plan.Reason, "not from this archive")
}

func TestPlanBinlog_EmptyArchive(t *testing.T) {
	plan, err := retention.PlanBinlog(nil, []catalog.Backup{{ID: "x", BinlogFile: "b.000001"}})
	require.NoError(t, err)
	assert.Empty(t, plan.Delete)
	assert.Empty(t, plan.Keep)
}

func TestKeptBackups(t *testing.T) {
	kept := retention.KeptBackups([]retention.Decision{
		{Backup: catalog.Backup{ID: "a"}, Keep: true},
		{Backup: catalog.Backup{ID: "b"}, Keep: false},
		{Backup: catalog.Backup{ID: "c"}, Keep: true},
	})
	require.Len(t, kept, 2)
	assert.Equal(t, catalog.ID("a"), kept[0].ID)
	assert.Equal(t, catalog.ID("c"), kept[1].ID)
}
