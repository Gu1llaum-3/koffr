package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/cli"
)

// A battery against multi-destination retention, which is the newest and
// least-walked part of the purge. Every case here is one where deleting the
// wrong thing would be unrecoverable.

// count returns how many backups sit in one destination on disk.
func count(t *testing.T, cfgPath, destination string) int {
	t.Helper()
	root := filepath.Join(filepath.Dir(cfgPath), "repo")
	if destination != "main" {
		root = filepath.Join(filepath.Dir(cfgPath), destination)
	}
	entries, err := os.ReadDir(filepath.Join(root, "sources", "prod-pg-main", "logical"))
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return len(entries)
}

// withPerDestinationRetention writes a default policy plus one override.
func withPerDestinationRetention(t *testing.T, cfgPath, def, override string) {
	t.Helper()
	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	block := "    retention:\n" + def + "\n" +
		"    retention_by_destination:\n      offsite:\n" + override + "\n"
	require.NoError(t, os.WriteFile(cfgPath, []byte(strings.Replace(string(body),
		"    destinations: [main, offsite]", block+"    destinations: [main, offsite]", 1)), 0o600))
}

// seed writes n backups to both destinations, oldest last.
func seedBoth(t *testing.T, cfgPath string, ids ...string) {
	t.Helper()
	for i, id := range ids {
		putBackupIn(t, cfgPath, "main", id)
		putBackupIn(t, cfgPath, "offsite", id)
		at := time.Now().Add(-time.Duration(i) * 24 * time.Hour)
		recordBackupOnAt(t, cfgPath, id, "main", at)
		recordBackupOnAt(t, cfgPath, id, "offsite", at)
	}
}

// The headline case: local keeps a week, offsite keeps a year. Pruning must
// apply each policy to its own destination and touch nothing else.
func TestBattery_EachDestinationKeepsItsOwn(t *testing.T) {
	cfgPath := twoDestinations(t)
	withPerDestinationRetention(t, cfgPath, "      keep_last: 1", "        keep_last: 3")
	seedBoth(t, cfgPath,
		"01AAA00000000000000000000A", "01BBB00000000000000000000B",
		"01CCC00000000000000000000C", "01DDD00000000000000000000D")

	code, out, errOut := run(t, "--config", cfgPath, "prune", "--confirm")
	require.Equal(t, cli.ExitOK, code, "stderr: %s", errOut)
	assert.Contains(t, out, "main")
	assert.Contains(t, out, "offsite")

	assert.Equal(t, 1, count(t, cfgPath, "main"))
	assert.Equal(t, 3, count(t, cfgPath, "offsite"),
		"the offsite policy is what makes a second copy worth having")
}

// A policy on one destination must never reach into another. This is the
// mistake with no recovery: pruning local storage and taking the offsite copy
// with it.
func TestBattery_PruningOneDestinationLeavesTheOther(t *testing.T) {
	cfgPath := twoDestinations(t)
	// Only main has a policy; offsite has none, so it keeps everything.
	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, []byte(strings.Replace(string(body),
		"    destinations: [main, offsite]",
		"    retention_by_destination:\n      main:\n        keep_last: 1\n"+
			"    destinations: [main, offsite]", 1)), 0o600))

	seedBoth(t, cfgPath, "01AAA00000000000000000000A", "01BBB00000000000000000000B")

	code, _, errOut := run(t, "--config", cfgPath, "prune", "--confirm")
	require.Equal(t, cli.ExitOK, code, "stderr: %s", errOut)

	assert.Equal(t, 1, count(t, cfgPath, "main"))
	assert.Equal(t, 2, count(t, cfgPath, "offsite"),
		"offsite has no policy, so nothing there may be deleted")
}

// EF-065 is per destination: each one keeps something restorable of its own.
// A floor that looked across destinations would let a policy empty local
// storage entirely because the offsite copy exists -- and then a network
// outage means no backups at all.
func TestBattery_TheFloorIsPerDestination(t *testing.T) {
	cfgPath := twoDestinations(t)
	withPerDestinationRetention(t, cfgPath, "      keep_within: 1m", "        keep_within: 1m")

	// Both older than the window, so the policy would take everything and only
	// the floor can save anything. seedBoth's newest is "now", which the
	// window keeps -- and a test where the floor never fires proves nothing
	// about the floor.
	for i, id := range []string{"01AAA00000000000000000000A", "01BBB00000000000000000000B"} {
		putBackupIn(t, cfgPath, "main", id)
		putBackupIn(t, cfgPath, "offsite", id)
		at := time.Now().Add(-time.Duration(i+2) * 24 * time.Hour)
		recordBackupOnAt(t, cfgPath, id, "main", at)
		recordBackupOnAt(t, cfgPath, id, "offsite", at)
	}

	code, out, errOut := run(t, "--config", cfgPath, "prune", "--confirm")
	require.Equal(t, cli.ExitOK, code, "stderr: %s", errOut)
	assert.Contains(t, out, "only restorable backup")

	assert.Equal(t, 1, count(t, cfgPath, "main"),
		"a policy that expires everything must still leave local storage restorable")
	assert.Equal(t, 1, count(t, cfgPath, "offsite"))
}

// A backup the catalog places on main, whose objects are only offsite. The
// restorability check has to consult the destination being pruned, not any
// other one, or the floor is spent on something that is not there.
func TestBattery_RestorabilityIsCheckedOnTheRightDestination(t *testing.T) {
	cfgPath := twoDestinations(t)
	withPerDestinationRetention(t, cfgPath, "      keep_within: 1m", "        keep_last: 5")

	// Newest: recorded on main, present only offsite.
	putBackupIn(t, cfgPath, "offsite", "01NEWEST00000000000000000A")
	recordBackupOnAt(t, cfgPath, "01NEWEST00000000000000000A", "main", time.Now())
	recordBackupOnAt(t, cfgPath, "01NEWEST00000000000000000A", "offsite", time.Now())

	// Older: really on main.
	putBackupIn(t, cfgPath, "main", "01OLDER000000000000000000B")
	recordBackupOnAt(t, cfgPath, "01OLDER000000000000000000B", "main", time.Now().Add(-48*time.Hour))

	code, _, errOut := run(t, "--config", cfgPath, "prune", "--confirm")
	require.Equal(t, cli.ExitOK, code, "stderr: %s", errOut)

	assert.Equal(t, 1, count(t, cfgPath, "main"),
		"the floor must fall through to the copy that is really on main")

	root := filepath.Join(filepath.Dir(cfgPath), "repo", "sources", "prod-pg-main", "logical")
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Equal(t, "01OLDER000000000000000000B", entries[0].Name())
}

// Orphans are per destination too: litter on offsite must be swept without
// touching main, and a complete backup on either is never litter.
func TestBattery_OrphansAcrossDestinations(t *testing.T) {
	cfgPath := twoDestinations(t)
	seedBoth(t, cfgPath, "01GOOD0000000000000000000A")

	litter := filepath.Join(filepath.Dir(cfgPath), "offsite",
		"sources", "prod-pg-main", "logical", "01LITTER00000000000000000B")
	require.NoError(t, os.MkdirAll(litter, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(litter, "dump.pgdump.zst.age"),
		[]byte("half an upload"), 0o600))
	old := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(litter, "dump.pgdump.zst.age"), old, old))

	code, out, errOut := run(t, "--config", cfgPath, "prune", "--orphans", "--confirm")
	require.Equal(t, cli.ExitOK, code, "stderr: %s", errOut)
	assert.Contains(t, out, "01LITTER")

	_, err := os.Stat(litter)
	assert.True(t, os.IsNotExist(err))
	assert.Equal(t, 1, count(t, cfgPath, "main"), "the good backup is untouched")
	assert.Equal(t, 1, count(t, cfgPath, "offsite"))
}

// A dry run across two destinations must touch neither, and must say what each
// would lose.
func TestBattery_DryRunAcrossDestinations(t *testing.T) {
	cfgPath := twoDestinations(t)
	withPerDestinationRetention(t, cfgPath, "      keep_last: 1", "        keep_last: 2")
	seedBoth(t, cfgPath,
		"01AAA00000000000000000000A", "01BBB00000000000000000000B", "01CCC00000000000000000000C")

	code, out, _ := run(t, "--config", cfgPath, "prune")
	require.Equal(t, cli.ExitOK, code)
	assert.Contains(t, out, "Nothing was")

	assert.Equal(t, 3, count(t, cfgPath, "main"))
	assert.Equal(t, 3, count(t, cfgPath, "offsite"))

	// 2 from main plus 1 from offsite. Counted as verdicts in the table rather
	// than as occurrences of the word: the summary line says "3 would be
	// deleted" and would inflate a naive count.
	var verdicts int
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) > 4 && slices.Contains(fields, "delete") {
			verdicts++
		}
	}
	assert.Equal(t, 3, verdicts)
}

// Running twice must converge on both destinations: a policy that deleted a
// little more each pass would empty a repository one night at a time.
func TestBattery_ConvergesOnEveryDestination(t *testing.T) {
	cfgPath := twoDestinations(t)
	withPerDestinationRetention(t, cfgPath, "      keep_last: 2", "        keep_last: 3")
	seedBoth(t, cfgPath,
		"01AAA00000000000000000000A", "01BBB00000000000000000000B",
		"01CCC00000000000000000000C", "01DDD00000000000000000000D")

	for pass := range 3 {
		code, _, errOut := run(t, "--config", cfgPath, "prune", "--confirm")
		require.Equal(t, cli.ExitOK, code, "pass %d, stderr: %s", pass, errOut)
		assert.Equal(t, 2, count(t, cfgPath, "main"), "pass %d", pass)
		assert.Equal(t, 3, count(t, cfgPath, "offsite"), "pass %d", pass)
	}
}

// A backup surviving on offsite has to stay restorable after main is pruned,
// which is the whole reason for keeping a second copy longer.
func TestBattery_ASurvivorOnOffsiteStaysReachable(t *testing.T) {
	cfgPath := twoDestinations(t)
	withPerDestinationRetention(t, cfgPath, "      keep_last: 1", "        keep_last: 3")
	seedBoth(t, cfgPath,
		"01AAA00000000000000000000A", "01BBB00000000000000000000B", "01CCC00000000000000000000C")

	code, _, errOut := run(t, "--config", cfgPath, "prune", "--confirm")
	require.Equal(t, cli.ExitOK, code, "stderr: %s", errOut)

	// Gone from main, still offsite, and reachable with no flag at all.
	code, out, errOut := run(t, "--config", cfgPath, "show", "01CCC00000000000000000000C")
	require.Equal(t, cli.ExitOK, code, "stderr: %s", errOut)
	assert.Contains(t, out, "01CCC00000000000000000000C")
}

func recordBackupOnAt(t *testing.T, cfgPath, id, destination string, at time.Time) {
	t.Helper()
	recordBackupFull(t, cfgPath, id, destination, at)
}

// Losing the catalog with two destinations made it unrebuildable: sync refused
// unless one was named, and no single destination holds the whole history once
// the two policies differ. That is the configuration a rebuild most needs to
// work for.
func TestBattery_CatalogSyncRebuildsFromEveryDestination(t *testing.T) {
	cfgPath := twoDestinations(t)

	// One backup on both, one only offsite, as retention leaves them.
	putBackupIn(t, cfgPath, "main", "01BOTH0000000000000000000A")
	putBackupIn(t, cfgPath, "offsite", "01BOTH0000000000000000000A")
	putBackupIn(t, cfgPath, "offsite", "01OFFSITE0000000000000000B")

	code, out, errOut := run(t, "--config", cfgPath, "catalog", "sync", "--from-manifests")
	require.Equal(t, cli.ExitOK, code, "stderr: %s", errOut)
	assert.Contains(t, out, "main")
	assert.Contains(t, out, "offsite")

	code, out, _ = run(t, "--config", cfgPath, "--output", "json", "ls")
	require.Equal(t, cli.ExitOK, code)

	var got struct {
		Result struct {
			Backups []struct {
				ID           string   `json:"backup_id"`
				Destinations []string `json:"destinations"`
			} `json:"backups"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got.Result.Backups, 2)

	where := map[string][]string{}
	for _, b := range got.Result.Backups {
		where[b.ID] = b.Destinations
	}
	assert.ElementsMatch(t, []string{"main", "offsite"}, where["01BOTH0000000000000000000A"],
		"reading both destinations is how the catalog learns a backup is on both")
	assert.Equal(t, []string{"offsite"}, where["01OFFSITE0000000000000000B"])
}

// One destination unreachable must not stop the rebuild from the others: a
// half-rebuilt catalog beats none, and the gap is reported rather than hidden.
func TestBattery_CatalogSyncSurvivesAnUnreachableDestination(t *testing.T) {
	cfgPath := twoDestinations(t)
	putBackupIn(t, cfgPath, "main", "01ONMAIN00000000000000000A")

	// Point offsite at a path that cannot be created.
	body, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, []byte(strings.Replace(string(body),
		"    path: "+filepath.Join(filepath.Dir(cfgPath), "offsite"),
		"    path: /proc/koffr-cannot-exist", 1)), 0o600))

	code, out, errOut := run(t, "--config", cfgPath, "catalog", "sync", "--from-manifests")
	require.Equal(t, cli.ExitOK, code, "stderr: %s", errOut)
	assert.Contains(t, out, "main")
	assert.Contains(t, errOut, "offsite", "the destination that could not be read has to be named")

	code, out, _ = run(t, "--config", cfgPath, "ls")
	require.Equal(t, cli.ExitOK, code)
	assert.Contains(t, out, "01ONMAIN00000000000000000A")
}

// The safety property that matters most: a destination Koffr cannot read must
// not lose anything. Confirmed against a stopped MinIO -- a policy that would
// have deleted three deleted none, and every line said why.
//
// Getting this backwards would delete backups because a network was down.
func TestBattery_AnUnreachableDestinationDeletesNothing(t *testing.T) {
	cfgPath := twoDestinations(t)
	withPerDestinationRetention(t, cfgPath, "      keep_last: 3", "        keep_last: 1")
	seedBoth(t, cfgPath,
		"01AAA00000000000000000000A", "01BBB00000000000000000000B", "01CCC00000000000000000000C")

	// offsite becomes unreadable while its policy would delete two of three.
	offsite := filepath.Join(filepath.Dir(cfgPath), "offsite")
	require.NoError(t, os.Chmod(offsite, 0o000))
	t.Cleanup(func() { _ = os.Chmod(offsite, 0o700) })

	code, out, _ := run(t, "--config", cfgPath, "prune", "--confirm")
	require.Equal(t, cli.ExitOK, code)
	assert.Contains(t, out, "cannot confirm",
		"a destination that cannot be read has to say so, not delete quietly")

	require.NoError(t, os.Chmod(offsite, 0o700))
	assert.Equal(t, 3, count(t, cfgPath, "offsite"),
		"a policy keeping one deleted none, because none could be confirmed present")

	// And the destination that was readable was pruned normally.
	assert.Equal(t, 3, count(t, cfgPath, "main"))
}
