package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Step is one of the seven steps of E-024, in the order § 4.1 gives them. A
// result carries all seven, whether they ran or not: a job that is silent about
// a step it skipped is a job nobody can audit.
type Step string

// The seven steps of E-024.
const (
	StepResolution   Step = "resolution"
	StepDump         Step = "dump"
	StepCompression  Step = "compression"
	StepEncryption   Step = "encryption"
	StepWrite        Step = "write"
	StepVerification Step = "verification"
	StepManifest     Step = "manifest"
)

// Steps is the order of E-024. Nothing reorders it.
var Steps = []Step{
	StepResolution, StepDump, StepCompression,
	StepEncryption, StepWrite, StepVerification, StepManifest,
}

// deferredToLot3 is what the two last steps say until the lot 3 brings them.
const deferredToLot3 = "not in this release: verification and manifest arrive at the lot 3"

// StepOutcome is what became of one step.
type StepOutcome struct {
	Step Step
	Done bool

	// Deferred is non-empty when the step is not implemented yet. It is never
	// set on a step that merely failed: a failure is an error, not an absence.
	Deferred string
}

// Request is one backup to run.
type Request struct {
	Database string
	JobID    string
	At       time.Time
}

// Resolution is what the resolver settled on: the server, and the tool that
// will dump it. It is carried into the manifest at the lot 3 (§ 5.3).
type Resolution struct {
	Engine        string
	ServerVersion string
	ToolPath      string
	ToolVersion   string
	ToolSource    string

	// DirectoryDump says the dump will be -Fd, which imposes staging (E-030).
	DirectoryDump bool

	// Extension is what the archive file is called: `pgc`, `sql`…
	Extension string

	// Warnings are what an operator has to be told about this database —
	// MyISAM tables, for one (E-056). They travel with the job.
	Warnings []string
}

// Packed is what the pipeline produced.
type Packed struct {
	RawBytes     int64
	StoredBytes  int64
	SHA256Raw    string
	SHA256Stored string
	Pipeline     []string
}

// The ports of this use case. The domain says what it needs; `cmd/koffr` wires
// the adapters that can do it (ADR-0010).
type (
	// Resolver answers which tool dumps this database, and what the server is.
	Resolver interface {
		Resolve(ctx context.Context, database string) (Resolution, error)
	}

	// Dumper starts the dump and hands back its output. Closing that reader
	// waits for the sub-process and reports what it said (E-055).
	Dumper interface {
		Dump(ctx context.Context, resolution Resolution, request Request) (io.ReadCloser, error)
	}

	// Packer compresses, encrypts and fingerprints in one pass (E-025).
	Packer interface {
		Pack(ctx context.Context, into io.Writer, from io.Reader) (Packed, error)
	}

	// Capacity measures what E-061 needs before anything starts.
	Capacity interface {
		FreeBytes(ctx context.Context) (int64, error)
		DatabaseBytes(ctx context.Context, database string) (int64, error)
	}

	// History is the last successful run of a database, the best estimate of
	// what the next one will take.
	History interface {
		LastSuccessful(ctx context.Context, database string) (*PreviousBackup, error)
	}
)

// Destination is one place an archive goes, with the identifier the operator
// gave it: an error has to say **which** destination failed.
type Destination struct {
	ID    string
	Store Store
}

// Wiring is what a Service is built from.
type Wiring struct {
	StateDirectory string
	Staging        Mode

	Resolver     Resolver
	Dumper       Dumper
	Packer       Packer
	Capacity     Capacity
	History      History
	Destinations []Destination

	// StructuralVerifyWithoutEgress forwards the third case of E-030.
	StructuralVerifyWithoutEgress bool
}

// Service runs a backup. It decides in what order, whether it may start at all,
// and what happens when something breaks — it does not dump, write or compress.
type Service struct {
	wiring Wiring
}

// NewService builds the use case.
func NewService(wiring Wiring) *Service {
	return &Service{wiring: wiring}
}

// Result is what a job did. It is filled as the job goes, and returned even
// when the job fails: what ran before the failure is what an operator needs.
type Result struct {
	JobID    string
	Database string
	At       time.Time
	Duration time.Duration

	Staging       Mode
	StagingReason string
	StagingForced bool

	Path         string
	RawBytes     int64
	StoredBytes  int64
	SHA256Raw    string
	SHA256Stored string
	Pipeline     []string
	Destinations []string

	Warnings []string
	Steps    []StepOutcome
}

// Plan is the dry run of E-103b: it resolves the tool, decides the staging mode
// and says where the archive would go — and stops there.
//
// It takes no lock and writes nothing, not even a lock file. It does connect to
// the server, because a plan that could not name the tool it would run would be
// a plan of nothing.
func (s *Service) Plan(ctx context.Context, request Request) (Result, error) {
	result := Result{
		JobID: request.JobID, Database: request.Database, At: request.At,
		Steps: freshSteps(),
	}

	resolution, err := s.wiring.Resolver.Resolve(ctx, request.Database)
	if err != nil {
		return result, fmt.Errorf("resolve a tool for %s: %w", request.Database, err)
	}

	result.Warnings = resolution.Warnings
	result.mark(StepResolution)

	decision, err := s.decideStaging(ctx, request.Database, resolution)
	if err != nil {
		return result, err
	}

	result.Staging, result.StagingReason, result.StagingForced = decision.Mode, decision.Reason, decision.Forced
	result.Path = ArchivePath(request.Database, request.At, request.JobID, resolution.Extension)

	for _, destination := range s.wiring.Destinations {
		result.Destinations = append(result.Destinations, destination.ID)
	}

	return result, nil
}

// Run backs up one database, following the seven steps of E-024 in order.
func (s *Service) Run(ctx context.Context, request Request) (Result, error) {
	result := Result{
		JobID: request.JobID, Database: request.Database, At: request.At,
		Steps: freshSteps(),
	}

	// E-051, before anything else: a second job on this database is refused,
	// not queued.
	lock, err := Acquire(s.wiring.StateDirectory, request.Database, request.JobID, request.At)
	if err != nil {
		return result, err
	}
	defer func() { _ = lock.Release() }()

	started := time.Now()
	defer func() { result.Duration = time.Since(started) }()

	resolution, err := s.wiring.Resolver.Resolve(ctx, request.Database)
	if err != nil {
		return result, fmt.Errorf("resolve a tool for %s: %w", request.Database, err)
	}

	result.Warnings = resolution.Warnings
	result.mark(StepResolution)

	decision, err := s.decideStaging(ctx, request.Database, resolution)
	if err != nil {
		return result, err
	}

	result.Staging, result.StagingReason, result.StagingForced = decision.Mode, decision.Reason, decision.Forced
	result.Path = ArchivePath(request.Database, request.At, request.JobID, resolution.Extension)

	packed, err := s.dumpAndWrite(ctx, request, resolution, decision.Mode, result.Path, &result)
	if err != nil {
		return result, err
	}

	result.RawBytes, result.StoredBytes = packed.RawBytes, packed.StoredBytes
	result.SHA256Raw, result.SHA256Stored = packed.SHA256Raw, packed.SHA256Stored
	result.Pipeline = packed.Pipeline

	for _, destination := range s.wiring.Destinations {
		result.Destinations = append(result.Destinations, destination.ID)
	}

	return result, nil
}

// decideStaging applies E-061 then § 4.5: what the archive is expected to take,
// what there is room for, and what that makes of the configured mode.
func (s *Service) decideStaging(ctx context.Context, database string, resolution Resolution) (StagingDecision, error) {
	free, err := s.wiring.Capacity.FreeBytes(ctx)
	if err != nil {
		return StagingDecision{}, fmt.Errorf("measure the free space: %w", err)
	}

	last, err := s.wiring.History.LastSuccessful(ctx, database)
	if err != nil {
		return StagingDecision{}, fmt.Errorf("read the last backup of %s: %w", database, err)
	}

	databaseBytes, err := s.wiring.Capacity.DatabaseBytes(ctx, database)
	if err != nil {
		return StagingDecision{}, fmt.Errorf("measure the size of %s: %w", database, err)
	}

	decided, err := DecideStaging(StagingInputs{
		Configured:                    s.wiring.Staging,
		DirectoryDump:                 resolution.DirectoryDump,
		Destinations:                  len(s.wiring.Destinations),
		StructuralVerifyWithoutEgress: s.wiring.StructuralVerifyWithoutEgress,
		FreeBytes:                     free,
		ExpectedBytes:                 EstimateStored(last, databaseBytes),
	})
	if err != nil {
		return StagingDecision{}, err
	}

	return decided, nil
}

// dumpAndWrite runs the middle of E-024: dump, compress, encrypt, write.
func (s *Service) dumpAndWrite(
	ctx context.Context, request Request, resolution Resolution,
	mode Mode, path string, result *Result,
) (Packed, error) {
	dump, err := s.wiring.Dumper.Dump(ctx, resolution, request)
	if err != nil {
		return Packed{}, fmt.Errorf("dump %s: %w", request.Database, err)
	}

	result.mark(StepDump)

	if mode == Stage {
		return s.stage(ctx, dump, path, result)
	}

	return s.stream(ctx, dump, path, result)
}

// stage writes the packed stream to a staging file, **closes the dump** — which
// is what ends the transaction on the database, E-055 — and only then sends.
func (s *Service) stage(ctx context.Context, dump io.ReadCloser, path string, result *Result) (Packed, error) {
	staging, err := s.stagingFile()
	if err != nil {
		_ = dump.Close()

		return Packed{}, err
	}

	defer func() {
		_ = staging.Close()
		_ = os.Remove(staging.Name())
	}()

	packed, packErr := s.wiring.Packer.Pack(ctx, staging, dump)

	// The dump is closed before anything is sent, whatever happened: the time a
	// dump holds a production server must not depend on a destination.
	closeErr := dump.Close()

	if err := errors.Join(packErr, closeErr); err != nil {
		return Packed{}, fmt.Errorf("pack the dump of %s: %w", result.Database, err)
	}

	result.mark(StepCompression, StepEncryption)

	for _, destination := range s.wiring.Destinations {
		if _, err := staging.Seek(0, io.SeekStart); err != nil {
			return Packed{}, fmt.Errorf("rewind the staging file: %w", err)
		}

		if _, err := destination.Store.Write(ctx, path, staging); err != nil {
			return Packed{}, fmt.Errorf("write to the destination %s: %w", destination.ID, err)
		}
	}

	result.mark(StepWrite)

	return packed, nil
}

// stream sends straight to the one destination E-030 allows in this mode.
func (s *Service) stream(ctx context.Context, dump io.ReadCloser, path string, result *Result) (Packed, error) {
	defer func() { _ = dump.Close() }()

	if len(s.wiring.Destinations) != 1 {
		return Packed{}, fmt.Errorf("streaming to %d destinations: E-030 allows one", len(s.wiring.Destinations))
	}

	reader, writer := io.Pipe()
	packing := make(chan Packed, 1)
	failed := make(chan error, 1)

	go func() {
		packed, err := s.wiring.Packer.Pack(ctx, writer, dump)
		_ = writer.CloseWithError(err)

		packing <- packed
		failed <- err
	}()

	_, writeErr := s.wiring.Destinations[0].Store.Write(ctx, path, reader)
	_ = reader.CloseWithError(writeErr)

	packed, packErr := <-packing, <-failed

	if err := errors.Join(packErr, writeErr); err != nil {
		return Packed{}, fmt.Errorf("stream the dump of %s: %w", result.Database, err)
	}

	result.mark(StepCompression, StepEncryption, StepWrite)

	return packed, nil
}

// stagingFile opens the buffer of § 4.5, under the state directory so that it
// lives on the volume koffr was given and not on whatever /tmp happens to be.
func (s *Service) stagingFile() (*os.File, error) {
	directory := filepath.Join(s.wiring.StateDirectory, "tmp")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("make the staging directory %s: %w", directory, err)
	}

	file, err := os.CreateTemp(directory, "staging-*.koffr")
	if err != nil {
		return nil, fmt.Errorf("open a staging file in %s: %w", directory, err)
	}

	return file, nil
}

// freshSteps lists the seven steps, none done, the last two declared absent.
func freshSteps() []StepOutcome {
	outcomes := make([]StepOutcome, 0, len(Steps))

	for _, step := range Steps {
		outcome := StepOutcome{Step: step}
		if step == StepVerification || step == StepManifest {
			outcome.Deferred = deferredToLot3
		}

		outcomes = append(outcomes, outcome)
	}

	return outcomes
}

// mark records that a step ran. A deferred step is never marked: it is not
// implemented, and saying otherwise would be the lie P3 is about.
func (r *Result) mark(steps ...Step) {
	for _, step := range steps {
		for index := range r.Steps {
			if r.Steps[index].Step == step && r.Steps[index].Deferred == "" {
				r.Steps[index].Done = true
			}
		}
	}
}
