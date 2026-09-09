package cli

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/catalog/replica"
	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/retention"
	"github.com/Gu1llaum-3/koffr/internal/storage"
)

func (a *app) pruneCmd() *cobra.Command {
	var (
		sourceID string
		confirm  bool
		orphans  bool
	)
	c := &cobra.Command{
		Use:   "prune [source]",
		Short: "Delete backups a retention policy no longer keeps",
		Long: "Delete the backups a source's retention policy no longer keeps.\n\n" +
			"Nothing is deleted without --confirm. Running it without is the supported\n" +
			"way to use this command: it lists exactly what would go and which rule\n" +
			"spared each survivor, so approving a deletion means having read one\n" +
			"(EF-064, EF-105).\n\n" +
			"A source with no retention policy keeps everything. That is the only safe\n" +
			"default for a setting whose mistakes cannot be undone.\n\n" +
			"The last backup of a source is never deleted, whatever the policy says\n" +
			"(EF-065). And a backup of a kind this version cannot reason about stops\n" +
			"the whole pass: a physical backup can have incrementals depending on it\n" +
			"and WAL whose replay starts from it, and guessing about that is the one\n" +
			"mistake with no recovery.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.begin(cmd); err != nil {
				return err
			}
			if len(args) == 1 {
				sourceID = args[0]
			}
			return a.runPrune(cmd.Context(), sourceID, confirm, orphans)
		},
	}
	c.Flags().BoolVar(&confirm, "confirm", false, "actually delete; without it nothing is touched")
	c.Flags().BoolVar(&orphans, "orphans", false,
		"also sweep what a job that died before writing its manifest left behind:\n"+
			"objects no manifest points at, and unfinished uploads the store still bills for")
	return c
}

// pruneLine is one backup's fate in the report. The reason is the point: an
// operator approving a deletion needs to see which rule spared each survivor
// before believing the ones it did not.
type pruneLine struct {
	BackupID    string `json:"backup_id"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	TakenAt     string `json:"taken_at"`
	Keep        bool   `json:"keep"`
	Reason      string `json:"reason"`
}

func (a *app) runPrune(ctx context.Context, sourceID string, confirm, sweepOrphans bool) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}

	ids := cfg.SourceIDs()
	if sourceID != "" {
		if _, err := a.source(cfg, sourceID); err != nil {
			return err
		}
		ids = []string{sourceID}
	}

	cat, err := openCatalog(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = cat.Close() }()

	var (
		lines       []pruneLine
		binlogLines []binlogLine
		deleted     []catalog.ID
		freed       int64
		keepsData   []string
	)
	for _, id := range ids {
		src, _ := cfg.Source(id)

		// One pass per destination, each with its own policy (EF-044). Keeping
		// seven days locally and twelve months offsite is the point of writing
		// to both, so a single pass over the source would be the wrong shape.
		for _, destName := range src.Destinations {
			policy := src.RetentionFor(destName)
			if policy.IsZero() {
				a.printf("%s on %s: no retention policy, keeping everything", id, destName)
				continue
			}

			plan, err := a.planFor(ctx, cat, cfg, id, src, destName, policy)
			if err != nil {
				return err
			}
			for _, d := range plan {
				lines = append(lines, pruneLine{
					BackupID: string(d.Backup.ID), Source: id, Destination: destName,
					TakenAt: d.Backup.StartedAt.UTC().Format(time.RFC3339),
					Keep:    d.Keep, Reason: d.Reason,
				})
			}
			// The binary log archive follows the backups it serves (EF-063):
			// its floor is the oldest backup this pass keeps, so it is decided
			// here, on the destination that holds it, and never on its own.
			var bp *retention.BinlogPlan
			if archivesTo(src, destName) {
				var bl []binlogLine
				bp, bl, err = a.planBinlogFor(ctx, cfg, id, src, plan)
				if err != nil {
					return err
				}
				binlogLines = append(binlogLines, bl...)
			}
			if !confirm {
				continue
			}
			if bp != nil {
				n, err := a.applyBinlog(ctx, cfg, id, src, bp)
				if err != nil {
					return err
				}
				freed += n
			}

			applied, err := a.applyFor(ctx, cat, cfg, destName, plan)
			if err != nil {
				return err
			}
			deleted = append(deleted, applied.Deleted...)
			freed += applied.FreedBytes
			if len(applied.Deleted) > 0 && !applied.SpaceReclaimed &&
				!slices.Contains(keepsData, destName) {
				keepsData = append(keepsData, destName)
			}
		}
	}

	// The replica in the repository still lists what was just deleted, and
	// `catalog sync` merges rather than replaces -- so without this, a rebuild
	// resurrects every pruned backup as a row nothing can restore.
	if len(deleted) > 0 {
		if warn := a.refreshReplica(ctx, cfg, cat); warn != "" {
			a.warnf("koffr: %s", warn)
		}
	}

	var (
		orphanLines []orphanLine
		uploadLines []uploadLine
	)
	if sweepOrphans {
		found, stale, err := a.sweepOrphans(ctx, cfg, confirm)
		if err != nil {
			return err
		}
		orphanLines, uploadLines = found, stale
		if confirm {
			for _, o := range found {
				freed += o.Bytes
			}
		}
	}

	// The archive's files are deleted too, and a summary that counted only
	// backups said "deleted 0" over eight of them on the first real run.
	binlogDeletes := 0
	for _, b := range binlogLines {
		if !b.Keep {
			binlogDeletes++
		}
	}

	out := struct {
		DryRun  bool         `json:"dry_run"`
		Backups []pruneLine  `json:"backups"`
		Binlogs []binlogLine `json:"binlogs,omitempty"`
		Orphans []orphanLine `json:"orphans,omitempty"`
		// IncompleteUploads are billed and invisible to every listing, so a
		// script watching this repository has no other way to learn of them.
		IncompleteUploads  []uploadLine `json:"incomplete_uploads,omitempty"`
		Deleted            int          `json:"deleted"`
		BinlogFilesDeleted int          `json:"binlog_files_deleted,omitempty"`
		Freed              int64        `json:"freed_bytes"`
		// SpaceReclaimed is false when the destination keeps what it deletes.
		// A script watching freed_bytes needs to know the number is zero
		// because nothing was freed, not because nothing was deleted.
		SpaceReclaimed bool `json:"space_reclaimed"`
	}{!confirm, lines, binlogLines, orphanLines, uploadLines, len(deleted), binlogDeletes, freed, len(keepsData) == 0}

	a.emit(out, func(p *printer) {
		p.table(func(p *printer) {
			p.printf("BACKUP ID\tSOURCE\tDESTINATION\tTAKEN\tVERDICT\tWHY\n")
			for _, l := range lines {
				verdict := "delete"
				if l.Keep {
					verdict = "keep"
				}
				p.printf("%s\t%s\t%s\t%s\t%s\t%s\n",
					l.BackupID, l.Source, l.Destination, l.TakenAt, verdict, l.Reason)
			}
		})
		if len(binlogLines) > 0 {
			p.printf("\nbinary log archive:\n")
			for _, b := range binlogLines {
				verdict := "delete"
				if b.Keep {
					verdict = "keep"
				}
				p.printf("  %s  %s  %s  %s\n", b.Source, b.File, verdict, b.Reason)
			}
		}
		if len(orphanLines) > 0 {
			p.printf("\norphan objects (a job died before writing its manifest):\n")
			for _, o := range orphanLines {
				p.printf("  %s  %s\n", o.Prefix, humanBytes(o.Bytes))
			}
		}
		if len(uploadLines) > 0 {
			p.printf("\nunfinished uploads (a job was killed mid-transfer; " +
				"stored and billed, and no listing shows them):\n")
			for _, u := range uploadLines {
				p.printf("  %s  %s  %s  begun %s\n",
					u.Destination, u.Key, humanBytes(u.Bytes), u.Begun)
			}
		}
		if out.DryRun {
			var wouldGo int
			for _, l := range lines {
				if !l.Keep {
					wouldGo++
				}
			}
			p.printf("\n%s would be deleted. Nothing was: pass --confirm.\n", countOf(wouldGo, binlogDeletes))
			return
		}
		if len(keepsData) > 0 {
			// Said in full rather than as a footnote. An operator reading
			// "deleted 3" on a versioned bucket will assume the bill moved,
			// and it did not.
			p.printf("\ndeleted %s. No space was reclaimed: %s keeps previous versions "+
				"of what it deletes, so the bytes stay until a bucket lifecycle rule "+
				"expires them.\n", countOf(out.Deleted, binlogDeletes), strings.Join(keepsData, ", "))
			return
		}
		p.printf("\ndeleted %s, freed %s\n", countOf(out.Deleted, binlogDeletes), humanBytes(out.Freed))
	})
	return nil
}

func (a *app) planFor(
	ctx context.Context, cat catalog.MetadataStore, cfg config.Config,
	id string, src config.Source, destName string, policy retention.Policy,
) ([]retention.Decision, error) {
	backups, err := cat.ListBackups(ctx,
		catalog.BackupFilter{SourceID: id, Destination: destName})
	if err != nil {
		return nil, err
	}

	// EF-065 wants the last *restorable* backup, and a catalog row is not one.
	// Checking costs a Stat per backup and only until the first that is there,
	// which is a price worth paying before deleting anything.
	restorable, err := a.restorableCheck(ctx, cfg, destName)
	if err != nil {
		return nil, err
	}

	plan, err := retention.Plan(backups, policy,
		time.Now().In(cfg.Scheduler.Location()), retention.WithRestorable(restorable))
	if err != nil {
		// A kind this version cannot reason about. Refused for the whole pass,
		// not skipped: a partial purge is worse than none, because it looks
		// like it worked.
		return nil, &Fault{Code: ExitConfig, Err: err}
	}
	return plan, nil
}

func (a *app) applyFor(
	ctx context.Context, cat catalog.MetadataStore, cfg config.Config,
	destName string, plan []retention.Decision,
) (retention.Applied, error) {
	dest, known := cfg.Destinations[destName]
	if !known {
		return retention.Applied{}, fault(ExitConfig, "no destination %q", destName)
	}
	st, err := openStorage(ctx, dest)
	if err != nil {
		return retention.Applied{}, err
	}

	applied, err := retention.Apply(ctx, st, cat, plan)
	if err != nil {
		return applied, fmt.Errorf("prune: %w", err)
	}
	return applied, nil
}

// refreshReplica rewrites the catalog copy in every destination a pruned source
// writes to.
//
// A warning rather than an error: the deletions already happened and are
// correct. A stale replica is a rebuild that resurrects rows, which is
// annoying and visible, not a backup that is gone.
func (a *app) refreshReplica(ctx context.Context, cfg config.Config, cat catalog.MetadataStore) string {
	snap, err := cat.Export(ctx)
	if err != nil {
		return "the catalog copy in the repository was not refreshed: " + err.Error()
	}
	sealer, err := sealerFor(cfg)
	if err != nil {
		return "the catalog copy in the repository was not refreshed: " + err.Error()
	}

	for _, name := range sortedKeys(cfg.Destinations) {
		st, err := openStorage(ctx, cfg.Destinations[name])
		if err != nil {
			return fmt.Sprintf("the catalog copy in %s was not refreshed: %v", name, err)
		}
		if err := replica.Write(ctx, st, sealer, snap); err != nil {
			return fmt.Sprintf("the catalog copy in %s was not refreshed: %v", name, err)
		}
	}
	return ""
}

// restorableCheck asks the repository whether a backup's manifest is still
// there.
//
// The manifest is the right thing to look for: its presence is what makes a set
// of objects a backup (ENF-010), so a prefix without one is litter whatever
// else it holds.
func (a *app) restorableCheck(
	ctx context.Context, cfg config.Config, destName string,
) (func(catalog.Backup) bool, error) {
	dest, known := cfg.Destinations[destName]
	if !known {
		return nil, fault(ExitConfig, "no destination %q", destName)
	}
	st, err := openStorage(ctx, dest)
	if err != nil {
		return nil, err
	}

	return func(b catalog.Backup) bool {
		layoutSource, err := storage.ForSource(b.SourceID)
		if err != nil {
			return false
		}
		backup, err := layoutSource.Backup(storage.DirLogical, string(b.ID))
		if err != nil {
			return false
		}
		// An error that is not "absent" -- a network blip, a permission
		// problem -- reads as not restorable, which makes the floor keep more
		// rather than less. Being wrong in that direction costs disk; being
		// wrong the other way costs the backup.
		_, err = st.Stat(ctx, backup.ManifestKey())
		return err == nil
	}, nil
}

// countOf words a prune's tally. Backups are always counted, even at zero;
// binary log files only when some go, so a repository without an archive reads
// as it always did.
func countOf(backups, binlogFiles int) string {
	if binlogFiles == 0 {
		return strconv.Itoa(backups)
	}
	return fmt.Sprintf("%d backup(s) and %d binary log file(s)", backups, binlogFiles)
}

// binlogLine is one archived binary log file and what retention decided.
type binlogLine struct {
	Source string `json:"source"`
	File   string `json:"file"`
	Keep   bool   `json:"keep"`
	Reason string `json:"reason,omitempty"`
}

// archivesTo says whether a source's binary log archive lives on this destination.
func archivesTo(src config.Source, destName string) bool {
	if src.Binlog == nil || !src.Binlog.Enabled {
		return false
	}
	dest := src.Binlog.Destination
	if dest == "" && len(src.Destinations) > 0 {
		dest = src.Destinations[0]
	}
	return dest == destName
}

// planBinlogFor decides the archive's fate from what retention keeps.
func (a *app) planBinlogFor(
	ctx context.Context, cfg config.Config, id string, src config.Source, plan []retention.Decision,
) (*retention.BinlogPlan, []binlogLine, error) {
	archive, err := a.binlogArchive(ctx, cfg, id, src)
	if err != nil {
		return nil, nil, err
	}
	archived, err := archive.Archived(ctx)
	if err != nil {
		return nil, nil, err
	}
	bp, err := retention.PlanBinlog(archived, retention.KeptBackups(plan))
	if err != nil {
		return nil, nil, fmt.Errorf("prune: %s: %w", id, err)
	}
	var lines []binlogLine
	for _, n := range bp.Delete {
		lines = append(lines, binlogLine{Source: id, File: n.String(), Keep: false,
			Reason: "before the oldest kept backup's position"})
	}
	if len(bp.Keep) > 0 {
		reason := bp.Reason
		if reason == "" && bp.Floor != nil {
			reason = fmt.Sprintf("needed from %s onwards by the oldest kept backup", bp.Floor)
		}
		// One line for the kept range rather than one per file: an archive
		// holds thousands, and a purge report nobody can read is a purge
		// report nobody reads.
		lines = append(lines, binlogLine{Source: id,
			File: bp.Keep[0].String() + " .. " + bp.Keep[len(bp.Keep)-1].String(), Keep: true, Reason: reason})
	}
	return &bp, lines, nil
}

// applyBinlog deletes what the plan allows and reports the bytes.
func (a *app) applyBinlog(
	ctx context.Context, cfg config.Config, id string, src config.Source, bp *retention.BinlogPlan,
) (int64, error) {
	if len(bp.Delete) == 0 {
		return 0, nil
	}
	archive, err := a.binlogArchive(ctx, cfg, id, src)
	if err != nil {
		return 0, err
	}
	var freed int64
	for _, n := range bp.Delete {
		size, err := archive.Delete(ctx, n)
		if err != nil {
			return freed, fmt.Errorf("prune: %s: %w", id, err)
		}
		freed += size
	}
	return freed, nil
}

// orphanLine is one prefix with objects and no manifest.
type orphanLine struct {
	Prefix string `json:"prefix"`
	Bytes  int64  `json:"bytes"`
}

// orphanGrace is how recently a prefix may have been touched and still be
// considered a job in progress rather than litter.
//
// Generous on purpose. A backup being written has objects and no manifest,
// which from outside is exactly what litter looks like, and deleting a running
// job is a far worse outcome than paying for a stale prefix another day. A
// 10 GiB backup takes minutes; this allows for one taking hours.
const orphanGrace = 24 * time.Hour

// uploadLine is one multipart upload begun by a job that never came back.
type uploadLine struct {
	Destination string `json:"destination"`
	Key         string `json:"key"`
	Begun       string `json:"begun"`
	// Bytes is what its parts hold, which is what the store is charging for.
	Bytes int64 `json:"bytes"`
}

// sweepOrphans finds, and with confirm removes, what no manifest points at.
//
// Two kinds of litter, one accident. A job killed partway leaves objects with
// no manifest, which a listing shows, and on an object store it also leaves an
// unfinished multipart upload, which no listing shows and which the service
// charges for regardless. Reporting only the visible half would let an operator
// tidy a repository and keep paying for the rest.
//
// Off unless asked. Sweeping is the one deletion Koffr can make that is not
// described by any policy, so it stays a thing an operator does deliberately.
func (a *app) sweepOrphans(
	ctx context.Context, cfg config.Config, confirm bool,
) ([]orphanLine, []uploadLine, error) {
	var (
		out     []orphanLine
		uploads []uploadLine
	)
	for _, name := range sortedKeys(cfg.Destinations) {
		st, err := openStorage(ctx, cfg.Destinations[name])
		if err != nil {
			return nil, nil, err
		}
		found, err := retention.FindOrphansOlderThan(ctx, st, orphanGrace)
		if err != nil {
			return nil, nil, err
		}
		for _, o := range found {
			out = append(out, orphanLine{Prefix: o.Prefix, Bytes: o.Bytes})
		}

		// The same grace period, for the same reason: from outside, an upload
		// in flight and one abandoned last month look identical, and aborting
		// the wrong one kills a running backup.
		stale, err := retention.FindIncompleteUploadsOlderThan(ctx, st, orphanGrace)
		if err != nil {
			return nil, nil, err
		}
		for _, u := range stale {
			uploads = append(uploads, uploadLine{
				Destination: name,
				Key:         u.Key,
				Begun:       u.Initiated.UTC().Format(time.RFC3339),
				Bytes:       u.Bytes,
			})
		}

		if !confirm {
			continue
		}
		if len(found) > 0 {
			if _, err := retention.RemoveOrphans(ctx, st, found); err != nil {
				return nil, nil, fmt.Errorf("prune: sweeping orphans in %s: %w", name, err)
			}
		}
		if err := retention.AbortIncompleteUploads(ctx, st, stale); err != nil {
			return nil, nil, fmt.Errorf("prune: abandoning unfinished uploads in %s: %w", name, err)
		}
	}
	return out, uploads, nil
}
