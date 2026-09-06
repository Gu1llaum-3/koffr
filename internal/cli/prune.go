package cli

import (
	"context"
	"fmt"
	"slices"
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
		"also sweep objects left by a job that died before writing its manifest")
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
		lines     []pruneLine
		deleted   []catalog.ID
		freed     int64
		keepsData []string
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
			if !confirm {
				continue
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

	var orphanLines []orphanLine
	if sweepOrphans {
		found, err := a.sweepOrphans(ctx, cfg, confirm)
		if err != nil {
			return err
		}
		orphanLines = found
		if confirm {
			for _, o := range found {
				freed += o.Bytes
			}
		}
	}

	out := struct {
		DryRun  bool         `json:"dry_run"`
		Backups []pruneLine  `json:"backups"`
		Orphans []orphanLine `json:"orphans,omitempty"`
		Deleted int          `json:"deleted"`
		Freed   int64        `json:"freed_bytes"`
		// SpaceReclaimed is false when the destination keeps what it deletes.
		// A script watching freed_bytes needs to know the number is zero
		// because nothing was freed, not because nothing was deleted.
		SpaceReclaimed bool `json:"space_reclaimed"`
	}{!confirm, lines, orphanLines, len(deleted), freed, len(keepsData) == 0}

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
		if len(orphanLines) > 0 {
			p.printf("\norphan objects (a job died before writing its manifest):\n")
			for _, o := range orphanLines {
				p.printf("  %s  %s\n", o.Prefix, humanBytes(o.Bytes))
			}
		}
		if out.DryRun {
			var wouldGo int
			for _, l := range lines {
				if !l.Keep {
					wouldGo++
				}
			}
			p.printf("\n%d would be deleted. Nothing was: pass --confirm.\n", wouldGo)
			return
		}
		if len(keepsData) > 0 {
			// Said in full rather than as a footnote. An operator reading
			// "deleted 3" on a versioned bucket will assume the bill moved,
			// and it did not.
			p.printf("\ndeleted %d. No space was reclaimed: %s keeps previous versions "+
				"of what it deletes, so the bytes stay until a bucket lifecycle rule "+
				"expires them.\n", out.Deleted, strings.Join(keepsData, ", "))
			return
		}
		p.printf("\ndeleted %d, freed %s\n", out.Deleted, humanBytes(out.Freed))
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

// sweepOrphans finds, and with confirm removes, objects no manifest points at.
//
// Off unless asked. Sweeping is the one deletion Koffr can make that is not
// described by any policy, so it stays a thing an operator does deliberately.
func (a *app) sweepOrphans(ctx context.Context, cfg config.Config, confirm bool) ([]orphanLine, error) {
	var out []orphanLine
	for _, name := range sortedKeys(cfg.Destinations) {
		st, err := openStorage(ctx, cfg.Destinations[name])
		if err != nil {
			return nil, err
		}
		found, err := retention.FindOrphansOlderThan(ctx, st, orphanGrace)
		if err != nil {
			return nil, err
		}
		for _, o := range found {
			out = append(out, orphanLine{Prefix: o.Prefix, Bytes: o.Bytes})
		}
		if !confirm || len(found) == 0 {
			continue
		}
		if _, err := retention.RemoveOrphans(ctx, st, found); err != nil {
			return nil, fmt.Errorf("prune: sweeping orphans in %s: %w", name, err)
		}
	}
	return out, nil
}
