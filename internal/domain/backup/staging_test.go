package backup_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/domain/backup"
)

// BKP-02 — the three modes of E-029 exist and are chosen per database. What a
// decision returns is never `auto`: `auto` is a question, and a job needs the
// answer.
func TestBKP02TheThreeModesAreSelectablePerDatabase(t *testing.T) {
	roomy := backup.StagingInputs{Destinations: 1, FreeBytes: 100 << 30, ExpectedBytes: 1 << 30}

	cases := []struct {
		configured backup.Mode
		want       backup.Mode
	}{
		{backup.Stage, backup.Stage},
		{backup.Stream, backup.Stream},
		{backup.Auto, backup.Stage}, // room enough, so the default of § 4.5
	}

	for _, c := range cases {
		t.Run(string(c.configured), func(t *testing.T) {
			inputs := roomy
			inputs.Configured = c.configured

			decided, err := backup.DecideStaging(inputs)
			if err != nil {
				t.Fatalf("DecideStaging: %v", err)
			}
			if decided.Mode != c.want {
				t.Errorf("mode = %q, want %q (%s)", decided.Mode, c.want, decided.Reason)
			}
			if decided.Mode == backup.Auto {
				t.Error("a decision returned auto, which is a question and not an answer")
			}
			if decided.Reason == "" {
				t.Error("the decision gives no reason, so nothing can be recorded in the manifest")
			}
		})
	}
}

// BKP-03 — `stage` is **imposed** in three cases of E-030, and imposed means
// over an explicit `stream`: the three are situations where streaming cannot
// work, not preferences.
func TestBKP03StageIsImposedByTheThreeCasesOfTheSpecification(t *testing.T) {
	roomy := backup.StagingInputs{
		Configured: backup.Stream, Destinations: 1,
		FreeBytes: 100 << 30, ExpectedBytes: 1 << 30,
	}

	cases := []struct {
		name  string
		apply func(*backup.StagingInputs)
		says  string
	}{
		{"the directory format", func(in *backup.StagingInputs) { in.DirectoryDump = true }, "directory"},
		{"more than one destination", func(in *backup.StagingInputs) { in.Destinations = 2 }, "destination"},
		{"structural verification", func(in *backup.StagingInputs) { in.StructuralVerifyWithoutEgress = true }, "verif"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inputs := roomy
			c.apply(&inputs)

			decided, err := backup.DecideStaging(inputs)
			if err != nil {
				t.Fatalf("DecideStaging: %v", err)
			}
			if decided.Mode != backup.Stage {
				t.Errorf("mode = %q, want stage — %s imposes it (%s)", decided.Mode, c.name, decided.Reason)
			}
			if !decided.Forced {
				t.Error("the decision does not say it was imposed, so nothing explains overriding the configuration")
			}
			if !strings.Contains(strings.ToLower(decided.Reason), c.says) {
				t.Errorf("the reason does not say why:\n%s", decided.Reason)
			}
		})
	}
}

// BKP-04 — `auto` arbitrates on the free space, with the margin of `N-4`, and
// the mode it applied is what gets recorded. Two runs of the same database can
// legitimately differ; a manifest that does not say which is unreadable.
func TestBKP04AutoChoosesOnTheFreeSpaceAndSaysWhatItChose(t *testing.T) {
	const expected = 10 << 30 // 10 GiB once compressed

	tight := backup.StagingInputs{
		Configured: backup.Auto, Destinations: 1,
		ExpectedBytes: expected, FreeBytes: 14 << 30, // below expected × 1.5
	}

	decided, err := backup.DecideStaging(tight)
	if err != nil {
		t.Fatalf("DecideStaging: %v", err)
	}
	if decided.Mode != backup.Stream {
		t.Errorf("mode = %q, want stream — there is not room for the margin (%s)", decided.Mode, decided.Reason)
	}
	if !strings.Contains(decided.Reason, "space") {
		t.Errorf("the reason does not name what decided:\n%s", decided.Reason)
	}

	roomy := tight
	roomy.FreeBytes = 15 << 30 // exactly expected × 1.5

	decided, err = backup.DecideStaging(roomy)
	if err != nil {
		t.Fatalf("DecideStaging: %v", err)
	}
	if decided.Mode != backup.Stage {
		t.Errorf("mode = %q, want stage — the margin of § 4.5 is met exactly (%s)", decided.Mode, decided.Reason)
	}
}

// BKP-05 — the expected size comes from the **last successful backup**, and
// from the size of the database only when there is none. Extrapolating from
// what actually happened beats any assumption about compression.
func TestBKP05TheExpectedSizeComesFromTheLastBackupFirst(t *testing.T) {
	const databaseBytes = 40 << 30

	fromHistory := backup.EstimateStored(&backup.PreviousBackup{StoredBytes: 6 << 30}, databaseBytes)
	if fromHistory != 6<<30 {
		t.Errorf("estimate = %d, want the 6 GiB the last backup actually took", fromHistory)
	}

	// With no history, § 4.5 gives a range of 10 % to 25 % of the raw size.
	// koffr takes the pessimistic end (`N-12`): under-estimating fills a disk.
	blind := backup.EstimateStored(nil, databaseBytes)
	if blind != databaseBytes/4 {
		t.Errorf("estimate = %d, want a quarter of %d — the pessimistic end of § 4.5", blind, databaseBytes)
	}

	// And an unknown database size is not an estimate of zero, which would make
	// every check pass.
	if backup.EstimateStored(nil, 0) != 0 {
		t.Error("an unknown size produced a non-zero estimate")
	}
}

// BKP-05 — a job that would fill the disk **falls back to `stream`** when it
// may, and is **refused up front** when `stage` is imposed. Filling the disk of
// a production machine is worse than not backing up and saying so (E-061).
func TestBKP05AJobThatWouldFillTheDiskFallsBackOrIsRefused(t *testing.T) {
	cramped := backup.StagingInputs{
		Configured: backup.Stage, Destinations: 1,
		ExpectedBytes: 10 << 30, FreeBytes: 1 << 30,
	}

	decided, err := backup.DecideStaging(cramped)
	if err != nil {
		t.Fatalf("DecideStaging: %v", err)
	}
	if decided.Mode != backup.Stream {
		t.Errorf("mode = %q, want stream — staging would fill the disk (%s)", decided.Mode, decided.Reason)
	}

	// But with two destinations, stage is imposed and there is nowhere to fall
	// back to: the job is refused before it starts.
	imposed := cramped
	imposed.Destinations = 2

	_, err = backup.DecideStaging(imposed)
	if err == nil {
		t.Fatal("a job that cannot stage and cannot stream was accepted")
	}
	if !errors.Is(err, backup.ErrNotEnoughSpace) {
		t.Errorf("got %v, want %v", err, backup.ErrNotEnoughSpace)
	}
	for _, want := range []string{"10", "1"} { // what was needed, and what there is
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not give the figures (%q):\n%v", want, err)
		}
	}
}

// BKP-05 — an unknown free space does not silently pass the check. koffr says
// it could not tell rather than staging into a disk it never measured.
func TestBKP05AnUnknownFreeSpaceDoesNotPassSilently(t *testing.T) {
	decided, err := backup.DecideStaging(backup.StagingInputs{
		Configured: backup.Auto, Destinations: 1,
		ExpectedBytes: 10 << 30, FreeBytes: 0,
	})
	if err != nil {
		t.Fatalf("DecideStaging: %v", err)
	}
	if decided.Mode != backup.Stream {
		t.Errorf("mode = %q, want stream — nothing says there is room (%s)", decided.Mode, decided.Reason)
	}
}
