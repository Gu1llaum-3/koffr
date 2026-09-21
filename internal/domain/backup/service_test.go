package backup_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/domain/backup"
)

// BKP-06 — the seven steps of E-024 happen **in that order**, and the result
// says so. The last two are not in this release: they are reported as deferred,
// named, and never as done. A job that claims to have verified what it did not
// is the failure mode § 2 P3 exists to prevent.
func TestBKP06TheSevenStepsHappenInTheOrderOfTheSpecification(t *testing.T) {
	world := newWorld(t)

	result, err := world.service().Run(t.Context(), request())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []backup.Step{
		backup.StepResolution, backup.StepDump, backup.StepCompression,
		backup.StepEncryption, backup.StepWrite, backup.StepVerification, backup.StepManifest,
	}

	got := make([]backup.Step, 0, len(result.Steps))
	for _, outcome := range result.Steps {
		got = append(got, outcome.Step)
	}

	if !slices.Equal(got, want) {
		t.Fatalf("steps = %v,\nwant %v — the order of E-024", got, want)
	}

	for _, outcome := range result.Steps[:5] {
		if !outcome.Done {
			t.Errorf("the step %q did not run", outcome.Step)
		}
	}

	for _, outcome := range result.Steps[5:] {
		if outcome.Done {
			t.Errorf("the step %q reported itself done, and it is not implemented", outcome.Step)
		}
		if !strings.Contains(outcome.Deferred, "3") {
			t.Errorf("the step %q does not say which lot brings it: %q", outcome.Step, outcome.Deferred)
		}
	}
}

// BKP-06 — a step that fails stops the job, and the steps after it are **not**
// reported as done. An archive of nothing looks exactly like an archive.
func TestBKP06AFailedStepStopsTheJob(t *testing.T) {
	world := newWorld(t)
	world.dumper.fail = errors.New("pg_dump exited with 1: connection refused")

	result, err := world.service().Run(t.Context(), request())
	if err == nil {
		t.Fatal("a job whose dump failed reported success")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("the failure does not carry what the tool said:\n%v", err)
	}

	for _, outcome := range result.Steps {
		if outcome.Step == backup.StepResolution {
			continue
		}
		if outcome.Done {
			t.Errorf("the step %q ran although the dump failed", outcome.Step)
		}
	}

	if len(world.destination.written) != 0 {
		t.Errorf("a failed job wrote %d archives", len(world.destination.written))
	}
}

// E-024 and E-053 — in `stage`, the archive reaches **every** destination, and
// they receive the same bytes. The mode actually applied is on the result, for
// the manifest of E-053 and for `doctor`.
func TestTheArchiveReachesEveryDestinationInStageMode(t *testing.T) {
	world := newWorld(t)
	world.second = newDestination()

	result, err := world.service().Run(t.Context(), request())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Staging != backup.Stage {
		t.Fatalf("staging = %q, want stage — two destinations impose it (%s)", result.Staging, result.StagingReason)
	}
	if result.StagingReason == "" {
		t.Error("nothing explains the mode that was applied")
	}

	first, second := world.destination.only(t), world.second.only(t)
	if !bytes.Equal(first, second) {
		t.Errorf("the two destinations got different archives: %d and %d bytes", len(first), len(second))
	}
	if len(first) == 0 {
		t.Error("the archive is empty")
	}
	if result.StoredBytes != int64(len(first)) {
		t.Errorf("the result says %d bytes stored, the destination holds %d", result.StoredBytes, len(first))
	}
}

// E-024 — in `stream`, the one destination still gets the whole archive. There
// is no staging file to fall back on, which is exactly what the mode trades.
func TestTheArchiveReachesTheDestinationInStreamMode(t *testing.T) {
	world := newWorld(t)
	world.staging = backup.Stream

	result, err := world.service().Run(t.Context(), request())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Staging != backup.Stream {
		t.Fatalf("staging = %q, want stream (%s)", result.Staging, result.StagingReason)
	}
	if got := world.destination.only(t); len(got) == 0 {
		t.Error("the destination got nothing")
	}
}

// BKP-01 inside the use case — a second job on the same database is refused
// while the first holds the lock, and the lock is given back when it ends.
func TestASecondJobOnTheSameDatabaseIsRefusedByTheUseCase(t *testing.T) {
	world := newWorld(t)
	service := world.service()

	release := make(chan struct{})
	world.dumper.hold = release

	var (
		waiting sync.WaitGroup
		first   error
	)

	waiting.Add(1)

	go func() {
		defer waiting.Done()

		_, first = service.Run(context.WithoutCancel(t.Context()), request())
	}()

	world.dumper.started(t)

	if _, err := service.Run(t.Context(), request()); !errors.Is(err, backup.ErrLocked) {
		t.Errorf("the second job got %v, want %v", err, backup.ErrLocked)
	}

	close(release)
	waiting.Wait()

	if first != nil {
		t.Fatalf("the first job failed: %v", first)
	}

	// And once it is over, the database is free again.
	if _, err := service.Run(t.Context(), request()); err != nil {
		t.Errorf("the lock was not given back: %v", err)
	}
}

// ---- the world the use case runs in --------------------------------------

const dumped = "PGDMP a database, pretend"

func request() backup.Request {
	return backup.Request{
		Database: "boutique-prod",
		JobID:    "01JQ8F3K2M7X9P4W",
		At:       time.Date(2026, 9, 22, 2, 0, 3, 0, time.UTC),
	}
}

type world struct {
	t           *testing.T
	stateDir    string
	staging     backup.Mode
	resolver    *fakeResolver
	dumper      *fakeDumper
	packer      *fakePacker
	capacity    *fakeCapacity
	history     *fakeHistory
	destination *fakeDestination
	second      *fakeDestination
}

func newWorld(t *testing.T) *world {
	t.Helper()

	return &world{
		t:           t,
		stateDir:    t.TempDir(),
		staging:     backup.Auto,
		resolver:    &fakeResolver{},
		dumper:      &fakeDumper{running: make(chan struct{})},
		packer:      &fakePacker{},
		capacity:    &fakeCapacity{free: 100 << 30, database: 1 << 30},
		history:     &fakeHistory{},
		destination: newDestination(),
	}
}

func (w *world) service() *backup.Service {
	destinations := []backup.Destination{{ID: "local", Store: w.destination}}
	if w.second != nil {
		destinations = append(destinations, backup.Destination{ID: "offsite", Store: w.second})
	}

	return backup.NewService(backup.Wiring{
		StateDirectory: w.stateDir,
		Staging:        w.staging,
		Resolver:       w.resolver,
		Dumper:         w.dumper,
		Packer:         w.packer,
		Capacity:       w.capacity,
		History:        w.history,
		Destinations:   destinations,
	})
}

type fakeResolver struct{ fail error }

func (f *fakeResolver) Resolve(context.Context, string) (backup.Resolution, error) {
	if f.fail != nil {
		return backup.Resolution{}, f.fail
	}

	return backup.Resolution{
		Engine: "postgresql", ServerVersion: "16.10",
		ToolPath: "/usr/bin/pg_dump", ToolVersion: "16.10", ToolSource: "host",
		Extension: "pgc",
	}, nil
}

type fakeDumper struct {
	fail     error
	hold     chan struct{}
	running  chan struct{}
	announce sync.Once
}

func (f *fakeDumper) Dump(context.Context, backup.Resolution, backup.Request) (io.ReadCloser, error) {
	if f.fail != nil {
		return nil, f.fail
	}

	if f.hold != nil {
		f.announce.Do(func() { close(f.running) })
		<-f.hold
	}

	return io.NopCloser(strings.NewReader(dumped)), nil
}

// started waits until the dump of the first job is under way, so the second job
// really meets a held lock rather than racing it.
func (f *fakeDumper) started(t *testing.T) {
	t.Helper()

	select {
	case <-f.running:
	case <-time.After(5 * time.Second):
		t.Fatal("the first job never started dumping")
	}
}

// fakePacker stands in for internal/pipeline: what it does for real is proven
// in its own package, and end to end at wave 6.
type fakePacker struct{ fail error }

func (f *fakePacker) Pack(_ context.Context, into io.Writer, from io.Reader) (backup.Packed, error) {
	if f.fail != nil {
		return backup.Packed{}, f.fail
	}

	written, err := io.Copy(into, from)
	if err != nil {
		return backup.Packed{}, err
	}

	return backup.Packed{
		RawBytes: written, StoredBytes: written,
		SHA256Raw: "raw", SHA256Stored: "stored",
		Pipeline: []string{"zstd:3", "age:x25519"},
	}, nil
}

type fakeCapacity struct{ free, database int64 }

func (f *fakeCapacity) FreeBytes(context.Context) (int64, error) { return f.free, nil }

func (f *fakeCapacity) DatabaseBytes(context.Context, string) (int64, error) {
	return f.database, nil
}

type fakeHistory struct{ last *backup.PreviousBackup }

func (f *fakeHistory) LastSuccessful(context.Context, string) (*backup.PreviousBackup, error) {
	return f.last, nil
}

type fakeDestination struct {
	mutex   sync.Mutex
	written map[string][]byte
}

func newDestination() *fakeDestination {
	return &fakeDestination{written: map[string][]byte{}}
}

func (f *fakeDestination) Write(_ context.Context, path string, from io.Reader) (int64, error) {
	contents, err := io.ReadAll(from)
	if err != nil {
		return 0, err
	}

	f.mutex.Lock()
	defer f.mutex.Unlock()

	f.written[path] = contents

	return int64(len(contents)), nil
}

func (f *fakeDestination) Read(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not needed here")
}

func (f *fakeDestination) List(context.Context, string) ([]backup.Entry, error) { return nil, nil }
func (f *fakeDestination) Delete(context.Context, string) error                 { return nil }
func (f *fakeDestination) Check(context.Context) error                          { return nil }

// only returns the single archive this destination holds.
func (f *fakeDestination) only(t *testing.T) []byte {
	t.Helper()

	f.mutex.Lock()
	defer f.mutex.Unlock()

	if len(f.written) != 1 {
		t.Fatalf("the destination holds %d archives, want 1", len(f.written))
	}

	for _, contents := range f.written {
		return contents
	}

	return nil
}

// E-103b — a dry run says what the job would do and **writes nothing**: no
// archive, no staging file, and no lock. It stops before the dump, which is the
// first thing that costs a production server anything.
func TestADryRunSaysWhatItWouldDoAndWritesNothing(t *testing.T) {
	world := newWorld(t)
	world.second = newDestination()

	planned, err := world.service().Plan(t.Context(), request())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if planned.Staging != backup.Stage {
		t.Errorf("staging = %q, want stage — two destinations impose it", planned.Staging)
	}
	if planned.Path == "" {
		t.Error("the plan does not say where the archive would go")
	}
	if want := []string{"local", "offsite"}; !slices.Equal(planned.Destinations, want) {
		t.Errorf("destinations = %v, want %v", planned.Destinations, want)
	}

	for _, outcome := range planned.Steps {
		if outcome.Step == backup.StepResolution {
			continue
		}
		if outcome.Done {
			t.Errorf("a dry run reported the step %q as done", outcome.Step)
		}
	}

	if len(world.destination.written) != 0 || len(world.second.written) != 0 {
		t.Error("a dry run wrote an archive")
	}

	entries, err := os.ReadDir(filepath.Join(world.stateDir, "locks"))
	if err == nil && len(entries) != 0 {
		t.Errorf("a dry run left %d locks behind", len(entries))
	}
}
