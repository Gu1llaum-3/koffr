// Package retention decides which backups to delete (EF-060 to EF-065).
//
// This is the only code in Koffr that destroys anything, so it is split in two:
// Plan is pure logic over a list and a policy, testable exhaustively without
// touching a repository, and the deleting happens elsewhere against a plan
// somebody has had the chance to read.
//
// It refuses what it cannot reason about. A physical backup can have
// incrementals depending on it (EF-062) and WAL segments whose replay starts
// from it (EF-063); neither concept exists yet, so Plan stops rather than
// guesses. That guard is structural rather than a check somebody has to
// remember: when the physical backup arrives, prune fails loudly until it is
// taught, which is the failure worth having.
package retention

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// knownKinds are the kinds Plan understands.
//
// A logical backup is self-contained: one directory, no chain, no WAL. That is
// exactly why retention is safe for it and not yet for the others.
var knownKinds = map[string]bool{"logical": true}

// Policy is EF-060.
//
// Rules are a union: a backup any rule wants is kept. The alternative deletes
// something a policy explicitly asked to keep, and between the two failure
// modes only one is recoverable.
type Policy struct {
	// KeepLast keeps this many of the most recent, whatever their age.
	KeepLast int

	// KeepWithin keeps everything taken more recently than this.
	KeepWithin time.Duration

	// Hourly to Yearly keep the newest backup of each of that many periods.
	// "Daily: 7" means seven days, not seven backups.
	Hourly  int
	Daily   int
	Weekly  int
	Monthly int
	Yearly  int
}

// IsZero reports whether the policy says anything at all.
func (p Policy) IsZero() bool {
	return p.KeepLast == 0 && p.KeepWithin == 0 &&
		p.Hourly == 0 && p.Daily == 0 && p.Weekly == 0 && p.Monthly == 0 && p.Yearly == 0
}

// Validate refuses a policy rather than normalising it. A negative count is a
// typo, and guessing what it meant is how a purge does something nobody asked
// for.
func (p Policy) Validate() error {
	for name, n := range map[string]int{
		"keep_last": p.KeepLast, "hourly": p.Hourly, "daily": p.Daily,
		"weekly": p.Weekly, "monthly": p.Monthly, "yearly": p.Yearly,
	} {
		if n < 0 {
			return fmt.Errorf("retention: %s cannot be negative", name)
		}
	}
	if p.KeepWithin < 0 {
		return errors.New("retention: keep_within cannot be negative")
	}
	return nil
}

// Decision is one backup's fate, and why.
//
// The reason is not decoration: EF-064 says a dry run lists what would be
// deleted *and why*, and an operator approving a deletion needs to see which
// rule spared each survivor before believing the ones it did not.
type Decision struct {
	Backup catalog.Backup
	Keep   bool
	Reason string
}

// Option tunes a plan.
type Option func(*options)

type options struct {
	restorable func(catalog.Backup) bool
}

// WithRestorable tells Plan how to check a backup is actually there.
//
// EF-065 says the last *restorable* backup, and a catalog row is not a backup.
// If the newest one's objects were lost -- bit rot, a bucket lifecycle rule,
// someone tidying up -- the floor would spend itself on a row that restores
// nothing while the policy deleted the older good ones.
//
// Optional because not every caller can afford the round trips, and a caller
// that cannot check should not be made to pretend it did.
func WithRestorable(check func(catalog.Backup) bool) Option {
	return func(o *options) { o.restorable = check }
}

// Plan decides what to delete, and never touches anything.
func Plan(backups []catalog.Backup, p Policy, now time.Time, opts ...Option) ([]Decision, error) {
	var cfg options
	for _, opt := range opts {
		opt(&cfg)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	for _, b := range backups {
		if !knownKinds[b.Kind] {
			return nil, fmt.Errorf(
				"retention: backup %s is of kind %q, which this version cannot reason about safely: "+
					"a chain or a WAL range may depend on it (EF-062, EF-063). Nothing was deleted",
				b.ID, b.Kind)
		}
	}

	// Only completed backups are candidates. One that never finished has no
	// objects to delete and no value to keep, and counting it towards keep_last
	// would let three failures push a good backup out of the window.
	candidates := make([]catalog.Backup, 0, len(backups))
	for _, b := range backups {
		if b.Status == catalog.StatusCompleted {
			candidates = append(candidates, b)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	// Newest first, which is the order every rule below reads in.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].StartedAt.After(candidates[j].StartedAt)
	})

	reasons := make(map[catalog.ID]string, len(candidates))
	keep := func(b catalog.Backup, why string) {
		if _, already := reasons[b.ID]; !already {
			reasons[b.ID] = why
		}
	}

	if p.IsZero() {
		// Nothing configured deletes nothing. The zero value of a thing that
		// destroys backups has to be "destroy none".
		out := make([]Decision, 0, len(candidates))
		for _, b := range candidates {
			out = append(out, Decision{Backup: b, Keep: true, Reason: "no retention policy is configured"})
		}
		return out, nil
	}

	for i := 0; i < p.KeepLast && i < len(candidates); i++ {
		keep(candidates[i], fmt.Sprintf("among the last %d", p.KeepLast))
	}
	if p.KeepWithin > 0 {
		cutoff := now.Add(-p.KeepWithin)
		for _, b := range candidates {
			if b.StartedAt.After(cutoff) {
				keep(b, fmt.Sprintf("taken within the last %s", p.KeepWithin))
			}
		}
	}

	for _, rule := range []struct {
		name   string
		count  int
		bucket func(time.Time) string
	}{
		{"hourly", p.Hourly, func(t time.Time) string { return t.UTC().Format("2006-01-02T15") }},
		{"daily", p.Daily, func(t time.Time) string { return t.UTC().Format("2006-01-02") }},
		{"weekly", p.Weekly, func(t time.Time) string {
			year, week := t.UTC().ISOWeek()
			return fmt.Sprintf("%d-W%02d", year, week)
		}},
		{"monthly", p.Monthly, func(t time.Time) string { return t.UTC().Format("2006-01") }},
		{"yearly", p.Yearly, func(t time.Time) string { return t.UTC().Format("2006") }},
	} {
		if rule.count <= 0 {
			continue
		}
		// The newest of each period, most recent periods first. Walking the
		// sorted list means the first backup seen in a period is its newest.
		seen := map[string]bool{}
		taken := 0
		for _, b := range candidates {
			if taken >= rule.count {
				break
			}
			period := rule.bucket(b.StartedAt)
			if seen[period] {
				continue
			}
			seen[period] = true
			taken++
			keep(b, fmt.Sprintf("newest of %s %s", rule.name, period))
		}
	}

	// EF-065, applied last and only if nothing else spared anything restorable.
	//
	// A floor rather than a rule: when a policy already keeps a backup that is
	// really there, the policy's reason is the one an operator needs to read.
	// This one appears exactly when it did something.
	applyFloor(candidates, reasons, cfg, keep)

	out := make([]Decision, 0, len(candidates))
	for _, b := range candidates {
		why, spared := reasons[b.ID]
		if !spared {
			why = "no rule keeps it"
		}
		out = append(out, Decision{Backup: b, Keep: spared, Reason: why})
	}
	return out, nil
}

// ToDelete is the plan's deletions, in the order they should be applied:
// oldest first, so an interrupted pass has removed the least valuable.
func ToDelete(plan []Decision) []catalog.Backup {
	var out []catalog.Backup
	for _, d := range plan {
		if !d.Keep {
			out = append(out, d.Backup)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// Apply deletes what a plan says to delete, from the repository and from the
// catalog, in that order.
//
// The repository first, deliberately. If the process dies between the two, the
// catalog names a backup whose objects are gone -- which `koffr show` reports
// honestly and `catalog sync` will not resurrect. The reverse order would leave
// objects nothing points at: invisible, unlisted, and paid for every month.
//
// One failure does not stop the pass. A destination that refuses one prefix
// will probably refuse the next, but the ones that succeed are space actually
// reclaimed, and the errors are returned together rather than at the first
// stumble.
func Apply(
	ctx context.Context, st storage.Storage, cat catalog.MetadataStore, plan []Decision,
) (Applied, error) {
	var result Applied
	var failures []error

	result.SpaceReclaimed = st.Capabilities().DeleteReclaimsSpace

	for _, b := range ToDelete(plan) {
		src, err := storage.ForSource(b.SourceID)
		if err != nil {
			failures = append(failures, fmt.Errorf("retention: %s: %w", b.ID, err))
			continue
		}
		backup, err := src.Backup(storage.DirLogical, string(b.ID))
		if err != nil {
			failures = append(failures, fmt.Errorf("retention: %s: %w", b.ID, err))
			continue
		}

		freed, err := deletePrefix(ctx, st, backup.Prefix())
		if err != nil {
			failures = append(failures, err)
			continue
		}

		// The catalog row goes last and only if the objects went. A row for a
		// backup still sitting in the repository would hide it from every
		// listing while it kept being paid for.
		if err := cat.ForgetBackup(ctx, b.ID); err != nil {
			failures = append(failures, fmt.Errorf(
				"retention: %s was deleted from the repository but not from the catalog: %w", b.ID, err))
			continue
		}

		result.Deleted = append(result.Deleted, b.ID)
		if result.SpaceReclaimed {
			result.FreedBytes += freed
		}
	}

	if len(failures) > 0 {
		return result, errors.Join(failures...)
	}
	return result, nil
}

// Applied is what a pass actually did.
type Applied struct {
	Deleted []catalog.ID

	// FreedBytes is what the destination actually gave back, and is zero when
	// it gives nothing back. See SpaceReclaimed.
	FreedBytes int64

	// SpaceReclaimed says whether deleting removed the bytes or only the
	// listing.
	//
	// False on a versioned or Object-Locked bucket, where a delete writes a
	// marker and the data stays -- and stays billed -- until a lifecycle rule
	// expires it. Reported rather than assumed, because a purge announcing it
	// freed 190 MiB when the bill did not move is a number somebody will plan
	// capacity against.
	SpaceReclaimed bool
}

// deletePrefix removes every object under a backup's prefix.
//
// The manifest is deleted first, and that ordering is ENF-010 run backwards:
// the manifest's presence is what makes a set of objects a backup, so removing
// it first means an interrupted pass leaves litter rather than something a
// later reader would mistake for a backup and try to restore.
func deletePrefix(ctx context.Context, st storage.Storage, prefix string) (int64, error) {
	var keys []string
	var freed int64
	for info, err := range st.List(ctx, prefix) {
		if err != nil {
			return 0, fmt.Errorf("retention: list %s: %w", prefix, err)
		}
		keys = append(keys, info.Key)
		freed += info.Size
	}

	// The manifest is deleted first, and this used to be a sort with a
	// comparator that ignored j -- not a strict weak ordering, so sort.Slice's
	// result was formally unspecified. It happened to work in all 1829 orders
	// tried, which is not the same as being guaranteed, and "the manifest goes
	// first" is a guarantee about what an interrupted pass leaves behind.
	//
	// Moving it explicitly says the same thing and depends on nothing.
	for i, key := range keys {
		if strings.HasSuffix(key, "/"+storage.ManifestFile) {
			keys[0], keys[i] = keys[i], keys[0]
			break
		}
	}

	for _, key := range keys {
		if err := st.Delete(ctx, key); err != nil && !errors.Is(err, storage.ErrNotFound) {
			return 0, fmt.Errorf("retention: delete %s: %w", key, err)
		}
	}
	return freed, nil
}

// applyFloor makes sure something restorable survives.
//
// It walks newest-first to the first backup that is actually there, rather than
// stopping at the newest row. A row whose objects are gone is not a backup, and
// spending the floor on one would leave the source with nothing while the
// policy deleted the older good ones.
//
// If nothing is restorable, nothing is deleted. Removing the litter would be
// defensible on its own; doing it as a side effect of a retention policy, in
// the one situation where an operator has already lost their backups, is not.
func applyFloor(
	candidates []catalog.Backup,
	reasons map[catalog.ID]string,
	cfg options,
	keep func(catalog.Backup, string),
) {
	if cfg.restorable == nil {
		if _, spared := reasons[candidates[0].ID]; !spared {
			keep(candidates[0], "the only backup that would be left; no policy may delete the last one")
		}
		return
	}

	for _, b := range candidates {
		if !cfg.restorable(b) {
			continue
		}
		if _, spared := reasons[b.ID]; !spared {
			keep(b, "the only restorable backup that would be left; no policy may delete the last one")
		}
		return
	}

	for _, b := range candidates {
		keep(b, "nothing was deleted: Koffr cannot confirm any of these backups is still in the repository")
	}
}
