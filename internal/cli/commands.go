package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Gu1llaum-3/koffr/internal/backup"
	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/catalog/replica"
	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/executor/local"
	"github.com/Gu1llaum-3/koffr/internal/manifest"
	"github.com/Gu1llaum-3/koffr/internal/notify"
	"github.com/Gu1llaum-3/koffr/internal/pipeline"
	"github.com/Gu1llaum-3/koffr/internal/restore"
	"github.com/Gu1llaum-3/koffr/internal/scheduler"
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

// backupOnce runs one backup and returns its result, classified.
//
// Separate from runBackup because the scheduler needs the work without the
// reporting: a command's rendered answer belongs to the command, and a
// scheduler that inherited one would report the last backup as its own.
func (a *app) backupOnce(
	ctx context.Context, sourceID string, opt backupOptions,
) (backup.Result, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return backup.Result{}, err
	}
	src, err := a.source(cfg, sourceID)
	if err != nil {
		return backup.Result{}, err
	}
	destName, dest, err := a.destinationFor(cfg, src, opt.destination)
	if err != nil {
		return backup.Result{}, err
	}

	// Everything from here on is the attempt itself, so every failure is a
	// backup failure: the code that pages someone.
	res, err := a.doBackup(ctx, cfg, sourceID, src, destName, dest, opt)
	if err != nil {
		var f *Fault
		if errorsAs(err, &f) {
			return backup.Result{}, err
		}
		return backup.Result{}, &Fault{Code: ExitBackup, Err: err}
	}
	return res, nil
}

func (a *app) runBackup(ctx context.Context, sourceID string, opt backupOptions) error {
	res, err := a.backupOnce(ctx, sourceID, opt)
	if err != nil {
		return err
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
		Dest:     res.Destination,
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
	// Every destination after the first is a copy of the finished backup
	// (EF-044). Opened before the job starts, so a destination that cannot be
	// reached is found now rather than after the database has been read.
	mirrors, err := a.openMirrors(ctx, cfg, src, destName)
	if err != nil {
		return backup.Result{}, err
	}

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
		Mirrors:     mirrors,
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
	var sourceID, from string
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
			return a.runShow(cmd.Context(), args[0], sourceID, from)
		},
	}
	c.Flags().StringVar(&sourceID, "source", "", "source the backup belongs to (needed if several are configured)")
	c.Flags().StringVar(&from, "from", "", fromFlagHelp)
	return c
}

func (a *app) runShow(ctx context.Context, backupID, sourceID, from string) error {
	found, err := a.locate(ctx, backupID, sourceID, from)
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
		from     string
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
			return a.runFetch(cmd.Context(), args[0], sourceID, from, object, outDir, raw)
		},
	}
	c.Flags().StringVar(&sourceID, "source", "", "source the backup belongs to")
	c.Flags().StringVar(&from, "from", "", fromFlagHelp)
	c.Flags().StringVar(&object, "object", "", "fetch only this object (default: all of them)")
	c.Flags().StringVar(&outDir, "into", ".", "directory to write into, or - for stdout")
	c.Flags().BoolVar(&raw, "raw", false,
		"stop after decryption, leaving compression in place, as `age -d` would")
	return c
}

type fetchedFile struct {
	Object string `json:"object"`
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
}

func (a *app) runFetch(ctx context.Context, backupID, sourceID, from, only, outDir string, raw bool) error {
	found, err := a.locate(ctx, backupID, sourceID, from)
	if err != nil {
		return err
	}

	opener, err := openerFor(found.cfg)
	if err != nil {
		return err
	}
	fetcher := restore.Fetcher{Storage: found.storage, Opener: opener}

	// EF-083: `--into -` sends the artifact to stdout, so it can be piped
	// straight into pg_restore without ever landing on a disk. Exactly one
	// object then, because a pipe has no filenames to separate them by.
	if outDir == "-" {
		if a.format == formatJSON {
			return fault(ExitUsage,
				"--into - puts the artifact on stdout, so there is no room for --output json there")
		}
		return a.fetchToStdout(ctx, fetcher, found, only, raw)
	}

	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
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
		from     string
		into     string
		target   string
		create   bool
		noOwner  bool
		globals  bool
		force    bool
		yes      bool
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
				sourceID: sourceID, from: from, into: into, target: target, create: create,
				noOwner: noOwner, globals: globals, jobs: jobs,
				force: force, yes: yes,
			})
		},
	}
	c.Flags().StringVar(&sourceID, "source", "", "source the backup belongs to")
	c.Flags().StringVar(&from, "from", "", fromFlagHelp)
	c.Flags().StringVar(&into, "into", "", "database to restore into (required)")
	c.Flags().BoolVar(&create, "create", false, "create the target database first; fails if it exists")
	c.Flags().BoolVar(&noOwner, "no-owner", false, "restore without reassigning ownership")
	c.Flags().BoolVar(&globals, "with-globals", false, "replay roles and tablespaces before the dump")
	c.Flags().IntVar(&jobs, "jobs", 0, "parallel restore workers; needs an archive on disk, see `koffr fetch`")
	c.Flags().StringVar(&target, "target", "",
		"restore into this configured source's server instead of the backup's own (EF-080)")
	c.Flags().BoolVar(&force, "force", false,
		"restore into a database that already holds tables, merging the two")
	c.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt, for scripts and schedules")
	return c
}

type restoreOptions struct {
	sourceID string
	// from names the destination to read from. Empty means wherever it is.
	from string
	into string
	// target names another configured source whose server receives the
	// restore. It is a source id rather than a host and a password because a
	// credential on a command line is visible in ps (ENF-021), and because the
	// configuration stays the thing that says what exists (PD-005).
	target  string
	create  bool
	noOwner bool
	globals bool
	force   bool
	yes     bool
	jobs    int
}

func (a *app) runRestore(ctx context.Context, backupID string, opt restoreOptions) error {
	found, err := a.locate(ctx, backupID, opt.sourceID, opt.from)
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

	targetID := found.sourceID
	if opt.target != "" {
		targetID = opt.target
	}
	src, err := a.source(found.cfg, targetID)
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

	// EF-085. A restore is the one command that destroys data, and the
	// destination is a flag away from being the wrong one. Both guards are
	// checked before a single byte moves.
	if err := a.confirmRestore(ctx, src, targetID, found.manifest.BackupID, opt, ex); err != nil {
		return zero, err
	}

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
	// destination is where it was actually found, which is not always the
	// first one configured.
	destination string
	backup      storage.Backup
	storage     storage.Storage
	manifest    manifest.Manifest
}

// locate finds a backup by id, on whichever destination holds it.
//
// Not the first destination: with a second copy kept longer than the first
// (EF-044), everything past the local retention window lives only offsite, and
// looking in one place would make half the backups unreachable. That is exactly
// what happened before this, and the tell was that verifying it needed the
// configuration reordered by hand.
//
// The catalog is asked which destinations hold it, because the catalog now
// knows -- one row per copy. When it does not know, every configured
// destination is tried in order: the catalog is a cache (ADR-0004), and a
// backup it has forgotten is still restorable.
func (a *app) locate(ctx context.Context, backupID, sourceID, from string) (*located, error) {
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

	layoutSource, err := storage.ForSource(sourceID)
	if err != nil {
		return nil, fault(ExitUsage, "%v", err)
	}
	b, err := layoutSource.Backup(storage.DirLogical, backupID)
	if err != nil {
		return nil, fault(ExitUsage, "%v", err)
	}

	candidates, err := a.destinationsHolding(ctx, cfg, src, backupID, from)
	if err != nil {
		return nil, err
	}

	var tried []string
	for _, name := range candidates {
		dest, known := cfg.Destinations[name]
		if !known {
			return nil, fault(ExitUsage, "no destination %q in %s", name, cfg.Path())
		}
		st, err := openStorage(ctx, dest)
		if err != nil {
			// A destination that cannot be opened is not a destination that
			// lacks the backup: say so and move on, so one broken endpoint
			// does not hide a copy sitting on another.
			tried = append(tried, fmt.Sprintf("%s (unreachable)", name))
			continue
		}
		m, err := restore.Fetcher{Storage: st}.Manifest(ctx, b)
		if err != nil {
			tried = append(tried, name)
			continue
		}
		return &located{
			cfg: cfg, sourceID: sourceID, destination: name,
			backup: b, storage: st, manifest: m,
		}, nil
	}

	return nil, fmt.Errorf("no backup %s under %s on %s",
		backupID, layoutSource.Prefix(), strings.Join(tried, ", "))
}

// destinationsHolding decides where to look, in order.
func (a *app) destinationsHolding(
	ctx context.Context, cfg config.Config, src config.Source, backupID, from string,
) ([]string, error) {
	if from != "" {
		if !slices.Contains(src.Destinations, from) {
			return nil, fault(ExitUsage,
				"this source does not write to %q; it writes to %s",
				from, strings.Join(src.Destinations, ", "))
		}
		return []string{from}, nil
	}

	// What the catalog knows, kept in the configuration's order so the fastest
	// destination is tried first.
	known := a.catalogDestinations(ctx, cfg, backupID)
	var ordered []string
	for _, name := range src.Destinations {
		if slices.Contains(known, name) {
			ordered = append(ordered, name)
		}
	}
	if len(ordered) > 0 {
		return ordered, nil
	}

	// The catalog knows nothing -- lost, never rebuilt, or this backup predates
	// it. Every destination is a candidate, because the repository is the
	// truth.
	return src.Destinations, nil
}

// catalogDestinations lists the destinations the catalog records for a backup.
//
// A failure here is not an error: the catalog is a cache, and being unable to
// consult it means looking everywhere rather than giving up.
func (a *app) catalogDestinations(ctx context.Context, cfg config.Config, backupID string) []string {
	cat, err := openCatalog(ctx, cfg)
	if err != nil {
		return nil
	}
	defer func() { _ = cat.Close() }()

	backups, err := cat.ListBackups(ctx, catalog.BackupFilter{})
	if err != nil {
		return nil
	}
	var out []string
	for _, b := range backups {
		if string(b.ID) == backupID && b.Destination != "" {
			out = append(out, b.Destination)
		}
	}
	return out
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

// ---------------------------------------------------------------- catalog

func (a *app) catalogCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "catalog",
		Short: "Inspect and repair the catalog",
		Long: "The catalog is a cache. The repository is the truth.\n\n" +
			"Everything under this command exists so that losing the machine Koffr runs\n" +
			"on loses nothing that matters.",
	}
	c.AddCommand(a.catalogSyncCmd())
	return c
}

func (a *app) catalogSyncCmd() *cobra.Command {
	var (
		destination   string
		fromManifests bool
	)
	c := &cobra.Command{
		Use:   "sync",
		Short: "Rebuild the catalog from the repository",
		Long: "Rebuild the catalog from what the repository holds (EF-142).\n\n" +
			"Two levels, tried in that order:\n\n" +
			"  1. The replicated catalog. Complete, including the record of jobs that\n" +
			"     failed -- which produce no manifest and exist nowhere else. Needs the\n" +
			"     configured identity.\n" +
			"  2. The plaintext manifests. Backups only, no job history, but needs no key\n" +
			"     and no prior state at all. This is the level that works when everything\n" +
			"     else is gone, and it is why manifests are not encrypted.\n\n" +
			"The rebuild merges: rows already here are refreshed, rows recorded since the\n" +
			"last replication are kept. Running it twice changes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.begin(cmd); err != nil {
				return err
			}
			return a.runCatalogSync(cmd.Context(), destination, fromManifests)
		},
	}
	c.Flags().StringVar(&destination, "destination", "", "repository to rebuild from")
	c.Flags().BoolVar(&fromManifests, "from-manifests", false,
		"skip the replicated catalog and walk the manifests, which needs no key")
	return c
}

func (a *app) runCatalogSync(ctx context.Context, destination string, fromManifests bool) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	name, dest, err := a.anyDestination(cfg, destination)
	if err != nil {
		return err
	}
	st, err := openStorage(ctx, dest)
	if err != nil {
		return err
	}
	cat, err := openCatalog(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = cat.Close() }()

	before, err := cat.Export(ctx)
	if err != nil {
		return err
	}

	snap, source, skipped, err := a.recover(ctx, cfg, st, name, fromManifests)
	if err != nil {
		return err
	}
	if err := cat.Import(ctx, snap); err != nil {
		return err
	}
	after, err := cat.Export(ctx)
	if err != nil {
		return err
	}

	out := struct {
		Destination string   `json:"destination"`
		Source      string   `json:"source"`
		Backups     int      `json:"backups"`
		Jobs        int      `json:"jobs"`
		Added       int      `json:"added"`
		Skipped     []string `json:"skipped,omitempty"`
	}{
		Destination: name, Source: source,
		Backups: len(after.Backups), Jobs: len(after.Jobs),
		Added:   len(after.Backups) - len(before.Backups),
		Skipped: skipped,
	}
	a.emit(out, func(p *printer) {
		p.printf("rebuilt from %s (%s): %d backups, %d jobs, %d new\n",
			out.Destination, out.Source, out.Backups, out.Jobs, out.Added)
		for _, s := range out.Skipped {
			p.printf("skipped: %s\n", s)
		}
	})
	return nil
}

// recover picks a rebuild level and says which one it used.
//
// The replicated catalog first, because it is the only one carrying the job
// history. Falling back is announced rather than silent: "rebuilt from
// manifests" and "rebuilt from the replica" leave the operator with different
// catalogs, and they need to know which one they have.
func (a *app) recover(
	ctx context.Context, cfg config.Config, st storage.Storage, destName string, fromManifests bool,
) (catalog.Snapshot, string, []string, error) {
	if !fromManifests {
		opener, err := openerFor(cfg)
		if err == nil {
			snap, readErr := replica.Read(ctx, st, opener)
			switch {
			case readErr == nil:
				return snap, "replicated catalog", nil, nil
			case errors.Is(readErr, storage.ErrNotFound):
				a.printf("no replicated catalog in %s; rebuilding from the manifests", destName)
			default:
				// Not fatal. The manifests are still there, and a partial
				// rebuild beats refusing to rebuild at all.
				a.printf("the replicated catalog in %s could not be read (%v); "+
					"rebuilding from the manifests", destName, readErr)
			}
		} else {
			a.printf("no identity configured; rebuilding from the plaintext manifests")
		}
	}

	rebuilt, err := replica.RebuildFromManifests(ctx, st)
	if err != nil {
		return catalog.Snapshot{}, "", nil, err
	}

	// The destination name lives in the configuration, not in the repository:
	// the same bucket is "main" in one file and "offsite" in another. It is
	// filled in here, where a name exists, rather than invented by the rebuild.
	for i := range rebuilt.Backups {
		rebuilt.Backups[i].Destination = destName
	}
	return rebuilt.Snapshot, "manifests", rebuilt.Skipped, nil
}

// anyDestination resolves a destination without needing a source, because a
// rebuild is exactly the situation where the catalog cannot say which source
// wrote what.
func (a *app) anyDestination(cfg config.Config, want string) (string, config.Destination, error) {
	if want != "" {
		dest, ok := cfg.Destinations[want]
		if !ok {
			return "", config.Destination{}, fault(ExitUsage, "no destination %q in %s", want, cfg.Path())
		}
		return want, dest, nil
	}
	names := sortedKeys(cfg.Destinations)
	if len(names) != 1 {
		return "", config.Destination{}, fault(ExitUsage,
			"%s defines %d destinations (%s); name one with --destination",
			cfg.Path(), len(names), strings.Join(names, ", "))
	}
	return names[0], cfg.Destinations[names[0]], nil
}

// confirmRestore is EF-085: nothing is overwritten without being asked, and
// nothing is merged into a populated database by accident.
//
// Two separate guards, because they catch different mistakes. The populated
// check catches restoring into the wrong database; the confirmation catches
// restoring the wrong backup, or onto the wrong server, which no amount of
// inspection can detect for the operator.
func (a *app) confirmRestore(
	ctx context.Context, src config.Source, targetID, backupID string,
	opt restoreOptions, ex executor.Executor,
) error {
	if !opt.force {
		populated, err := targetHoldsData(ctx, src, opt.into, ex)
		if err != nil {
			return err
		}
		if populated {
			return fault(ExitUsage,
				"database %q on %s already holds tables; restoring into it merges two datasets. "+
					"Restore into an empty database, or pass --force if merging is what you meant",
				opt.into, targetID)
		}
	}
	if opt.yes {
		return nil
	}

	where := fmt.Sprintf("%s on %s (%s)", opt.into, targetID, src.Host)
	prompt := fmt.Sprintf("Restore backup %s into %s? [y/N] ", backupID, where)

	answer, ok := a.ask(prompt)
	if !ok {
		// No terminal and no --yes. Refusing is the only safe reading: a
		// scheduled job that meant to restore says so with a flag, and one that
		// did not should not have a prompt answered for it by an empty pipe.
		return fault(ExitUsage,
			"a restore needs confirmation and there is no terminal to ask on; pass --yes to proceed")
	}
	if answer != "y" && answer != "yes" {
		return fault(ExitUsage, "restore into %s cancelled", where)
	}
	return nil
}

// ask puts a question on stderr and reads the answer. It reports false when
// there is nobody to ask, which is not the same as an answer of "no": the
// caller decides what silence means.
//
// Whether anyone is there is settled before the question is printed. Asking
// into a closed pipe and then refusing would put a question in the log that
// nothing could ever have answered.
func (a *app) ask(prompt string) (string, bool) {
	if !a.interactive() {
		return "", false
	}
	// The prompt goes to stderr so stdout stays the answer. Its write error is
	// discarded: if stderr is gone there is nowhere to report that.
	_, _ = fmt.Fprint(a.streams.err(), prompt)

	reader := bufio.NewReader(a.streams.In)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(line)), true
}

// interactive reports whether there is a person at the other end.
//
// A file or a pipe on stdin is a script, whatever it contains. Tests inject a
// plain reader, which is treated as interactive on purpose: a test that had to
// allocate a pty to check a prompt would not be written.
func (a *app) interactive() bool {
	if a.streams.In == nil || a.format == formatJSON {
		return false
	}
	f, isFile := a.streams.In.(*os.File)
	if !isFile {
		return true
	}
	// term.IsTerminal rather than inspecting the file mode: /dev/null is a
	// character device too, so a mode check calls `koffr restore < /dev/null`
	// interactive and prints a question nothing can answer.
	return term.IsTerminal(int(f.Fd()))
}

// targetHoldsData reports whether the database already has user tables.
//
// A database that does not exist holds nothing, and --create is how it comes
// into being: failing here would refuse the ordinary case.
func targetHoldsData(ctx context.Context, src config.Source, database string, ex executor.Executor) (bool, error) {
	probe := postgresConfig(src, localToolRunner())
	probe.Database = database

	conn, err := probe.Connect(ctx, ex)
	if err != nil {
		// Cannot connect, so there is nothing to overwrite yet. The restore
		// itself will report the real problem in a moment, with better words.
		return false, nil //nolint:nilerr // an unreachable database holds no data to lose
	}
	defer func() { _ = conn.Close(ctx) }()

	var n int
	const q = `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
	           WHERE c.relkind IN ('r','p','m') AND n.nspname NOT IN ('pg_catalog','information_schema')`
	if err := conn.QueryRow(ctx, q).Scan(&n); err != nil {
		return false, fmt.Errorf("restore: check whether %s is empty: %w", database, err)
	}
	return n > 0, nil
}

// fetchToStdout streams one object out, for piping.
//
// Nothing else may reach stdout on this path, so progress and the summary go to
// stderr: `koffr fetch ... --into - | pg_restore` puts the artifact in the pipe
// and a stray line of prose would corrupt it.
func (a *app) fetchToStdout(
	ctx context.Context, f restore.Fetcher, found *located, only string, raw bool,
) error {
	obj, err := soleObject(found.manifest, only)
	if err != nil {
		return err
	}
	a.printf("streaming %s to stdout...", baseName(obj.Key))

	n, err := f.Object(ctx, found.backup.Prefix(), obj, a.streams.out(), restore.FetchOptions{Raw: raw})
	if err != nil {
		return classifyRepository(err)
	}
	// No emit: the answer is the bytes. A JSON envelope here would be mixed
	// into the artifact, so --output json is refused rather than obeyed.
	a.printf("%s bytes", humanBytes(n))
	return nil
}

// soleObject picks the one object a pipe can carry.
func soleObject(m manifest.Manifest, only string) (manifest.Object, error) {
	if only != "" {
		for _, o := range m.Objects {
			name := baseName(o.Key)
			if name == only || strings.TrimSuffix(strings.TrimSuffix(name, ".age"), ".zst") == only {
				return o, nil
			}
		}
		return manifest.Object{}, fault(ExitUsage, "backup %s has no object named %q", m.BackupID, only)
	}

	// The main artifact by default: it is what anyone piping a fetch wants, and
	// guessing between a dump and a sidecar would be worse than asking.
	for _, suffix := range []string{".pgdump", ".tar", ".xbstream"} {
		if o, ok := objectNamed(m, suffix); ok {
			return o, nil
		}
	}
	return manifest.Object{}, fault(ExitUsage,
		"backup %s has no single main artifact; name one with --object", m.BackupID)
}

// ---------------------------------------------------------------- schedule

func (a *app) scheduleCmd() *cobra.Command {
	var once bool
	c := &cobra.Command{
		Use:   "schedule",
		Short: "Run the built-in scheduler until stopped",
		Long: "Run every source that has a schedule, on that schedule, until stopped.\n\n" +
			"One job at a time per source: a run that overruns its next window is skipped\n" +
			"and said so, never queued behind itself. Failures are retried by class --\n" +
			"a storage timeout is worth another go, a configuration mistake is not, and\n" +
			"retrying one every minute is how alerts become noise.\n\n" +
			"SIGHUP rereads the configuration without disturbing a running backup.\n" +
			"SIGINT and SIGTERM stop scheduling and cancel what is running, which is what\n" +
			"kills pg_dump rather than leaving it holding a connection.\n\n" +
			"Delegating to cron or systemd instead is a supported choice: every job here\n" +
			"is `koffr backup <source>` (EF-091).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.begin(cmd); err != nil {
				return err
			}
			return a.runSchedule(cmd.Context(), once)
		},
	}
	c.Flags().BoolVar(&once, "dry-run", false,
		"print the timetable and the next run of each job, then exit")
	return c
}

func (a *app) runSchedule(ctx context.Context, dryRun bool) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	jobs, err := scheduledJobs(cfg)
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		return fault(ExitConfig,
			"no source in %s has a schedule; add `schedule: \"@daily\"` to one, "+
				"or drive `koffr backup` from cron instead", cfg.Path())
	}

	// The catalog answers "when did this source last succeed", which is what
	// tells a window that was taken from one that went by unattended. It is
	// read once, at start: a scheduler that reopened the catalog on every tick
	// would spend its life contending with the jobs it started.
	lastSuccess, err := lastSuccessfulBackups(ctx, cfg)
	if err != nil {
		a.warnf("koffr: could not read the catalog, so no missed window will be picked up: %v", err)
	}

	if err := a.setupLogging(cfg); err != nil {
		return err
	}
	defer func() { _ = a.closeLog() }()

	hub, err := a.buildHub(cfg)
	if err != nil {
		return err
	}
	defer hub.Wait()

	dms, err := a.buildDeadMansSwitch(cfg)
	if err != nil {
		return err
	}

	sched := &scheduler.Scheduler{
		Location:            cfg.Scheduler.Location(),
		MaxConcurrent:       cfg.Scheduler.MaxConcurrent,
		Window:              cfg.Scheduler.ExecutionWindow(),
		CancelOnWindowClose: cfg.Scheduler.Window.CancelOnClose,
		DisableCatchUp:      !cfg.Scheduler.CatchUpEnabled(),
		LastSuccess: func(sourceID string) (time.Time, bool) {
			at, ok := lastSuccess[sourceID]
			return at, ok
		},
		// Without this the class never reaches the policy and a configuration
		// mistake is retried every minute until someone mutes the alerts.
		Classify: pipeline.ClassOf,
		Retry: scheduler.RetryPolicy{
			Attempts:     cfg.Scheduler.Retry.Attempts,
			InitialDelay: cfg.Scheduler.Retry.InitialDelay,
			MaxDelay:     cfg.Scheduler.Retry.MaxDelay,
		},
		// backupOnce rather than runBackup: the latter records a result for
		// the command to render, and `koffr schedule` would then report the
		// last backup as its own answer -- in JSON, a backup envelope labelled
		// "schedule".
		Execute: func(ctx context.Context, job scheduler.Job) error {
			res, err := a.backupOnce(ctx, job.SourceID, backupOptions{
				destination: job.Destination,
				request:     source.Request{Kind: job.Kind},
			})
			if err == nil {
				a.logf(ctx, slog.LevelInfo, "backup completed",
					"source", job.SourceID, "backup_id", string(res.BackupID),
					"bytes", totalBytes(res.Manifest), "destination", job.Destination)
				a.reportSuccess(ctx, hub, dms, job.SourceID, string(res.BackupID),
					totalBytes(res.Manifest))
			}
			return err
		},
		// Both hooks report rather than decide. A skipped window and a failed
		// attempt are exactly what an operator needs told, and in M4 they are
		// what the notifier will listen to.
		OnSkip: func(job scheduler.Job, why string) {
			a.logf(ctx, slog.LevelWarn, "scheduled window passed over",
				"source", job.SourceID, "reason", why)
			hub.Publish(ctx, notify.Event{
				Kind: notify.KindBackupSkipped, Severity: notify.SeverityWarning,
				SourceID: job.SourceID,
				Message:  "a scheduled window was passed over: " + why,
			})
		},
		OnStart: func(job scheduler.Job, catchUp bool) {
			a.logf(ctx, slog.LevelInfo, "backup starting",
				"source", job.SourceID, "destination", job.Destination, "catch_up", catchUp)
			if !catchUp {
				return
			}
			// A backup running is routine. A backup running because last
			// night's did not is a fact worth telling someone, and it is the
			// only signal that a window was ever missed.
			a.printf("%s: making good a missed window", job.SourceID)
			hub.Publish(ctx, notify.Event{
				Kind: notify.KindBackupCaughtUp, Severity: notify.SeverityWarning,
				SourceID: job.SourceID,
				Message: "a scheduled backup did not run when it should have; " +
					"one is being taken now",
			})
		},
		OnResult: func(res scheduler.Result) {
			if res.Err == nil {
				return // reported where the backup id is known
			}
			a.logf(ctx, slog.LevelError, "backup attempt failed",
				"source", res.Job.SourceID, "attempt", res.Attempt,
				"class", string(pipeline.ClassOf(res.Err)),
				"will_retry", res.WillRetry, "error", res.Err.Error())
			a.reportFailure(ctx, hub, res.Job.SourceID, res.Attempt, res.WillRetry, res.Err)
		},
	}
	if err := sched.SetJobs(jobs); err != nil {
		return &Fault{Code: ExitConfig, Err: err}
	}

	// Retention on its own timetable, and only if one was written. A purge that
	// ran because nobody said it should not is the one automation whose
	// mistakes cannot be undone -- so this stays opt-in even though a
	// repository that grows for ever is the alternative.
	stopPrune, err := a.schedulePrune(ctx, cfg)
	if err != nil {
		return err
	}
	defer stopPrune()

	if dryRun {
		return a.printTimetable(cfg, jobs)
	}

	// Databasus does this and Koffr did not: a process that died left its job
	// recorded as running for ever, so the catalog claimed a backup was in
	// progress that no process was doing. Reconciling on start is the only
	// moment the truth is knowable.
	a.reconcileInterruptedJobs(ctx, cfg, hub)

	// The flag the readiness endpoint reads. Set before the loop starts and
	// cleared when it ends, so /readyz is honest during shutdown as well.
	var schedulerRunning atomic.Bool
	schedulerRunning.Store(true)
	defer schedulerRunning.Store(false)

	stopHealth, err := a.serveHealth(ctx, cfg, jobs, &schedulerRunning)
	if err != nil {
		return err
	}
	defer stopHealth()

	a.printf("scheduling %d source(s) in %s, window %s; SIGHUP rereads %s",
		len(jobs), cfg.Scheduler.Location(), cfg.Scheduler.ExecutionWindow(), cfg.Path())

	// EF-104. A reload replaces the timetable and says nothing about what is
	// running: a backup halfway through is work already paid for.
	hangup := make(chan os.Signal, 1)
	signal.Notify(hangup, syscall.SIGHUP)
	defer signal.Stop(hangup)

	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()

	for {
		select {
		case err := <-done:
			// Any context error means "you were asked to stop", not "you
			// failed". A deadline and a signal are the same thing from here.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				a.printf("stopped")
				return nil
			}
			return err
		case <-hangup:
			a.reload(sched)
		}
	}
}

// reload rereads the configuration on SIGHUP.
//
// A configuration that no longer loads leaves the old timetable in place and
// says so. Stopping would turn a typo into a night with no backups, which is a
// worse outcome than running yesterday's schedule.
func (a *app) reload(sched *scheduler.Scheduler) {
	cfg, err := a.loadConfig()
	if err != nil {
		a.warnf("koffr: SIGHUP: the configuration did not load, keeping the previous schedule: %v", err)
		return
	}
	jobs, err := scheduledJobs(cfg)
	if err != nil {
		a.warnf("koffr: SIGHUP: %v; keeping the previous schedule", err)
		return
	}
	if err := sched.SetJobs(jobs); err != nil {
		a.warnf("koffr: SIGHUP: %v; keeping the previous schedule", err)
		return
	}
	a.printf("reloaded %s: %d scheduled source(s)", cfg.Path(), len(jobs))
}

// scheduledJobs turns the configuration into a timetable.
func scheduledJobs(cfg config.Config) ([]scheduler.Job, error) {
	var jobs []scheduler.Job
	for _, id := range cfg.SourceIDs() {
		src, _ := cfg.Source(id)
		if src.Schedule == "" {
			continue
		}
		if len(src.Destinations) == 0 {
			return nil, fmt.Errorf("source %s is scheduled but has no destination", id)
		}
		jobs = append(jobs, scheduler.Job{
			SourceID:    id,
			Destination: src.Destinations[0],
			Kind:        source.KindLogical,
			Spec:        src.Schedule,
		})
	}
	return jobs, nil
}

func (a *app) printTimetable(cfg config.Config, jobs []scheduler.Job) error {
	type line struct {
		Source      string `json:"source"`
		Schedule    string `json:"schedule"`
		Destination string `json:"destination"`
		Next        string `json:"next_run"`
	}
	now := time.Now().In(cfg.Scheduler.Location())

	out := make([]line, 0, len(jobs))
	for _, j := range jobs {
		next, err := scheduler.NextRun(j.Spec, now)
		if err != nil {
			return &Fault{Code: ExitConfig, Err: err}
		}
		out = append(out, line{
			Source: j.SourceID, Schedule: j.Spec, Destination: j.Destination,
			Next: next.Format(time.RFC3339),
		})
	}

	a.emit(struct {
		Timezone string `json:"timezone"`
		Jobs     []line `json:"jobs"`
	}{cfg.Scheduler.Location().String(), out}, func(p *printer) {
		p.table(func(p *printer) {
			p.printf("SOURCE\tSCHEDULE\tDESTINATION\tNEXT RUN\n")
			for _, l := range out {
				p.printf("%s\t%s\t%s\t%s\n", l.Source, l.Schedule, l.Destination, l.Next)
			}
		})
	})
	return nil
}

// reconcileInterruptedJobs closes out jobs a dead process left open.
//
// A crash, a SIGKILL or a machine reboot leaves a job recorded as running, and
// nothing else will ever change that: the process that would have finished it
// is gone. Left alone the catalog says a backup is in progress, which is the
// one state an operator cannot act on -- neither "it worked" nor "it failed".
//
// Start-up is the only moment this is knowable, because a single-writer catalog
// means anything still marked running belongs to a previous life.
func (a *app) reconcileInterruptedJobs(ctx context.Context, cfg config.Config, hub *notify.Hub) {
	cat, err := openCatalog(ctx, cfg)
	if err != nil {
		a.warnf("koffr: could not open the catalog to reconcile interrupted jobs: %v", err)
		return
	}
	defer func() { _ = cat.Close() }()

	snap, err := cat.Export(ctx)
	if err != nil {
		a.warnf("koffr: could not read the catalog to reconcile interrupted jobs: %v", err)
		return
	}

	for _, job := range snap.Jobs {
		if job.Status != catalog.StatusRunning {
			continue
		}
		job.Status = catalog.StatusFailed
		job.ErrorClass = catalog.ErrClassCanceled
		job.ErrorDetail = "Koffr stopped before this job finished; it was marked failed on the next start"
		job.FinishedAt = time.Now().UTC()
		if err := cat.RecordJob(ctx, job); err != nil {
			a.warnf("koffr: could not close out interrupted job %s: %v", job.ID, err)
			continue
		}
		a.printf("job %s (%s) was left running by a previous run; marked failed", job.ID, job.SourceID)
		hub.Publish(ctx, notify.Event{
			Kind: notify.KindJobInterrupted, Severity: notify.SeverityWarning,
			SourceID: job.SourceID,
			Message: "a backup was still recorded as running when Koffr started, " +
				"so the previous run ended without finishing it",
		})
	}
}

// lastSuccessfulBackups reports when each source last completed a backup.
//
// Completed, not attempted: a source whose last three jobs failed has still
// missed its windows, and treating a failure as coverage is how a gap becomes
// invisible.
func lastSuccessfulBackups(ctx context.Context, cfg config.Config) (map[string]time.Time, error) {
	cat, err := openCatalog(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cat.Close() }()

	backups, err := cat.ListBackups(ctx, catalog.BackupFilter{Status: catalog.StatusCompleted})
	if err != nil {
		return nil, err
	}

	latest := make(map[string]time.Time, len(backups))
	for _, b := range backups {
		if at, seen := latest[b.SourceID]; !seen || b.StartedAt.After(at) {
			latest[b.SourceID] = b.StartedAt
		}
	}
	return latest, nil
}

// schedulePrune runs the retention policies on their own cron, alongside the
// backups.
//
// A scheduler of its own rather than another job in the main one: a purge is
// not a backup, it must not consume a backup's concurrency slot, and a source
// whose purge overran should not have its next backup skipped for it.
func (a *app) schedulePrune(ctx context.Context, cfg config.Config) (stop func(), err error) {
	if cfg.Scheduler.Prune == "" {
		return func() {}, nil
	}

	pruner := &scheduler.Scheduler{
		Location:      cfg.Scheduler.Location(),
		MaxConcurrent: 1,
		Window:        cfg.Scheduler.ExecutionWindow(),
		// No catch-up. A missed backup is a gap in history worth making good;
		// a missed purge is a day of extra storage, and hurrying to delete
		// things after an outage is the wrong instinct entirely.
		DisableCatchUp: true,
		Execute: func(ctx context.Context, _ scheduler.Job) error {
			// --confirm, because a scheduled dry run would be a daily report
			// nobody reads. The decision was made when the policy was written.
			return a.runPrune(ctx, "", true, false)
		},
		OnResult: func(res scheduler.Result) {
			if res.Err != nil {
				a.logf(ctx, slog.LevelError, "scheduled purge failed", "error", res.Err.Error())
			}
		},
	}
	if err := pruner.SetJobs([]scheduler.Job{{SourceID: "retention", Spec: cfg.Scheduler.Prune}}); err != nil {
		return nil, &Fault{Code: ExitConfig, Err: err}
	}

	a.printf("retention runs on %s", cfg.Scheduler.Prune)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = pruner.Run(ctx)
	}()
	return func() { <-done }, nil
}

// openMirrors opens every destination but the primary.
func (a *app) openMirrors(
	ctx context.Context, cfg config.Config, src config.Source, primary string,
) ([]backup.Mirrored, error) {
	var out []backup.Mirrored
	for _, name := range src.Destinations {
		if name == primary {
			continue
		}
		dest, known := cfg.Destinations[name]
		if !known {
			return nil, fault(ExitConfig, "no destination %q in %s", name, cfg.Path())
		}
		st, err := openStorage(ctx, dest)
		if err != nil {
			return nil, err
		}
		out = append(out, backup.Mirrored{Name: name, Storage: st})
	}
	return out, nil
}

// fromFlagHelp is shared because the flag means the same thing everywhere, and
// three slightly different sentences would be three chances to disagree.
const fromFlagHelp = "read from this destination; by default, wherever the backup is"
