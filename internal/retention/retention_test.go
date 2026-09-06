package retention_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/retention"
)

var now = time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

// at builds a completed logical backup taken that long ago.
func at(id string, ago time.Duration) catalog.Backup {
	return catalog.Backup{
		ID: catalog.ID(id), SourceID: "prod", Kind: "logical",
		Status: catalog.StatusCompleted, StartedAt: now.Add(-ago),
	}
}

func kept(t *testing.T, decisions []retention.Decision) []string {
	t.Helper()
	var out []string
	for _, d := range decisions {
		if d.Keep {
			out = append(out, string(d.Backup.ID))
		}
	}
	return out
}

func deleted(t *testing.T, decisions []retention.Decision) []string {
	t.Helper()
	var out []string
	for _, d := range decisions {
		if !d.Keep {
			out = append(out, string(d.Backup.ID))
		}
	}
	return out
}

// A policy nobody wrote must not delete anything. The zero value of a thing
// that deletes backups has to be "delete nothing".
func TestPlan_AnEmptyPolicyKeepsEverything(t *testing.T) {
	backups := []catalog.Backup{at("c", 0), at("b", 48*time.Hour), at("a", 400*24*time.Hour)}

	plan, err := retention.Plan(backups, retention.Policy{}, now)
	require.NoError(t, err)
	assert.Empty(t, deleted(t, plan))
	for _, d := range plan {
		assert.Contains(t, d.Reason, "no retention policy")
	}
}

func TestPlan_KeepLast(t *testing.T) {
	backups := []catalog.Backup{
		at("e", 0), at("d", 24*time.Hour), at("c", 48*time.Hour),
		at("b", 72*time.Hour), at("a", 96*time.Hour),
	}

	plan, err := retention.Plan(backups, retention.Policy{KeepLast: 3}, now)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"e", "d", "c"}, kept(t, plan))
	assert.ElementsMatch(t, []string{"b", "a"}, deleted(t, plan))

	for _, d := range plan {
		if d.Keep {
			assert.Contains(t, d.Reason, "last 3",
				"EF-064 wants the reason, not just the verdict")
		}
	}
}

func TestPlan_KeepWithin(t *testing.T) {
	backups := []catalog.Backup{
		at("c", time.Hour), at("b", 6*24*time.Hour), at("a", 8*24*time.Hour),
	}

	plan, err := retention.Plan(backups, retention.Policy{KeepWithin: 7 * 24 * time.Hour}, now)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"c", "b"}, kept(t, plan))
	assert.ElementsMatch(t, []string{"a"}, deleted(t, plan))
}

// GFS keeps the newest backup of each period, which is what makes "seven daily"
// mean seven days rather than seven backups.
func TestPlan_GFSDaily(t *testing.T) {
	var backups []catalog.Backup
	// Three a day for five days: 15 backups, five days.
	for day := range 5 {
		for n := range 3 {
			id := string(rune('a'+day)) + string(rune('0'+n))
			backups = append(backups, at(id, time.Duration(day)*24*time.Hour+time.Duration(n)*time.Hour))
		}
	}

	plan, err := retention.Plan(backups, retention.Policy{Daily: 3}, now)
	require.NoError(t, err)

	// The newest of each of the three most recent days.
	assert.ElementsMatch(t, []string{"a0", "b0", "c0"}, kept(t, plan))
	assert.Len(t, deleted(t, plan), 12)
}

func TestPlan_GFSAcrossPeriods(t *testing.T) {
	backups := []catalog.Backup{
		at("today", time.Hour),
		at("yesterday", 25*time.Hour),
		at("lastweek", 8*24*time.Hour),
		at("lastmonth", 40*24*time.Hour),
		at("lastyear", 400*24*time.Hour),
		at("ancient", 800*24*time.Hour),
	}

	plan, err := retention.Plan(backups,
		retention.Policy{Daily: 2, Weekly: 2, Monthly: 2, Yearly: 2}, now)
	require.NoError(t, err)

	assert.NotContains(t, kept(t, plan), "ancient",
		"two years kept means the third goes")
	assert.Contains(t, kept(t, plan), "today")
	assert.Contains(t, kept(t, plan), "lastyear")
}

// Rules are a union, not an intersection. A backup any rule wants is kept:
// the alternative deletes something a policy explicitly asked to keep, which
// is the failure mode that matters here.
func TestPlan_RulesAreAUnion(t *testing.T) {
	backups := []catalog.Backup{
		at("c", time.Hour), at("b", 30*24*time.Hour), at("a", 200*24*time.Hour),
	}

	// keep_last would drop b and a; monthly wants b; yearly wants a.
	plan, err := retention.Plan(backups,
		retention.Policy{KeepLast: 1, Monthly: 2, Yearly: 2}, now)
	require.NoError(t, err)
	assert.Empty(t, deleted(t, plan))
}

// EF-065, and the rule that outranks every policy. A source with one backup has
// one chance of being restored, and no configuration should be able to spend
// it.
func TestPlan_NeverDeletesTheLastBackup(t *testing.T) {
	// Only a policy that actually expires things can express "delete the last
	// one" -- a GFS rule always keeps the newest of its most recent period, and
	// keep_last: 0 with nothing else is the empty policy, which keeps
	// everything by a different route. Expiry is the one road to this cliff.
	plan, err := retention.Plan(
		[]catalog.Backup{at("only", 400*24*time.Hour)},
		retention.Policy{KeepWithin: time.Minute}, now)
	require.NoError(t, err)

	assert.Empty(t, deleted(t, plan))
	require.Len(t, plan, 1)
	assert.Contains(t, plan[0].Reason, "only",
		"the reason has to say the rule that saved it, or an operator cannot tell "+
			"a deliberate keep from a policy that did nothing")
}

// Even with a policy that keeps some, the newest survives whatever the rules
// say about it.
func TestPlan_TheNewestIsAlwaysKept(t *testing.T) {
	backups := []catalog.Backup{at("newest", 400*24*time.Hour), at("older", 500*24*time.Hour)}

	plan, err := retention.Plan(backups, retention.Policy{KeepWithin: time.Hour}, now)
	require.NoError(t, err)
	assert.Equal(t, []string{"newest"}, kept(t, plan))
	assert.Equal(t, []string{"older"}, deleted(t, plan))
}

// The guard that makes this safe to ship before M2 exists.
//
// A physical backup can have incrementals depending on it (EF-062) and WAL
// segments whose replay starts from it (EF-063). Neither concept exists yet, so
// this cannot reason about them -- and reasoning wrongly about what to delete
// is the one mistake with no recovery. It stops rather than guesses.
func TestPlan_RefusesAKindItCannotReasonAbout(t *testing.T) {
	physical := at("phys", time.Hour)
	physical.Kind = "physical"

	_, err := retention.Plan([]catalog.Backup{at("log", 0), physical}, retention.Policy{KeepLast: 1}, now)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "physical")
	assert.Contains(t, err.Error(), "phys", "the operator has to know which backup stopped it")
}

// A backup that never completed has no objects to delete and no value to keep.
// Counting one towards keep_last would let three failures push a good backup
// out of the window.
func TestPlan_IgnoresBackupsThatNeverCompleted(t *testing.T) {
	failed := at("failed", time.Hour)
	failed.Status = catalog.StatusFailed

	backups := []catalog.Backup{failed, at("good", 2*time.Hour), at("older", 3*time.Hour)}

	plan, err := retention.Plan(backups, retention.Policy{KeepLast: 1}, now)
	require.NoError(t, err)
	assert.Equal(t, []string{"good"}, kept(t, plan))
	assert.Equal(t, []string{"older"}, deleted(t, plan))
}

func TestPlan_NoBackups(t *testing.T) {
	plan, err := retention.Plan(nil, retention.Policy{KeepLast: 3}, now)
	require.NoError(t, err)
	assert.Empty(t, plan)
}

// A policy is refused rather than silently normalised: a negative count is a
// typo, and guessing what it meant is how a purge does something nobody asked
// for.
func TestPolicy_Validate(t *testing.T) {
	assert.NoError(t, retention.Policy{KeepLast: 3, Daily: 7}.Validate())
	assert.NoError(t, retention.Policy{}.Validate())
	assert.Error(t, retention.Policy{KeepLast: -1}.Validate())
	assert.Error(t, retention.Policy{Daily: -1}.Validate())
	assert.Error(t, retention.Policy{KeepWithin: -time.Hour}.Validate())
}
