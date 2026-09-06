//go:build milestone

// Package milestone_test holds the checks that close a milestone.
//
// They are behind a build tag and a `make verify-milestone` target rather than
// in the ordinary suite, because they move tens of gigabytes and take tens of
// minutes. A check that long, run in front of every push, is a check that gets
// skipped -- and then a milestone closes on a criterion nobody measured.
//
// What is here is exactly what the fast suite cannot cover: scale. Correctness
// is proved elsewhere, on small data, in seconds. These answer a different
// question -- does it still hold at the size a real database has.
package milestone_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// SizeEnv overrides the database size, for validating the harness itself.
//
// The criterion is 10 GiB. A smaller run exercises the same code and proves the
// same mechanics, but it does not close the criterion, and the report says so
// rather than letting a 1 GiB pass read like a 10 GiB one.
const SizeEnv = "KOFFR_MILESTONE_SIZE_GB"

const criterionSizeGB = 10

func targetSizeGB(t *testing.T) int {
	t.Helper()
	if v := os.Getenv(SizeEnv); v != "" {
		n, err := strconv.Atoi(v)
		require.NoError(t, err, "%s must be a whole number of gibibytes", SizeEnv)
		require.Positive(t, n)
		return n
	}
	return criterionSizeGB
}

// TestM1ExitCriteria measures criteria 1, 3 and 4 on one database.
//
// One database for all three deliberately: they are questions about the same
// job, and seeding ten gibibytes twice to ask them separately would put the
// whole target out of reach of anyone's afternoon.
func TestM1ExitCriteria(t *testing.T) {
	sizeGB := targetSizeGB(t)
	report := newReport(t,
		"fs: peak heap", "fs: rows restored", "fs: checksum",
		"s3: peak heap", "s3: rows restored", "s3: checksum",
		"temporary files", "database size")

	ctx := context.Background()

	// Order matters here. t.TempDir honours TMPDIR, so the repositories'
	// location is taken first; only then does TMPDIR move to a directory of its
	// own for criterion 4 to watch.
	//
	// Keeping them apart is the point. A destination's own object, mid-write,
	// is not a staged copy of itself -- and the first attempt at this watched
	// the system temporary directory, which on a laptop holds Steam cookies and
	// half of npm.
	dataRoot := t.TempDir()
	staging := stagingDir(t)

	pg := startPostgres(t, ctx)
	seedTo(t, ctx, pg, sizeGB)

	source := fingerprint(t, ctx, pg, sourceDB)
	t.Logf("source: %d rows, checksum %s, %s on disk",
		source.rows, source.checksum, humanBytes(source.bytes))

	// Criterion 4 watches both machines for the whole run. It is armed before
	// the first backup and read after the last restore: a temporary file that
	// appears and is deleted is still a temporary file, and PD-003 says there
	// are none.
	watch := watchForTemporaries(t, ctx, pg, staging)

	for _, dest := range []destination{fsDestination(t, dataRoot), s3Destination(t, ctx, dataRoot)} {
		t.Run(dest.name, func(t *testing.T) {
			// Not t.Fatal on the way out: a leg that fails must still let the
			// other one run, or one broken backend hides whatever the other
			// would have said.
			// Criterion 3 is measured around the backup only. A restore is
			// pg_restore's memory, not ours, and folding it in would report a
			// number that says nothing about ENF-001.
			peak := measurePeakHeap(t, func() {
				runKoffr(t, dest.config, "backup", sourceID)
			})
			report.add(dest.name+": peak heap", humanBytes(int64(peak)),
				peak < 512<<20, "ENF-001: under 512 MiB, independent of database size")

			backupID := latestBackup(t, dest.config)
			restored := "restored_" + dest.name
			runKoffr(t, dest.config, "restore", backupID,
				"--into", restored, "--create", "--with-globals", "--yes")

			got := fingerprint(t, ctx, pg, restored)

			// Dropped as soon as it has been read. Ten gibibytes restored twice
			// alongside the source is thirty, and the first run of this gate
			// died on "No space left on device" with nothing wrong with Koffr.
			dropDatabase(t, ctx, pg, restored)

			report.add(dest.name+": rows restored", strconv.Itoa(got.rows),
				got.rows == source.rows, fmt.Sprintf("criterion 1: %d expected", source.rows))
			report.add(dest.name+": checksum", got.checksum,
				got.checksum == source.checksum, "criterion 1: data identical to the source")
		})
	}

	found := watch.read(t)
	report.add("temporary files", found.describe(), found.clean(),
		"PD-003, criterion 4: nothing staged on either machine")

	report.add("database size", fmt.Sprintf("%d GiB", sizeGB), sizeGB >= criterionSizeGB,
		fmt.Sprintf("criterion 1 is measured at %d GiB", criterionSizeGB))

	report.print(t)
}
