package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/retention"
)

func (a *app) pruneCmd() *cobra.Command {
	var (
		sourceID string
		confirm  bool
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
			return a.runPrune(cmd.Context(), sourceID, confirm)
		},
	}
	c.Flags().BoolVar(&confirm, "confirm", false, "actually delete; without it nothing is touched")
	return c
}

// pruneLine is one backup's fate in the report. The reason is the point: an
// operator approving a deletion needs to see which rule spared each survivor
// before believing the ones it did not.
type pruneLine struct {
	BackupID string `json:"backup_id"`
	Source   string `json:"source"`
	TakenAt  string `json:"taken_at"`
	Keep     bool   `json:"keep"`
	Reason   string `json:"reason"`
}

func (a *app) runPrune(ctx context.Context, sourceID string, confirm bool) error {
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
		lines   []pruneLine
		deleted []catalog.ID
		freed   int64
	)
	for _, id := range ids {
		src, _ := cfg.Source(id)
		if src.Retention.Policy().IsZero() {
			// Said out loud rather than skipped in silence: an operator who
			// expected a purge and got none should learn why here.
			a.printf("%s: no retention policy, keeping everything", id)
			continue
		}

		plan, err := a.planFor(ctx, cat, cfg, id, src)
		if err != nil {
			return err
		}
		for _, d := range plan {
			lines = append(lines, pruneLine{
				BackupID: string(d.Backup.ID), Source: id,
				TakenAt: d.Backup.StartedAt.UTC().Format(time.RFC3339),
				Keep:    d.Keep, Reason: d.Reason,
			})
		}
		if !confirm {
			continue
		}

		applied, err := a.applyFor(ctx, cat, cfg, src, plan)
		if err != nil {
			return err
		}
		deleted = append(deleted, applied.Deleted...)
		freed += applied.FreedBytes
	}

	out := struct {
		DryRun  bool        `json:"dry_run"`
		Backups []pruneLine `json:"backups"`
		Deleted int         `json:"deleted"`
		Freed   int64       `json:"freed_bytes"`
	}{!confirm, lines, len(deleted), freed}

	a.emit(out, func(p *printer) {
		p.table(func(p *printer) {
			p.printf("BACKUP ID\tSOURCE\tTAKEN\tVERDICT\tWHY\n")
			for _, l := range lines {
				verdict := "delete"
				if l.Keep {
					verdict = "keep"
				}
				p.printf("%s\t%s\t%s\t%s\t%s\n", l.BackupID, l.Source, l.TakenAt, verdict, l.Reason)
			}
		})
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
		p.printf("\ndeleted %d, freed %s\n", out.Deleted, humanBytes(out.Freed))
	})
	return nil
}

func (a *app) planFor(
	ctx context.Context, cat catalog.MetadataStore, cfg config.Config, id string, src config.Source,
) ([]retention.Decision, error) {
	backups, err := cat.ListBackups(ctx, catalog.BackupFilter{SourceID: id})
	if err != nil {
		return nil, err
	}
	plan, err := retention.Plan(backups, src.Retention.Policy(), time.Now().In(cfg.Scheduler.Location()))
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
	src config.Source, plan []retention.Decision,
) (retention.Applied, error) {
	_, dest, err := a.destinationFor(cfg, src, "")
	if err != nil {
		return retention.Applied{}, err
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
