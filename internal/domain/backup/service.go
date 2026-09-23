package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
const deferredToLot3 = "not in this release: verification arrives at the lot 3"

// manifestSuffix is how a manifest is named: beside its archive, sorted next to
// it in a listing, and found by reading every *.json of a repository — which is
// what E-059 asks of an inventory (`N-4`).
const manifestSuffix = ".json"

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

	// Argv is the command line the dump ran with, **without its password** —
	// the manifest of § 5.3 shows it, because it says what the archive is.
	Argv []string

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

		// Pipeline is what Pack applies, in order. The archive is named after
		// it, so it has to be knowable **before** the first byte is written
		// (`N-3`, A-13).
		Pipeline() []string
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

	// Manifester renders the manifest of a finished backup — step 07 of E-024.
	//
	// The domain declares that it needs **bytes** to deposit, and never what is
	// in them: the shape of a manifest belongs to `catalog`, and `AR-03` keeps
	// the two modules of the domain from knowing each other.
	Manifester interface {
		Render(result Result) ([]byte, error)
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
	Journal      Journal
	History      History
	Manifester   Manifester
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
	if wiring.Journal == nil {
		wiring.Journal = silentJournal{}
	}

	return &Service{wiring: wiring}
}

// journal writes one line of the trace of E-024 (BKP-20).
func (s *Service) journal(request Request, step Step, failed error, facts ...Fact) {
	s.wiring.Journal.Step(JobStep{
		Job: request.JobID, Database: request.Database,
		Step: step, Failed: failed, Facts: facts,
	})
}

// journalDeferred records a step this release does not implement, so that a
// trace of seven steps is never mistaken for seven steps that ran.
func (s *Service) journalDeferred(request Request) {
	for _, step := range []Step{StepVerification} {
		s.wiring.Journal.Step(JobStep{
			Job: request.JobID, Database: request.Database,
			Step: step, Deferred: deferredToLot3,
		})
	}
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

	// Resolution is what the resolver settled on. The manifest of E-058 carries
	// it — the tool, its version and its provenance say what can read the
	// archive back.
	Resolution Resolution
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
	result.Path = ArchivePath(request.Database, request.At, request.JobID,
		ArchiveExtension(resolution.Extension, s.wiring.Packer.Pipeline()))

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

	// BKP-17 — collect what a killed job left behind, before adding to it. Only
	// buffers whose process is gone: another database may be staging right now.
	if err := purgeStaging(s.wiring.StateDirectory); err != nil {
		return result, err
	}

	started := time.Now()

	resolution, err := s.wiring.Resolver.Resolve(ctx, request.Database)
	if err != nil {
		s.journal(request, StepResolution, err)

		return result, fmt.Errorf("resolve a tool for %s: %w", request.Database, err)
	}

	result.Warnings = resolution.Warnings
	result.Resolution = resolution
	result.mark(StepResolution)
	s.journal(request, StepResolution, nil,
		Fact{"engine", resolution.Engine},
		Fact{"server_version", resolution.ServerVersion},
		Fact{"tool", resolution.ToolPath},
		Fact{"tool_version", resolution.ToolVersion},
		Fact{"tool_source", resolution.ToolSource},
	)

	decision, err := s.decideStaging(ctx, request.Database, resolution)
	if err != nil {
		s.journal(request, StepDump, err)

		return result, err
	}

	result.Staging, result.StagingReason, result.StagingForced = decision.Mode, decision.Reason, decision.Forced
	result.Path = ArchivePath(request.Database, request.At, request.JobID,
		ArchiveExtension(resolution.Extension, s.wiring.Packer.Pipeline()))

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

	s.journal(request, StepWrite, nil,
		Fact{"path", result.Path},
		Fact{"destinations", strings.Join(result.Destinations, ",")},
		Fact{"raw_bytes", result.RawBytes},
		Fact{"stored_bytes", result.StoredBytes},
		Fact{"sha256_raw", result.SHA256Raw},
		Fact{"sha256_stored", result.SHA256Stored},
	)
	s.journalDeferred(request)

	// The duration is known before the manifest is written, because the
	// manifest carries it (E-058).
	result.Duration = time.Since(started)

	if err := s.depositManifest(ctx, request, &result); err != nil {
		return result, err
	}

	return result, nil
}

// depositManifest writes the manifest beside the archive, on **every**
// destination — step 07 of E-024, and the last thing a job does.
//
// It is last on purpose: a manifest that arrived first would describe something
// that does not exist yet, and it carries the verification state, which is not
// known before the step that precedes it.
func (s *Service) depositManifest(ctx context.Context, request Request, result *Result) error {
	if s.wiring.Manifester == nil {
		return nil
	}

	rendered, err := s.wiring.Manifester.Render(*result)
	if err != nil {
		s.journal(request, StepManifest, err)

		return fmt.Errorf("render the manifest of %s: %w", result.Database, err)
	}

	beside := result.Path + manifestSuffix

	for _, destination := range s.wiring.Destinations {
		if _, err := destination.Store.Write(ctx, beside, bytes.NewReader(rendered)); err != nil {
			s.journal(request, StepManifest, err, Fact{"destination", destination.ID})

			return fmt.Errorf("deposit the manifest on %s: %w", destination.ID, err)
		}
	}

	result.markEvenIfDeferred(StepManifest)
	s.journal(request, StepManifest, nil, Fact{"path", beside})

	return nil
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
		s.journal(request, StepDump, err)

		return Packed{}, fmt.Errorf("dump %s: %w", request.Database, err)
	}

	result.mark(StepDump)
	s.journal(request, StepDump, nil,
		Fact{"staging", string(mode)},
		Fact{"staging_reason", result.StagingReason},
		Fact{"tool", resolution.ToolPath},
	)

	if mode == Stage {
		return s.stage(ctx, request, dump, path, result)
	}

	return s.stream(ctx, request, dump, path, result)
}

// stage writes the packed stream to a staging file, **closes the dump** — which
// is what ends the transaction on the database, E-055 — and only then sends.
func (s *Service) stage(ctx context.Context, request Request, dump io.ReadCloser, path string, result *Result) (Packed, error) {
	staging, err := s.stagingFile(request.JobID)
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
		s.journal(request, StepCompression, err)

		return Packed{}, fmt.Errorf("pack the dump of %s: %w", result.Database, err)
	}

	result.mark(StepCompression, StepEncryption)
	s.journalPacked(request, packed)

	for _, destination := range s.wiring.Destinations {
		if _, err := staging.Seek(0, io.SeekStart); err != nil {
			return Packed{}, fmt.Errorf("rewind the staging file: %w", err)
		}

		if _, err := destination.Store.Write(ctx, path, staging); err != nil {
			s.journal(request, StepWrite, err, Fact{"destination", destination.ID})

			return Packed{}, fmt.Errorf("write to the destination %s: %w", destination.ID, err)
		}
	}

	result.mark(StepWrite)

	return packed, nil
}

// stream sends straight to the one destination E-030 allows in this mode.
func (s *Service) stream(ctx context.Context, request Request, dump io.ReadCloser, path string, result *Result) (Packed, error) {
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
		s.journal(request, StepCompression, err)

		return Packed{}, fmt.Errorf("stream the dump of %s: %w", result.Database, err)
	}

	result.mark(StepCompression, StepEncryption, StepWrite)
	s.journalPacked(request, packed)

	return packed, nil
}

// stagingFile opens the buffer of § 4.5. Its name carries the pid of this
// process, so that the next job can tell it from a buffer being written now.
func (s *Service) stagingFile(job string) (*os.File, error) {
	directory := filepath.Join(s.wiring.StateDirectory, stagingDirectory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("make the staging directory %s: %w", directory, err)
	}

	path := filepath.Join(directory, StagingFileName(os.Getpid(), job))

	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open a staging file in %s: %w", directory, err)
	}

	return file, nil
}

// journalPacked records what the two middle steps of E-024 produced. They are
// one pass through the pipeline and two steps of the specification, so each
// gets its line with what it can honestly claim.
func (s *Service) journalPacked(request Request, packed Packed) {
	s.journal(request, StepCompression, nil,
		Fact{"pipeline", strings.Join(packed.Pipeline, ",")},
		Fact{"raw_bytes", packed.RawBytes},
		Fact{"stored_bytes", packed.StoredBytes},
	)
	s.journal(request, StepEncryption, nil,
		Fact{"sha256_raw", packed.SHA256Raw},
		Fact{"sha256_stored", packed.SHA256Stored},
	)
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

// markEvenIfDeferred records a step that used to be declared absent and is now
// implemented: it clears the note along with setting the flag.
func (r *Result) markEvenIfDeferred(step Step) {
	for index := range r.Steps {
		if r.Steps[index].Step == step {
			r.Steps[index].Done = true
			r.Steps[index].Deferred = ""
		}
	}
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
