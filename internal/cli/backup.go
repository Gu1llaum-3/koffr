package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/domain/backup"
	"github.com/Gu1llaum-3/koffr/internal/domain/catalog"
	"github.com/Gu1llaum-3/koffr/internal/domain/crypto"
	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
	"github.com/Gu1llaum-3/koffr/internal/engine"
	"github.com/Gu1llaum-3/koffr/internal/pipeline"
	"github.com/Gu1llaum-3/koffr/internal/store"
)

func newBackupCommand() *cobra.Command {
	var (
		dryRun     bool
		searchPath []string
	)

	cmd := &cobra.Command{
		Use:   "backup <database>",
		Short: "Back up one database to its destinations",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			service, request, declared, err := backupService(cmd, args[0], searchPath)
			if err != nil {
				return err
			}

			if dryRun {
				planned, err := service.Plan(cmd.Context(), request)
				renderPlan(cmd, planned)

				return err //nolint:wrapcheck // the use case already names the database and the step
			}

			done, err := service.Run(cmd.Context(), request)
			renderBackup(cmd, done)

			if err == nil {
				recordInCatalogue(cmd, done, declared)
			}

			return err //nolint:wrapcheck // the use case already names the database and the step
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "say what would be done, and write nothing")
	cmd.Flags().StringSliceVar(&searchPath, "search-path", nil,
		"directories to look for tools in instead of the ones koffr knows about")

	return cmd
}

// backupService wires one database: what resolves it, what dumps it, what packs
// it and where it goes. The domain knows none of these by name (ADR-0010).
func backupService(
	cmd *cobra.Command, database string, searchPath []string,
) (*backup.Service, backup.Request, config.Database, error) {
	loaded, err := loadResolved(cmd)
	if err != nil {
		return nil, backup.Request{}, config.Database{}, err
	}

	subjects, err := targetsOf(loaded, database)
	if err != nil {
		return nil, backup.Request{}, config.Database{}, err
	}

	declared, err := declaredDatabase(loaded, database)
	if err != nil {
		return nil, backup.Request{}, config.Database{}, err
	}

	destinations, err := destinationsOf(loaded, declared)
	if err != nil {
		return nil, backup.Request{}, config.Database{}, err
	}

	recipients, err := recipientsFor(loaded, declared)
	if err != nil {
		return nil, backup.Request{}, config.Database{}, err
	}

	if warning := recipients.Warning(); warning != "" {
		warn(cmd, "%s\n", warning)
	}

	access := &databaseAccess{
		subject:     subjects[0],
		onHost:      finderFor(cmd, searchPath),
		inContainer: containerFinder(),
		stagingDir:  stateDir(cmd),
	}

	service := backup.NewService(backup.Wiring{
		StateDirectory: stateDir(cmd),
		Staging:        backup.Mode(declared.Staging),
		Resolver:       access,
		Dumper:         access,
		Capacity:       access,
		History:        noHistory{},
		Journal:        slogJournal{logger: loggerOf(cmd)},
		Packer:         pipelinePacker{recipients: recipients},
		Manifester:     manifester{database: declared, recipients: recipients},
		Destinations:   destinations,
	})

	return service, backup.Request{Database: database, JobID: backup.NewJobID(), At: time.Now()}, declared, nil
}

func declaredDatabase(loaded *config.Config, id string) (config.Database, error) {
	for _, database := range loaded.Databases {
		if database.ID == id {
			return database, nil
		}
	}

	return config.Database{}, fmt.Errorf("no database is called %q", id)
}

// destinationsOf builds a store per destination the database names. A database
// that names none is a configuration mistake, not a backup without a copy.
func destinationsOf(loaded *config.Config, database config.Database) ([]backup.Destination, error) {
	if len(database.Destinations) == 0 {
		return nil, fmt.Errorf("the database %q declares no destination, so there is nowhere to write its archives",
			database.ID)
	}

	declared := make(map[string]config.Destination, len(loaded.Destinations))
	known := make([]string, 0, len(loaded.Destinations))

	for _, destination := range loaded.Destinations {
		declared[destination.ID] = destination
		known = append(known, destination.ID)
	}

	slices.Sort(known)

	built := make([]backup.Destination, 0, len(database.Destinations))

	for _, id := range database.Destinations {
		found, ok := declared[id]
		if !ok {
			return nil, fmt.Errorf("the database %q sends its archives to %q, which no destination declares; "+
				"the configuration declares: %s", database.ID, id, strings.Join(known, ", "))
		}

		if found.Type != "filesystem" {
			return nil, fmt.Errorf("the destination %q is of type %q, and only filesystem is implemented "+
				"in this release; s3 and sftp arrive at the lot 4", id, found.Type)
		}

		built = append(built, backup.Destination{ID: id, Store: store.NewFilesystem(found.Path)})
	}

	return built, nil
}

// databaseAccess is one database, seen from the adapters: it resolves its tool,
// dumps it, and measures what a backup of it would take. One type because the
// three answers come from the same probe, and probing three times to answer
// them separately would be three connections to a production server.
type databaseAccess struct {
	subject     resolve.Subject
	onHost      resolve.ToolFinder
	inContainer resolve.ContainerToolFinder
	stagingDir  string

	server resolve.ServerInfo
	tool   resolve.Candidate
}

func (d *databaseAccess) Resolve(ctx context.Context, database string) (backup.Resolution, error) {
	diagnosed := resolve.Diagnose(ctx, engine.New(), d.onHost, d.inContainer, []resolve.Subject{d.subject})[0]

	if diagnosed.Unreachable != nil {
		return backup.Resolution{}, fmt.Errorf("%s does not answer: %w", database, diagnosed.Unreachable)
	}
	if diagnosed.NoTool != nil {
		return backup.Resolution{}, diagnosed.NoTool
	}

	d.server, d.tool = diagnosed.Server, diagnosed.Tool

	resolution := backup.Resolution{
		Engine:        string(diagnosed.Server.Family),
		ServerVersion: diagnosed.Server.Version.String(),
		ToolPath:      diagnosed.Tool.Path,
		ToolVersion:   diagnosed.Tool.Version.String(),
		ToolSource:    string(diagnosed.Tool.Source),
		Extension:     extensionFor(diagnosed.Server.Family),
		// The options that describe the archive, never the connection: the
		// manifest is deposited unencrypted on every destination (E-059).
		Argv: engine.ArchiveOptions(engine.DumpRequest{
			Target: d.subject.Target, Tool: diagnosed.Tool, Container: d.subject.Container,
		}),
	}

	if warning := diagnosed.Server.MyISAMWarning(); warning != "" {
		resolution.Warnings = append(resolution.Warnings, warning)
	}

	return resolution, nil
}

func (d *databaseAccess) Dump(ctx context.Context, _ backup.Resolution, _ backup.Request) (io.ReadCloser, error) {
	dump, err := engine.NewWithDocker(engine.ContainerOptions{}).Dump(ctx, engine.DumpRequest{
		Target:    d.subject.Target,
		Tool:      d.tool,
		Container: d.subject.Container,
	})
	if err != nil {
		return nil, fmt.Errorf("start the dump of %s: %w", d.subject.ID, err)
	}

	return dump, nil
}

// DatabaseBytes is what the probe read, not a second connection.
func (d *databaseAccess) DatabaseBytes(context.Context, string) (int64, error) {
	return d.server.DatabaseBytes, nil
}

// FreeBytes measures the volume the staging file would live on — that one, not
// whatever /tmp happens to be.
func (d *databaseAccess) FreeBytes(context.Context) (int64, error) {
	if err := os.MkdirAll(d.stagingDir, 0o700); err != nil {
		return 0, fmt.Errorf("make the state directory %s: %w", d.stagingDir, err)
	}

	var volume syscall.Statfs_t
	if err := syscall.Statfs(d.stagingDir, &volume); err != nil {
		return 0, fmt.Errorf("measure the free space of %s: %w", d.stagingDir, err)
	}

	return bytesFree(volume), nil
}

func extensionFor(family resolve.Family) string {
	if family == resolve.PostgreSQL {
		return "pgc"
	}

	return "sql"
}

// pipelinePacker is internal/pipeline behind the port the domain declares.
type pipelinePacker struct {
	recipients crypto.Recipients
}

func (p pipelinePacker) Pack(_ context.Context, into io.Writer, from io.Reader) (backup.Packed, error) {
	done, err := pipeline.Run(into, from, pipeline.Options{Recipients: p.recipients})
	if err != nil {
		return backup.Packed{}, fmt.Errorf("compress and encrypt: %w", err)
	}

	return backup.Packed{
		RawBytes: done.RawBytes, StoredBytes: done.StoredBytes,
		SHA256Raw: done.RawSHA256, SHA256Stored: done.StoredSHA256,
		Pipeline: p.Pipeline(),
	}, nil
}

// Pipeline is what internal/pipeline applies, in the order § 4.1 gives it:
// compress, then encrypt. The archive is named after it (A-13).
func (p pipelinePacker) Pipeline() []string {
	return []string{"zstd:3", "age:x25519"}
}

// noHistory is what koffr knows about previous runs in this release: nothing.
// The catalogue is written at the lot 3, so every estimate falls back on the
// size of the database, which is the other half of E-061 (`N-13`).
type noHistory struct{}

func (noHistory) LastSuccessful(context.Context, string) (*backup.PreviousBackup, error) {
	return nil, nil //nolint:nilnil // no history is not a failure, it is the state of this release
}

func stateDir(cmd *cobra.Command) string {
	if dir, err := cmd.Flags().GetString("state-dir"); err == nil && dir != "" {
		return dir
	}

	return config.DefaultStateDir
}

func renderPlan(cmd *cobra.Command, planned backup.Result) {
	if planned.Path == "" {
		return
	}

	say(cmd, "dry run  %s\n", planned.Database)
	say(cmd, "archive  %s\n", planned.Path)
	say(cmd, "staging  %s (%s)\n", planned.Staging, planned.StagingReason)
	say(cmd, "sending  %s\n", strings.Join(planned.Destinations, ", "))
	say(cmd, "%v\n", "nothing was written")

	for _, warning := range planned.Warnings {
		warn(cmd, "%s\n", warning)
	}
}

func renderBackup(cmd *cobra.Command, done backup.Result) {
	if done.Path == "" {
		return
	}

	say(cmd, "archive  %s\n", done.Path)
	say(cmd, "staging  %s (%s)\n", done.Staging, done.StagingReason)
	say(cmd, "size     %d bytes stored, %d dumped\n", done.StoredBytes, done.RawBytes)
	say(cmd, "sha256   %s\n", done.SHA256Stored)
	say(cmd, "sent to  %s\n", strings.Join(done.Destinations, ", "))

	for _, outcome := range done.Steps {
		if outcome.Deferred != "" {
			say(cmd, "pending  %s — %s\n", outcome.Step, outcome.Deferred)
		}
	}

	for _, warning := range done.Warnings {
		warn(cmd, "%s\n", warning)
	}
}

// slogJournal writes the trace of a job to the file of E-026. The domain knows
// nothing of slog: it declares the port and this implements it (`N-1`, AR-01).
type slogJournal struct {
	logger *slog.Logger
}

func (j slogJournal) Step(entry backup.JobStep) {
	attributes := []any{
		slog.String("job", entry.Job),
		slog.String("database", entry.Database),
		slog.String("step", string(entry.Step)),
	}

	for _, fact := range entry.Facts {
		attributes = append(attributes, slog.Any(fact.Name, fact.Value))
	}

	switch {
	case entry.Failed != nil:
		j.logger.Error("step failed", append(attributes, slog.String("error", entry.Failed.Error()))...)

	case entry.Deferred != "":
		j.logger.Info("step deferred", append(attributes, slog.String("deferred", entry.Deferred))...)

	default:
		j.logger.Info("step done", attributes...)
	}
}

// recipientsFor applies `Q-04`: a database that declares its own recipients is
// encrypted for **them alone**; one that declares none inherits the fleet's
// (ADR-0016).
func recipientsFor(loaded *config.Config, database config.Database) (crypto.Recipients, error) {
	fleet, err := crypto.LoadRecipients(loaded.Encryption.RecipientsFile)
	if err != nil {
		return crypto.Recipients{}, fmt.Errorf("the encryption keys of the fleet: %w", err)
	}

	if database.RecipientsFile == "" {
		return fleet, nil
	}

	own, err := crypto.LoadRecipients(database.RecipientsFile)
	if err != nil {
		return crypto.Recipients{}, fmt.Errorf("the encryption keys of %s: %w", database.ID, err)
	}

	return crypto.Effective(fleet, own), nil
}

// recordInCatalogue indexes a finished backup.
//
// It is the **command** that does it, not the use case: `AR-03` keeps
// `domain/backup` and `domain/catalog` from knowing each other, and neither has
// any business importing the other to write a row (`N-9`).
//
// A catalogue that cannot record does **not** fail a backup that is already
// written (`N-10`): the archive and its manifest are on the destination, and
// the manifest is the source of truth (ADR-0006). Losing the index is a warning.
func recordInCatalogue(cmd *cobra.Command, done backup.Result, declared config.Database) {
	book, closer, err := catalogFor(cmd)
	if err != nil {
		warn(cmd, "the backup is written but was not indexed: %v\n", err)

		return
	}
	defer func() { _ = closer() }()

	if err := book.RecordDatabase(cmd.Context(), databaseSnapshot(declared), done.At); err != nil {
		warn(cmd, "the backup is written but its database was not indexed: %v\n", err)

		return
	}

	entry := catalog.Backup{
		ID: done.JobID, Database: done.Database,
		StartedAt: done.At, FinishedAt: done.At.Add(done.Duration),
		RawBytes: done.RawBytes, StoredBytes: done.StoredBytes,
		SHA256Raw: done.SHA256Raw, SHA256Stored: done.SHA256Stored,
		Verified: catalog.NotVerified,
	}

	for _, destination := range done.Destinations {
		entry.Locations = append(entry.Locations, catalog.Location{
			Destination: destination, Path: done.Path,
			Bytes: done.StoredBytes, StoredAt: done.At.Add(done.Duration),
		})
	}

	if err := book.RecordBackup(cmd.Context(), entry); err != nil {
		warn(cmd, "the backup is written but was not indexed: %v\n", err)
	}
}

// manifester renders the manifest of E-058. It lives here because the domain
// declares that it needs bytes and never what is in them: the shape belongs to
// `catalog`, and `AR-03` keeps `backup` and `catalog` from knowing each other.
type manifester struct {
	database   config.Database
	recipients crypto.Recipients
}

func (m manifester) Render(done backup.Result) ([]byte, error) {
	indexed := catalog.Backup{
		ID: done.JobID, Database: done.Database,
		StartedAt: done.At, FinishedAt: done.At.Add(done.Duration),
		RawBytes: done.RawBytes, StoredBytes: done.StoredBytes,
		SHA256Raw: done.SHA256Raw, SHA256Stored: done.SHA256Stored,
		Verified: catalog.NotVerified,
	}

	run := catalog.Run{
		ServerVersion: done.Resolution.ServerVersion,
		Tool: catalog.Tool{
			Name:    filepath.Base(done.Resolution.ToolPath),
			Version: done.Resolution.ToolVersion,
			Source:  done.Resolution.ToolSource,
			Path:    done.Resolution.ToolPath,
			Argv:    done.Resolution.Argv,
		},
		Format:     done.Resolution.Extension,
		Pipeline:   done.Pipeline,
		Staging:    string(done.Staging),
		Recipients: m.recipients.Public(),
	}

	rendered, err := json.MarshalIndent(catalog.ManifestOf(indexed, databaseSnapshot(m.database), run), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render the manifest of %s: %w", done.Database, err)
	}

	return append(rendered, '\n'), nil
}

// databaseSnapshot is the resolved configuration as the catalogue and the
// manifest keep it: no credential, ever (E-115, E-059).
func databaseSnapshot(declared config.Database) catalog.Database {
	return catalog.Database{
		ID: declared.ID, Engine: declared.Engine, Host: declared.Host, Port: declared.Port,
		Name: declared.Database, User: declared.User,
		Destinations: declared.Destinations, Staging: declared.Staging,
		Schedule: declared.Schedule, Container: declared.Tools.Container,
		Retention: catalog.Retention{
			Last: declared.Retention.Last, Daily: declared.Retention.Daily,
			Weekly: declared.Retention.Weekly, Monthly: declared.Retention.Monthly,
		},
	}
}
