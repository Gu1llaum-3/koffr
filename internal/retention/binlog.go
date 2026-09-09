package retention

import (
	"fmt"

	"github.com/Gu1llaum-3/koffr/internal/binlog"
	"github.com/Gu1llaum-3/koffr/internal/catalog"
)

// BinlogPlan says which archived binary log files may go (EF-063).
//
// A file is needed by every backup whose anchor sits in it or before it: a
// point-in-time recovery from that backup replays from its anchor onwards, and
// a hole anywhere on the way is a recovery that stops short. So the floor is
// the anchor of the *oldest kept* backup, and nothing at or after the floor is
// deletable.
//
// The decision is made from the names alone, which is what makes it a pure
// function and what lets a test hand it any shape of archive and any set of
// kept backups. What it must never do is guess: a kept backup with no anchor
// -- taken before EF-031 was wired, or with the binary log off -- means the
// floor is unknown, and an unknown floor deletes nothing.
type BinlogPlan struct {
	Delete []binlog.Name
	Keep   []binlog.Name
	// Floor is the oldest file still needed, when it could be established.
	Floor *binlog.Name
	// Reason says why nothing is deleted, when nothing is.
	Reason string
}

// PlanBinlog decides for one source's archive, given the backups retention is
// keeping there.
func PlanBinlog(archived []binlog.Name, kept []catalog.Backup) (BinlogPlan, error) {
	sorted := append([]binlog.Name(nil), archived...)
	if err := binlog.Sort(sorted); err != nil {
		return BinlogPlan{}, err
	}
	if len(sorted) == 0 {
		return BinlogPlan{}, nil
	}

	if len(kept) == 0 {
		// No backup to replay from means the archive serves nothing today --
		// and deleting it would still be wrong: the next backup will need the
		// files written between now and then, and this archive is the only
		// place they are. The archive outlives any one backup on purpose.
		return BinlogPlan{Keep: sorted,
			Reason: "no kept backup anchors this archive yet; the archive is kept whole until one does"}, nil
	}

	var floor *binlog.Name
	for _, b := range kept {
		if b.BinlogFile == "" {
			return BinlogPlan{Keep: sorted, Reason: fmt.Sprintf(
				"backup %s has no binary log anchor, so the oldest file still needed cannot be known; nothing is deleted",
				b.ID)}, nil
		}
		n, err := binlog.Parse(b.BinlogFile)
		if err != nil {
			return BinlogPlan{}, fmt.Errorf("retention: backup %s anchors to %q: %w", b.ID, b.BinlogFile, err)
		}
		if n.Base != sorted[0].Base {
			return BinlogPlan{Keep: sorted, Reason: fmt.Sprintf(
				"backup %s anchors to %s, which is not from this archive (%s); nothing is deleted",
				b.ID, b.BinlogFile, sorted[0].Base)}, nil
		}
		if floor == nil || n.Seq < floor.Seq {
			f := n
			floor = &f
		}
	}

	plan := BinlogPlan{Floor: floor}
	for _, n := range sorted {
		if n.Seq < floor.Seq {
			plan.Delete = append(plan.Delete, n)
		} else {
			plan.Keep = append(plan.Keep, n)
		}
	}
	return plan, nil
}

// KeptBackups filters a plan down to what stays, which is what PlanBinlog
// wants to know about.
func KeptBackups(plan []Decision) []catalog.Backup {
	var kept []catalog.Backup
	for _, d := range plan {
		if d.Keep {
			kept = append(kept, d.Backup)
		}
	}
	return kept
}
