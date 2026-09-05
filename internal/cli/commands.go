package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/spf13/cobra"

	"github.com/Gu1llaum-3/koffr/internal/backup"
	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/executor/local"
	"github.com/Gu1llaum-3/koffr/internal/manifest"
	"github.com/Gu1llaum-3/koffr/internal/restore"
	"github.com/Gu1llaum-3/koffr/internal/source"
	"github.com/Gu1llaum-3/koffr/internal/source/postgres"
	"github.com/Gu1llaum-3/koffr/internal/storage"
	"github.com/Gu1llaum-3/koffr/internal/version"
)

// ---------------------------------------------------------------- version

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Long: "Print the version.\n\n" +
			"The same string is recorded in every manifest, so a backup can always say\n" +
			"what wrote it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.begin(cmd); err != nil {
				return err
			}
			v := struct {
				Version  string `json:"version"`
				Go       string `json:"go"`
				Platform string `json:"platform"`
			}{version.Value, runtime.Version(), runtime.GOOS + "/" + runtime.GOARCH}
			a.emit(v, func(p *printer) {
				p.printf("koffr %s (%s, %s)\n", v.Version, v.Go, v.Platform)
			})
			return nil
		},
	}
}

// ---------------------------------------------------------------- config

func (a *app) configCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Inspect the configuration",
	}
	c.AddCommand(a.configValidateCmd(), a.configShowCmd())
	return c
}

func (a *app) configValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Check the configuration and report every problem",
		Long: "Check the configuration and report every problem.\n\n" +
			"Every problem, not the first one: correcting a configuration one message\n" +
			"at a time is the difference between a tool people keep and one they fight.\n\n" +
			"Exits 3 if the file is invalid, so this is safe to put in front of a deploy.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.begin(cmd); err != nil {
				return err
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			out := struct {
				File         string   `json:"file"`
				Sources      []string `json:"sources"`
				Destinations []string `json:"destinations"`
			}{cfg.Path(), cfg.SourceIDs(), sortedKeys(cfg.Destinations)}

			a.emit(out, func(p *printer) {
				p.printf("%s is valid.\n", out.File)
				p.printf("  sources:      %s\n", strings.Join(out.Sources, ", "))
				p.printf("  destinations: %s\n", strings.Join(out.Destinations, ", "))
			})
			return nil
		},
	}
}

func (a *app) configShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the configuration with secrets redacted",
		Long: "Print the configuration as Koffr understood it, with every secret shown as\n" +
			"the reference it came from rather than its value.\n\n" +
			"Safe to paste into a ticket.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.begin(cmd); err != nil {
				return err
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			rendered, err := cfg.Redacted()
			if err != nil {
				return err
			}
			a.emit(struct {
				File string `json:"file"`
				YAML string `json:"yaml"`
			}{cfg.Path(), rendered}, func(p *printer) {
				p.printf("%s", rendered)
			})
			return nil
		},
	}
}

// ---------------------------------------------------------------- check

func (a *app) checkCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "check [source]",
		Short: "Check that every source and destination is actually usable",
		Long: "Reach every source and every destination and report what works.\n\n" +
			"This is the command to run before trusting a schedule: it finds the missing\n" +
			"client binary, the unreachable host and the unwritable bucket now, rather\n" +
			"than at three in the morning (PD-006).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.begin(cmd); err != nil {
				return err
			}
			return a.runCheck(cmd.Context(), args)
		},
	}
	return c
}

// checkResult is one line of the report.
type checkResult struct {
	What    string `json:"what"`
	Target  string `json:"target"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail,omitempty"`
	Problem string `json:"problem,omitempty"`
}

func (a *app) runCheck(ctx context.Context, args []string) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}

	var results []checkResult
	for _, name := range sortedKeys(cfg.Destinations) {
		results = append(results, checkDestination(ctx, name, cfg.Destinations[name]))
	}

	ids := cfg.SourceIDs()
	if len(args) == 1 {
		if _, err := a.source(cfg, args[0]); err != nil {
			return err
		}
		ids = []string{args[0]}
	}
	for _, id := range ids {
		src, _ := cfg.Source(id)
		results = append(results, checkSource(ctx, id, src))
	}

	failed := 0
	for _, r := range results {
		if !r.OK {
			failed++
		}
	}

	a.emit(struct {
		Checks []checkResult `json:"checks"`
		Failed int           `json:"failed"`
	}{results, failed}, func(p *printer) {
		p.table(func(p *printer) {
			for _, r := range results {
				mark, detail := "ok", r.Detail
				if !r.OK {
					mark, detail = "FAIL", r.Problem
				}
				p.printf("%s\t%s\t%s\t%s\n", mark, r.What, r.Target, detail)
			}
		})
	})

	if failed > 0 {
		// Not a backup failure: nothing was attempted. But not success either,
		// which is the whole point of running this in a pipeline.
		return fault(ExitFailure, "%d of %d checks failed", failed, len(results))
	}
	return nil
}

func checkDestination(ctx context.Context, name string, dest config.Destination) checkResult {
	r := checkResult{What: "destination", Target: name}
	st, err := openStorage(ctx, dest)
	if err != nil {
		r.Problem = err.Error()
		return r
	}
	// Listing proves credentials and reachability without writing anything into
	// someone's repository as a side effect of a check.
	for _, err := range st.List(ctx, "sources/") {
		if err != nil {
			r.Problem = err.Error()
			return r
		}
		break
	}
	caps := st.Capabilities()
	r.OK = true
	r.Detail = fmt.Sprintf("%s, multipart=%t immutable=%t", dest.Type, caps.Multipart, caps.Immutable)
	return r
}

func checkSource(ctx context.Context, id string, src config.Source) checkResult {
	r := checkResult{What: "source", Target: id}

	ex, err := executorFor(ctx, src)
	if err != nil {
		r.Problem = err.Error()
		return r
	}
	defer func() { _ = ex.Close() }()

	s, err := sourceFor(src, ex)
	if err != nil {
		r.Problem = err.Error()
		return r
	}
	info, err := s.Probe(ctx, ex)
	if err != nil {
		r.Problem = err.Error()
		return r
	}

	r.OK = true
	kinds := make([]string, 0, len(info.Kinds))
	for _, k := range info.Kinds {
		kinds = append(kinds, string(k))
	}
	r.Detail = fmt.Sprintf("%s %s, can do: %s", info.Engine, info.ServerVersion, strings.Join(kinds, " "))
	if len(info.Restrictions) > 0 {
		r.Detail += " (" + strings.Join(info.Restrictions, "; ") + ")"
	}
	return r
}

// sourceFor builds the engine driver for a configured source.
func sourceFor(src config.Source, ex executor.Executor) (source.Source, error) {
	switch src.Engine {
	case "postgresql":
		return postgres.NewLogical(postgresConfig(src, localToolRunner()))
	default:
		return nil, fmt.Errorf("engine %q is not supported yet", src.Engine)
	}
}

// ---------------------------------------------------------------- helpers

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// ---------------------------------------------------------------- backup

func (a *app) backupCmd() *cobra.Command {
	var (
		destination string
		kind        string
		label       string
		includeS    []string
		excludeS    []string
		includeT    []string
		excludeT    []string
	)
	c := &cobra.Command{
		Use:   "backup <source>",
		Short: "Back up a source",
		Long: "Back up a source, streaming it into its destination.\n\n" +
			"Nothing is written to the database host and nothing is staged on this one:\n" +
			"the dump is compressed, encrypted and uploaded as it is produced.\n\n" +
			"The source must be one the configuration defines. A flag says what to do\n" +
			"now; it never invents a source, because a backup nothing in the file\n" +
			"describes is one the next run will not repeat.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.begin(cmd); err != nil {
				return err
			}
			return a.runBackup(cmd.Context(), args[0], backupOptions{
				destination: destination,
				request: source.Request{
					Kind:           source.Kind(kind),
					Label:          label,
					IncludeSchemas: includeS,
					ExcludeSchemas: excludeS,
					IncludeTables:  includeT,
					ExcludeTables:  excludeT,
				},
			})
		},
	}
	c.Flags().StringVar(&destination, "destination", "", "destination to write to (required if the source has several)")
	c.Flags().StringVar(&kind, "kind", string(source.KindLogical), "backup kind: logical")
	c.Flags().StringVar(&label, "label", "", "label recorded with the backup")
	c.Flags().StringSliceVar(&includeS, "include-schema", nil, "restrict to these schemas")
	c.Flags().StringSliceVar(&excludeS, "exclude-schema", nil, "skip these schemas")
	c.Flags().StringSliceVar(&includeT, "include-table", nil, "restrict to these tables")
	c.Flags().StringSliceVar(&excludeT, "exclude-table", nil, "skip these tables")
	return c
}

type backupOptions struct {
	destination string
	request     source.Request
}

func (a *app) runBackup(ctx context.Context, sourceID string, opt backupOptions) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	src, err := a.source(cfg, sourceID)
	if err != nil {
		return err
	}
	destName, dest, err := a.destinationFor(cfg, src, opt.destination)
	if err != nil {
		return err
	}

	// Everything from here on is the attempt itself, so every failure is a
	// backup failure: the code that pages someone.
	res, err := a.doBackup(ctx, cfg, sourceID, src, destName, dest, opt)
	if err != nil {
		var f *Fault
		if errorsAs(err, &f) {
			return err
		}
		return &Fault{Code: ExitBackup, Err: err}
	}

	out := struct {
		BackupID string  `json:"backup_id"`
		Source   string  `json:"source"`
		Kind     string  `json:"kind"`
		Dest     string  `json:"destination"`
		Prefix   string  `json:"prefix"`
		Bytes    int64   `json:"bytes"`
		Seconds  float64 `json:"seconds"`
	}{
		BackupID: string(res.BackupID),
		Source:   sourceID,
		Kind:     res.Manifest.Kind,
		Dest:     destName,
		Prefix:   res.Prefix,
		Bytes:    totalBytes(res.Manifest),
		Seconds:  res.Manifest.FinishedAt.Sub(res.Manifest.StartedAt).Seconds(),
	}
	a.emit(out, func(p *printer) {
		p.printf("%s  %s  %s  %s in %.1fs\n",
			out.BackupID, out.Source, out.Kind, humanBytes(out.Bytes), out.Seconds)
	})
	return nil
}

func (a *app) doBackup(
	ctx context.Context,
	cfg config.Config,
	sourceID string,
	src config.Source,
	destName string,
	dest config.Destination,
	opt backupOptions,
) (backup.Result, error) {
	ex, err := executorFor(ctx, src)
	if err != nil {
		return backup.Result{}, err
	}
	defer func() { _ = ex.Close() }()

	drv, err := sourceFor(src, ex)
	if err != nil {
		return backup.Result{}, err
	}

	st, err := openStorage(ctx, dest)
	if err != nil {
		return backup.Result{}, err
	}
	cat, err := openCatalog(ctx, cfg)
	if err != nil {
		return backup.Result{}, err
	}
	defer func() { _ = cat.Close() }()

	sealer, err := sealerFor(cfg)
	if err != nil {
		return backup.Result{}, err
	}

	a.printf("backing up %s to %s...", sourceID, destName)
	runner := &backup.Runner{
		Storage:        st,
		Catalog:        cat,
		Sealer:         sealer,
		Now:            time.Now,
		NewID:          func() catalog.ID { return catalog.ID(ulid.Make().String()) },
		KoffrVersion:   version.Value,
		RepositoryName: repositoryName(dest),
		Holder:         hostname(),
	}
	return runner.Run(ctx, backup.Request{
		SourceID:    sourceID,
		Source:      drv,
		Executor:    ex,
		Backup:      opt.request,
		Destination: destName,
	})
}

// ---------------------------------------------------------------- ls

func (a *app) lsCmd() *cobra.Command {
	var (
		sourceID string
		limit    int
	)
	c := &cobra.Command{
		Use:   "ls",
		Short: "List backups",
		Long: "List what the catalog knows about, newest first.\n\n" +
			"The catalog is a cache, not the truth: the repository is. A backup missing\n" +
			"here but present there is still restorable, which is why identifiers sort\n" +
			"by time and prefixes are printed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.begin(cmd); err != nil {
				return err
			}
			return a.runLs(cmd.Context(), sourceID, limit)
		},
	}
	c.Flags().StringVar(&sourceID, "source", "", "only this source")
	c.Flags().IntVar(&limit, "limit", 50, "how many to show")
	return c
}

type backupLine struct {
	ID          string `json:"backup_id"`
	Source      string `json:"source"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	Destination string `json:"destination"`
	StartedAt   string `json:"started_at"`
	Bytes       int64  `json:"bytes"`
}

func (a *app) runLs(ctx context.Context, sourceID string, limit int) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	if sourceID != "" {
		if _, err := a.source(cfg, sourceID); err != nil {
			return err
		}
	}
	cat, err := openCatalog(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = cat.Close() }()

	backups, err := cat.ListBackups(ctx, catalog.BackupFilter{SourceID: sourceID, Limit: limit})
	if err != nil {
		return err
	}

	lines := make([]backupLine, 0, len(backups))
	for _, b := range backups {
		lines = append(lines, backupLine{
			ID: string(b.ID), Source: b.SourceID, Kind: b.Kind,
			Status: string(b.Status), Destination: b.Destination,
			StartedAt: b.StartedAt.UTC().Format(time.RFC3339), Bytes: b.SizeBytes,
		})
	}
	a.emit(struct {
		Backups []backupLine `json:"backups"`
	}{lines}, func(p *printer) {
		if len(lines) == 0 {
			p.printf("no backups\n")
			return
		}
		p.table(func(p *printer) {
			p.printf("BACKUP ID\tSOURCE\tKIND\tSTATUS\tSTARTED\tSIZE\n")
			for _, l := range lines {
				p.printf("%s\t%s\t%s\t%s\t%s\t%s\n",
					l.ID, l.Source, l.Kind, l.Status, l.StartedAt, humanBytes(l.Bytes))
			}
		})
	})
	return nil
}

// ---------------------------------------------------------------- show

func (a *app) showCmd() *cobra.Command {
	var sourceID string
	c := &cobra.Command{
		Use:   "show <backup-id>",
		Short: "Show one backup's manifest",
		Long: "Show what a backup contains: its objects, their digests, and who it is\n" +
			"encrypted for.\n\n" +
			"The manifest is plaintext by design, so this works without a key. The\n" +
			"database and table names are not: they live in the encrypted sidecar and\n" +
			"appear only when the identity can read it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.begin(cmd); err != nil {
				return err
			}
			return a.runShow(cmd.Context(), args[0], sourceID)
		},
	}
	c.Flags().StringVar(&sourceID, "source", "", "source the backup belongs to (needed if several are configured)")
	return c
}

func (a *app) runShow(ctx context.Context, backupID, sourceID string) error {
	found, err := a.locate(ctx, backupID, sourceID)
	if err != nil {
		return err
	}

	a.emit(struct {
		Manifest manifest.Manifest `json:"manifest"`
		Prefix   string            `json:"prefix"`
	}{found.manifest, found.backup.Prefix()}, func(p *printer) {
		m := found.manifest
		p.printf("%s  %s  %s %s  %s\n", m.BackupID, m.SourceID, m.Engine, m.ServerVersion, m.Kind)
		p.printf("taken   %s to %s\n",
			m.StartedAt.UTC().Format(time.RFC3339), m.FinishedAt.UTC().Format(time.RFC3339))
		p.printf("prefix  %s\n", found.backup.Prefix())
		p.printf("tool    %s %s\n", m.Tool.Name, m.Tool.Version)
		p.printf("objects\n")
		p.table(func(p *printer) {
			for _, o := range m.Objects {
				p.printf("  %s\t%s\t%s\t%s\n",
					baseName(o.Key), humanBytes(o.SizeBytes), o.Codec, shortSum(o.SHA256))
			}
		})
	})
	return nil
}

// ---------------------------------------------------------------- fetch

func (a *app) fetchCmd() *cobra.Command {
	var (
		sourceID string
		object   string
		outDir   string
		raw      bool
	)
	c := &cobra.Command{
		Use:   "fetch <backup-id>",
		Short: "Write a backup's objects out as files",
		Long: "Decrypt a backup's objects and write them to disk, ready for the database's\n" +
			"own tools.\n\n" +
			"This is the escape hatch, and it is meant to be used: pg_restore --jobs\n" +
			"needs an archive it can seek, and so does anyone who would rather drive the\n" +
			"restore themselves. What lands on disk is exactly what RESTORE.md describes.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.begin(cmd); err != nil {
				return err
			}
			return a.runFetch(cmd.Context(), args[0], sourceID, object, outDir, raw)
		},
	}
	c.Flags().StringVar(&sourceID, "source", "", "source the backup belongs to")
	c.Flags().StringVar(&object, "object", "", "fetch only this object (default: all of them)")
	c.Flags().StringVar(&outDir, "into", ".", "directory to write into")
	c.Flags().BoolVar(&raw, "raw", false,
		"stop after decryption, leaving compression in place, as `age -d` would")
	return c
}

type fetchedFile struct {
	Object string `json:"object"`
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
}

func (a *app) runFetch(ctx context.Context, backupID, sourceID, only, outDir string, raw bool) error {
	found, err := a.locate(ctx, backupID, sourceID)
	if err != nil {
		return err
	}

	opener, err := openerFor(found.cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return fmt.Errorf("fetch: %w", err)
	}

	fetcher := restore.Fetcher{Storage: found.storage, Opener: opener}
	var written []fetchedFile
	for _, o := range found.manifest.Objects {
		name := baseName(o.Key)
		if only != "" && name != only && strings.TrimSuffix(strings.TrimSuffix(name, ".age"), ".zst") != only {
			continue
		}
		outName := strings.TrimSuffix(name, ".age")
		if !raw {
			outName = strings.TrimSuffix(outName, ".zst")
		}
		path := filepath.Join(outDir, outName)

		n, err := a.fetchOne(ctx, fetcher, found.backup.Prefix(), o, path, raw)
		if err != nil {
			return classifyRepository(err)
		}
		written = append(written, fetchedFile{Object: name, Path: path, Bytes: n})
	}
	if len(written) == 0 {
		return fault(ExitUsage, "backup %s has no object named %q", backupID, only)
	}

	a.emit(struct {
		Files []fetchedFile `json:"files"`
	}{written}, func(p *printer) {
		for _, f := range written {
			p.printf("%s  %s\n", f.Path, humanBytes(f.Bytes))
		}
	})
	return nil
}

// fetchOne writes one object, through a temporary name.
//
// The digest can only be checked once the last byte has been read, so the file
// is renamed into place afterwards: a fetch that failed halfway must not leave
// something that looks like a dump.
func (a *app) fetchOne(
	ctx context.Context, f restore.Fetcher, prefix string, o manifest.Object, path string, raw bool,
) (int64, error) {
	tmp := path + ".partial"
	//nolint:gosec // the path is where the operator asked the backup to land
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}
	a.printf("fetching %s...", baseName(o.Key))

	n, err := f.Object(ctx, prefix, o, file, restore.FetchOptions{Raw: raw})
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}
	return n, nil
}

// ---------------------------------------------------------------- restore

func (a *app) restoreCmd() *cobra.Command {
	var (
		sourceID string
		into     string
		create   bool
		noOwner  bool
		globals  bool
		jobs     int
	)
	c := &cobra.Command{
		Use:   "restore <backup-id>",
		Short: "Restore a backup into a database",
		Long: "Restore a backup into a database, streaming it straight from the\n" +
			"repository: nothing is staged on disk.\n\n" +
			"The target is always named explicitly. The source's own database name is in\n" +
			"the encrypted details, and restoring into whatever the dump was called is\n" +
			"how a test restore lands on production.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.begin(cmd); err != nil {
				return err
			}
			if into == "" {
				return fault(ExitUsage, "--into is required: name the database to restore into")
			}
			return a.runRestore(cmd.Context(), args[0], restoreOptions{
				sourceID: sourceID, into: into, create: create,
				noOwner: noOwner, globals: globals, jobs: jobs,
			})
		},
	}
	c.Flags().StringVar(&sourceID, "source", "", "source the backup belongs to")
	c.Flags().StringVar(&into, "into", "", "database to restore into (required)")
	c.Flags().BoolVar(&create, "create", false, "create the target database first; fails if it exists")
	c.Flags().BoolVar(&noOwner, "no-owner", false, "restore without reassigning ownership")
	c.Flags().BoolVar(&globals, "with-globals", false, "replay roles and tablespaces before the dump")
	c.Flags().IntVar(&jobs, "jobs", 0, "parallel restore workers; needs an archive on disk, see `koffr fetch`")
	return c
}

type restoreOptions struct {
	sourceID string
	into     string
	create   bool
	noOwner  bool
	globals  bool
	jobs     int
}

func (a *app) runRestore(ctx context.Context, backupID string, opt restoreOptions) error {
	found, err := a.locate(ctx, backupID, opt.sourceID)
	if err != nil {
		return err
	}

	res, err := a.doRestore(ctx, found, opt)
	if err != nil {
		var f *Fault
		if errorsAs(err, &f) {
			return err
		}
		return &Fault{Code: ExitRestore, Err: err}
	}

	out := struct {
		BackupID string   `json:"backup_id"`
		Database string   `json:"database"`
		Warnings []string `json:"warnings,omitempty"`
	}{backupID, opt.into, res.Warnings}
	a.emit(out, func(p *printer) {
		p.printf("restored %s into %s\n", out.BackupID, out.Database)
		for _, warn := range out.Warnings {
			p.printf("warning: %s\n", warn)
		}
	})
	return nil
}

func (a *app) doRestore(ctx context.Context, found *located, opt restoreOptions) (restore.PostgresResult, error) {
	var zero restore.PostgresResult

	src, err := a.source(found.cfg, found.sourceID)
	if err != nil {
		return zero, err
	}
	if src.Engine != "postgresql" {
		return zero, fmt.Errorf("restoring a %s backup is not supported yet", src.Engine)
	}
	opener, err := openerFor(found.cfg)
	if err != nil {
		return zero, err
	}
	ex, err := executorFor(ctx, src)
	if err != nil {
		return zero, err
	}
	defer func() { _ = ex.Close() }()

	fetcher := restore.Fetcher{Storage: found.storage, Opener: opener}
	dump, ok := objectNamed(found.manifest, ".pgdump")
	if !ok {
		return zero, fmt.Errorf("backup %s holds no pg_dump archive", found.manifest.BackupID)
	}

	// Both streams are pipes, so the objects are never written to disk: the
	// bytes go storage -> age -> zstd -> pg_restore and nowhere else (ENF-001).
	prefix := found.backup.Prefix()
	dumpR, dumpResult := a.pipeObject(ctx, fetcher, prefix, dump)
	fetchFailed := dumpResult

	req := restore.PostgresRequest{
		Database: opt.into,
		Create:   opt.create,
		NoOwner:  opt.noOwner,
		Jobs:     opt.jobs,
		Dump:     dumpR,
	}
	if opt.globals {
		if g, ok := objectNamed(found.manifest, "globals.sql"); ok {
			globalsR, globalsResult := a.pipeObject(ctx, fetcher, prefix, g)
			req.Globals = globalsR
			fetchFailed = func() error {
				if err := globalsResult(); err != nil {
					return err
				}
				return dumpResult()
			}
		}
	}

	a.printf("restoring %s into %s...", found.manifest.BackupID, opt.into)
	driver := restore.Postgres{Config: postgresConfig(src, localToolRunner())}
	res, err := driver.Restore(ctx, ex, req)

	// The repository's failure outranks the tool's reaction to it: pg_restore
	// complaining about a short archive is a symptom, and the missing or
	// damaged object is the cause.
	if fetchErr := fetchFailed(); fetchErr != nil {
		return res, classifyRepository(fetchErr)
	}
	return res, err
}

// pipeObject streams one object into a pipe the caller reads, and returns a
// function that stops the streaming and reports how it went.
//
// Both halves matter. CloseWithError stops the tool as soon as the repository
// fails, and the wait function surfaces *why*: pg_restore given a stream that
// ended early reports "input file is too short", which is true, useless, and
// says nothing about the object that could not be read. The caller prefers this
// error over the tool's.
//
// The stop is not optional. pg_restore stops reading at the archive's end
// marker -- the same behaviour that makes zstd exit 141 in RESTORE.md (P-006)
// -- so on a perfectly successful restore this goroutine is still blocked
// writing into a pipe nobody will read again. Waiting for it without closing
// the reader first hangs the command forever, which is exactly what it did.
//
// A reader that went away is therefore not a failure, and the returned error is
// nil for it. The cost is that the digest is not verified on a restore the tool
// cut short: streaming into a database and proving the whole object was intact
// are two things that cannot both happen in one pass. `koffr fetch` does verify,
// because it reads to the end.
func (a *app) pipeObject(
	ctx context.Context, f restore.Fetcher, prefix string, o manifest.Object,
) (io.Reader, func() error) {
	pr, pw := io.Pipe()
	done := make(chan struct{})
	var fetchErr error
	go func() {
		defer close(done)
		_, fetchErr = f.Object(ctx, prefix, o, pw, restore.FetchOptions{})
		_ = pw.CloseWithError(fetchErr)
	}()

	return pr, func() error {
		_ = pr.CloseWithError(errReaderGone)
		<-done
		if errors.Is(fetchErr, errReaderGone) || errors.Is(fetchErr, io.ErrClosedPipe) {
			return nil
		}
		return fetchErr
	}
}

// errReaderGone marks the pipe being closed from the reading end because the
// tool has finished with it.
var errReaderGone = errors.New("the restore stopped reading")

// ---------------------------------------------------------------- lookup

// located is a backup found in a repository, with everything open around it.
type located struct {
	cfg      config.Config
	sourceID string
	backup   storage.Backup
	storage  storage.Storage
	manifest manifest.Manifest
}

// locate finds a backup by id.
//
// It asks the catalog first because that is the fast path, and falls back to
// the repository because the catalog is a cache: a backup the catalog lost is
// still restorable, and a tool that cannot find it is the tool failing, not the
// backup.
func (a *app) locate(ctx context.Context, backupID, sourceID string) (*located, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	if sourceID == "" {
		sourceID, err = a.sourceOfBackup(ctx, cfg, backupID)
		if err != nil {
			return nil, err
		}
	}
	src, err := a.source(cfg, sourceID)
	if err != nil {
		return nil, err
	}
	_, dest, err := a.destinationFor(cfg, src, "")
	if err != nil {
		return nil, err
	}
	st, err := openStorage(ctx, dest)
	if err != nil {
		return nil, err
	}

	layoutSource, err := storage.ForSource(sourceID)
	if err != nil {
		return nil, fault(ExitUsage, "%v", err)
	}
	b, err := layoutSource.Backup(storage.DirLogical, backupID)
	if err != nil {
		return nil, fault(ExitUsage, "%v", err)
	}

	m, err := restore.Fetcher{Storage: st}.Manifest(ctx, b)
	if err != nil {
		return nil, fmt.Errorf("no backup %s under %s: %w", backupID, layoutSource.Prefix(), err)
	}
	return &located{
		cfg: cfg, sourceID: sourceID, backup: b, storage: st, manifest: m,
	}, nil
}

// sourceOfBackup asks the catalog which source a backup belongs to, and when
// there is only one configured, answers without asking.
func (a *app) sourceOfBackup(ctx context.Context, cfg config.Config, backupID string) (string, error) {
	ids := cfg.SourceIDs()
	if len(ids) == 1 {
		return ids[0], nil
	}
	cat, err := openCatalog(ctx, cfg)
	if err != nil {
		return "", err
	}
	defer func() { _ = cat.Close() }()

	backups, err := cat.ListBackups(ctx, catalog.BackupFilter{})
	if err != nil {
		return "", err
	}
	for _, b := range backups {
		if string(b.ID) == backupID {
			return b.SourceID, nil
		}
	}
	return "", fault(ExitUsage,
		"the catalog does not know backup %s; name its source with --source", backupID)
}

func objectNamed(m manifest.Manifest, suffix string) (manifest.Object, bool) {
	for _, o := range m.Objects {
		name := strings.TrimSuffix(strings.TrimSuffix(o.Key, ".age"), ".zst")
		if strings.HasSuffix(name, suffix) {
			return o, true
		}
	}
	return manifest.Object{}, false
}

func totalBytes(m manifest.Manifest) int64 {
	var n int64
	for _, o := range m.Objects {
		n += o.SizeBytes
	}
	return n
}

func baseName(key string) string {
	if i := strings.LastIndex(key, "/"); i >= 0 {
		return key[i+1:]
	}
	return key
}

func repositoryName(dest config.Destination) string {
	if dest.Type == "s3" {
		return "s3://" + dest.Bucket + "/" + strings.TrimPrefix(dest.Prefix, "/")
	}
	return dest.Path
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

// localToolRunner is where pg_dump and pg_restore run: always this machine.
//
// That is PD-002 and PD-003 in one line. The client binaries pull over the
// network, so the database host gets nothing installed and nothing written to
// its disk, and an SSH executor is a way to reach the port, not a place to run
// things.
func localToolRunner() executor.Executor { return local.New() }

// errorsAs is errors.As, named so the intent at the call site is "was this
// already classified" rather than a bare type assertion.
func errorsAs(err error, target **Fault) bool { return errors.As(err, target) }

// shortSum abbreviates a digest for a listing. The whole one is in the manifest
// and in RESTORE.md, where it is meant to be compared rather than read.
func shortSum(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}

// classifyRepository turns a damaged object into exit code 5.
//
// "A backup exists but did not verify" is exactly what a digest mismatch is,
// and it is worth its own code: a missing backup is a gap in the schedule, a
// backup that does not match its manifest is a gap someone has been trusting.
func classifyRepository(err error) error {
	if errors.Is(err, restore.ErrDigestMismatch) {
		return &Fault{Code: ExitVerify, Err: err}
	}
	return err
}
