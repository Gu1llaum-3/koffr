package retention_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/retention"
)

// daily builds one backup a day at 02:00 for the n days before now.
func daily(n int, from time.Time) []catalog.Backup {
	out := make([]catalog.Backup, 0, n)
	for i := range n {
		at := from.AddDate(0, 0, -i)
		at = time.Date(at.Year(), at.Month(), at.Day(), 2, 0, 0, 0, time.UTC)
		out = append(out, catalog.Backup{
			ID:       catalog.ID(fmt.Sprintf("%04d-%02d-%02d", at.Year(), at.Month(), at.Day())),
			SourceID: "prod", Kind: "logical", Status: catalog.StatusCompleted, StartedAt: at,
		})
	}
	return out
}

// A year of nightly backups against the policy most people would write. This is
// the shape a real repository has after a year, and the one where an off-by-one
// in a GFS bucket costs a month of history nobody notices until they need it.
func TestPlan_AYearOfNightlyBackups(t *testing.T) {
	backups := daily(365, now)

	plan, err := retention.Plan(backups, retention.Policy{
		Daily: 7, Weekly: 4, Monthly: 12,
	}, now)
	require.NoError(t, err)

	var keptIDs []string
	for _, d := range plan {
		if d.Keep {
			keptIDs = append(keptIDs, string(d.Backup.ID))
		}
	}

	// Seven days, four weeks, twelve months. The first week overlaps the daily
	// window and the first month overlaps both, so the total is fewer than 23
	// -- and that overlap is the union working as intended.
	assert.Less(t, len(keptIDs), 23)
	assert.Greater(t, len(keptIDs), 14, "twelve months of history has to survive")

	// The last seven days, every one of them.
	for i := range 7 {
		at := now.AddDate(0, 0, -i)
		assert.Contains(t, keptIDs, fmt.Sprintf("%04d-%02d-%02d", at.Year(), at.Month(), at.Day()),
			"day -%d is inside a seven-day window", i)
	}

	// One per month for twelve months, and nothing older.
	months := map[string]int{}
	for _, id := range keptIDs {
		months[id[:7]]++
	}
	assert.LessOrEqual(t, len(months), 13, "twelve monthly slots plus the current one")

	oldest := keptIDs[len(keptIDs)-1]
	assert.GreaterOrEqual(t, oldest, now.AddDate(-1, -1, 0).Format("2006-01-02"),
		"nothing older than the monthly window should survive")
}

// Backups that stop and start again -- a source that was down for a fortnight --
// must not let a gap eat the history on either side of it.
func TestPlan_AGapInTheTimeline(t *testing.T) {
	var backups []catalog.Backup
	backups = append(backups, daily(5, now)...)
	backups = append(backups, daily(5, now.AddDate(0, 0, -20))...)

	plan, err := retention.Plan(backups, retention.Policy{Daily: 7}, now)
	require.NoError(t, err)

	var kept int
	for _, d := range plan {
		if d.Keep {
			kept++
		}
	}
	// Seven daily slots, and only ten backups across two clusters: the rule
	// counts periods that have a backup, not calendar days.
	assert.Equal(t, 7, kept,
		"a fortnight with no backups must not spend the daily slots on days that have none")
}

// Several backups the same night -- a scheduled one and a manual one -- must
// count as one day, not two.
func TestPlan_SeveralBackupsInOnePeriod(t *testing.T) {
	var backups []catalog.Backup
	for day := range 3 {
		for hour := range 4 {
			at := now.AddDate(0, 0, -day)
			at = time.Date(at.Year(), at.Month(), at.Day(), 2+hour*5, 0, 0, 0, time.UTC)
			backups = append(backups, catalog.Backup{
				ID:       catalog.ID(fmt.Sprintf("d%d-h%d", day, hour)),
				SourceID: "prod", Kind: "logical", Status: catalog.StatusCompleted, StartedAt: at,
			})
		}
	}

	plan, err := retention.Plan(backups, retention.Policy{Daily: 3}, now)
	require.NoError(t, err)
	assert.Len(t, kept(t, plan), 3, "three days, one survivor each")

	// The newest of each day, not the oldest: a later backup is closer to the
	// state you would restore to.
	assert.ElementsMatch(t, []string{"d0-h3", "d1-h3", "d2-h3"}, kept(t, plan))
}

// Running the same plan twice must produce the same answer. A policy whose
// verdict drifts between two dry runs is one nobody can approve.
func TestPlan_IsDeterministic(t *testing.T) {
	backups := daily(90, now)
	policy := retention.Policy{KeepLast: 3, Daily: 7, Weekly: 4, Monthly: 3}

	first, err := retention.Plan(backups, policy, now)
	require.NoError(t, err)
	second, err := retention.Plan(backups, policy, now)
	require.NoError(t, err)

	require.Equal(t, len(first), len(second))
	for i := range first {
		assert.Equal(t, first[i].Backup.ID, second[i].Backup.ID)
		assert.Equal(t, first[i].Keep, second[i].Keep)
		assert.Equal(t, first[i].Reason, second[i].Reason)
	}
}

// Applying a plan and re-planning must converge: what a purge kept, the same
// policy must keep again. A policy that deleted a little more each pass would
// empty a repository one night at a time.
func TestPlan_ConvergesAfterAPass(t *testing.T) {
	backups := daily(60, now)
	policy := retention.Policy{Daily: 7, Weekly: 4, Monthly: 2}

	first, err := retention.Plan(backups, policy, now)
	require.NoError(t, err)

	var survivors []catalog.Backup
	for _, d := range first {
		if d.Keep {
			survivors = append(survivors, d.Backup)
		}
	}

	second, err := retention.Plan(survivors, policy, now)
	require.NoError(t, err)
	assert.Empty(t, deleted(t, second),
		"a second pass at the same moment must delete nothing it just decided to keep")
}

// The buckets are UTC, and a backup at 01:00 Paris time is the previous day in
// UTC. That is a choice rather than an accident: a policy whose meaning shifts
// twice a year would keep eight days one week and six the next.
func TestPlan_BucketsAreUTC(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	require.NoError(t, err)

	// 2026-03-02 00:30 Paris is 2026-03-01 23:30 UTC: different days.
	late := time.Date(2026, 3, 2, 0, 30, 0, 0, paris)
	early := time.Date(2026, 3, 2, 9, 0, 0, 0, paris)

	backups := []catalog.Backup{
		{ID: "late", SourceID: "prod", Kind: "logical", Status: catalog.StatusCompleted, StartedAt: late},
		{ID: "early", SourceID: "prod", Kind: "logical", Status: catalog.StatusCompleted, StartedAt: early},
	}

	plan, err := retention.Plan(backups, retention.Policy{Daily: 1},
		time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	// One daily slot, and they are in different UTC days, so only the newest
	// day is kept -- and the other is deleted rather than sharing the slot.
	assert.Equal(t, []string{"early"}, kept(t, plan))
}

// Ten thousand backups is a source backed up every ten minutes for two months.
// The plan is computed in memory, so this is about not being accidentally
// quadratic.
func TestPlan_ScalesToALargeRepository(t *testing.T) {
	backups := make([]catalog.Backup, 0, 10000)
	for i := range 10000 {
		backups = append(backups, catalog.Backup{
			ID:       catalog.ID(fmt.Sprintf("%06d", i)),
			SourceID: "prod", Kind: "logical", Status: catalog.StatusCompleted,
			StartedAt: now.Add(-time.Duration(i) * 10 * time.Minute),
		})
	}

	start := time.Now()
	plan, err := retention.Plan(backups, retention.Policy{
		Hourly: 24, Daily: 30, Weekly: 8, Monthly: 12,
	}, now)
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Len(t, plan, 10000)
	assert.NotEmpty(t, kept(t, plan))
}
