package backup

import (
	"errors"
	"fmt"
)

// Mode is a staging policy of § 4.5. It is declared per database (E-029).
type Mode string

// The three modes of E-029.
const (
	// Stage writes the compressed and encrypted stream to a staging file, then
	// sends from that file. The transaction on the database closes as soon as
	// the dump ends, whatever a destination's bandwidth is doing.
	Stage Mode = "stage"

	// Stream sends straight to the destination: no stop, no resume, and
	// verification costs a download.
	Stream Mode = "stream"

	// Auto arbitrates per run. It is a question, never an answer: a decision
	// returns Stage or Stream.
	Auto Mode = "auto"
)

// ErrNotEnoughSpace is E-061: staging is imposed and there is not room for it,
// so the job is refused **before** it starts. Filling the disk of a production
// machine is worse than not backing up and saying so.
var ErrNotEnoughSpace = errors.New("not enough free space to stage this backup")

// marginNumerator and marginDenominator are the × 1.5 of § 4.5, kept as a
// fraction so that no float ever decides whether a disk has room (ADR-0006).
const (
	marginNumerator   = 3
	marginDenominator = 2
)

// blindCompressionDivisor turns a raw database size into an expected stored
// size when there is no history: an eighth, 12,5 %.
//
// § 4.5 gives a range of 10 % to 25 %, and `N-12` first took the pessimistic
// end. The acceptance run of the 2026-09-22 measured **7,8 %** on a 372 MB
// PostgreSQL and **3,9 %** on a MariaDB: koffr was reserving 139 MB for an
// archive of 23 MB. ADR-0016 amended it — over-reserving is not free, it falls
// back to `stream`, and the same session measured what `stream` costs: the
// transaction stays open on production for the whole send.
const blindCompressionDivisor = 8

// StagingInputs is everything the arbitration of § 4.5 looks at. It holds no
// file, no connection and no clock: the decision is pure, so it can be shown to
// an operator by `doctor` without running a backup.
type StagingInputs struct {
	// Configured is what the database declares. Empty means Auto.
	Configured Mode

	// DirectoryDump says the dump will be -Fd, which is not a stream at all.
	DirectoryDump bool

	// Destinations is how many places this archive goes to. More than one and
	// the slowest would otherwise set the pace for the dump itself.
	Destinations int

	// StructuralVerifyWithoutEgress says the structure has to be read back and
	// that re-downloading the archive to do it is not acceptable.
	StructuralVerifyWithoutEgress bool

	// FreeBytes is what the staging directory has left; ExpectedBytes what the
	// archive is expected to take once compressed and encrypted.
	FreeBytes     int64
	ExpectedBytes int64
}

// StagingDecision is the answer, and why. The reason is not decoration: E-053
// records the mode actually applied in the manifest, and `doctor` shows it, so
// two runs of the same database that differ can be explained.
type StagingDecision struct {
	Mode   Mode
	Reason string

	// Forced says the mode was imposed by E-030 over what the database asked
	// for.
	Forced bool
}

// DecideStaging applies § 4.5 and E-030.
func DecideStaging(inputs StagingInputs) (StagingDecision, error) {
	room := inputs.FreeBytes >= needed(inputs.ExpectedBytes)

	if imposed, why := stageImposed(inputs); imposed {
		if !room {
			return StagingDecision{}, fmt.Errorf(
				"%w: %s imposes staging, which needs %d bytes with the margin of the specification, and %d are free",
				ErrNotEnoughSpace, why, needed(inputs.ExpectedBytes), inputs.FreeBytes)
		}

		return StagingDecision{Mode: Stage, Reason: why + " imposes staging", Forced: true}, nil
	}

	configured := inputs.Configured
	if configured == "" {
		configured = Auto
	}

	if configured == Stream {
		return StagingDecision{Mode: Stream, Reason: "the database asks for stream"}, nil
	}

	if !room {
		return StagingDecision{
			Mode: Stream,
			Reason: fmt.Sprintf("free space is %d bytes and staging needs %d, so this run streams",
				inputs.FreeBytes, needed(inputs.ExpectedBytes)),
		}, nil
	}

	if configured == Stage {
		return StagingDecision{Mode: Stage, Reason: "the database asks for stage"}, nil
	}

	return StagingDecision{
		Mode: Stage,
		Reason: fmt.Sprintf("free space is %d bytes, enough for the %d staging needs",
			inputs.FreeBytes, needed(inputs.ExpectedBytes)),
	}, nil
}

// stageImposed is the rule of E-030, in the order § 4.5 lists it.
func stageImposed(inputs StagingInputs) (bool, string) {
	switch {
	case inputs.DirectoryDump:
		return true, "the directory format"

	case inputs.Destinations > 1:
		return true, fmt.Sprintf("%d destinations", inputs.Destinations)

	case inputs.StructuralVerifyWithoutEgress:
		return true, "structural verification without egress"

	default:
		return false, ""
	}
}

// needed is the expected size with the margin of § 4.5.
func needed(expected int64) int64 {
	return expected * marginNumerator / marginDenominator
}

// PreviousBackup is what the last successful run of this database took. It is
// the best estimate there is: it happened.
type PreviousBackup struct {
	StoredBytes int64
}

// EstimateStored says how big the archive is expected to be. History first;
// failing that, a quarter of the raw size (`N-12`, § 4.5).
func EstimateStored(previous *PreviousBackup, databaseBytes int64) int64 {
	if previous != nil && previous.StoredBytes > 0 {
		return previous.StoredBytes
	}

	if databaseBytes <= 0 {
		return 0
	}

	return databaseBytes / blindCompressionDivisor
}
