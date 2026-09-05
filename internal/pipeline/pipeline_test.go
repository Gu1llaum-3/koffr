package pipeline_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"runtime"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/pipeline"
	"github.com/Gu1llaum-3/koffr/internal/source"
	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// Every test in this package runs under goleak.
//
// The pipeline's characteristic failure is not a wrong answer, it is a
// goroutine still holding a pipe after the job reported success. Nothing else
// in the codebase needs this as much.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func newIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	return id
}

const key = "sources/s1/logical/B1/dump.pgdump.zst.age"

func request(t *testing.T, src source.Source, store storage.Storage) pipeline.Request {
	t.Helper()
	seal, _ := sealer(t)
	return pipeline.Request{
		Source:   src,
		Executor: nopExecutor{},
		Backup:   source.Request{Kind: source.KindLogical},
		Storage:  store,
		Key:      key,
		Sealer:   seal,
	}
}

// unwrap decrypts and decompresses what the pipeline stored, which is the only
// proof that matters: an object that cannot be read back is not a backup.
func unwrap(t *testing.T, opener interface {
	Open(io.Reader) (io.Reader, error)
}, sealed []byte) []byte {
	t.Helper()
	plain, err := opener.Open(bytes.NewReader(sealed))
	require.NoError(t, err)

	z, err := zstd.NewReader(plain)
	require.NoError(t, err)
	defer z.Close()

	out, err := io.ReadAll(z)
	require.NoError(t, err)
	return out
}

func TestRun_RoundTrip(t *testing.T) {
	defer goleak.VerifyNone(t)

	payload := bytes.Repeat([]byte("koffr backup stream "), 5000)
	store := newFakeStorage()
	seal, opener := sealer(t)

	req := request(t, &fakeSource{reader: bytes.NewReader(payload)}, store)
	req.Sealer = seal

	res, err := pipeline.Run(t.Context(), req)
	require.NoError(t, err)

	stored, ok := store.object(key)
	require.True(t, ok, "nothing was stored")
	assert.Equal(t, payload, unwrap(t, opener, stored))

	assert.Equal(t, int64(len(payload)), res.BytesRead, "plaintext bytes taken from the source")
	assert.Equal(t, int64(len(stored)), res.Object.Size)
	assert.Less(t, res.Object.Size, int64(len(payload)), "repetitive input should compress")
}

// EF-053: the digest covers the ciphertext, so integrity can be checked without
// any key at all. A digest of the plaintext would be useless to a write-only
// node applying retention.
func TestRun_DigestCoversWhatWasStored(t *testing.T) {
	defer goleak.VerifyNone(t)

	store := newFakeStorage()
	res, err := pipeline.Run(t.Context(), request(t,
		&fakeSource{reader: bytes.NewReader(bytes.Repeat([]byte("x"), 100000))}, store))
	require.NoError(t, err)

	stored, ok := store.object(key)
	require.True(t, ok)
	want := sha256.Sum256(stored)
	assert.Equal(t, hex.EncodeToString(want[:]), res.SHA256)
}

func TestRun_RecordsRecipientsAndCodec(t *testing.T) {
	defer goleak.VerifyNone(t)

	store := newFakeStorage()
	res, err := pipeline.Run(t.Context(), request(t,
		&fakeSource{reader: bytes.NewReader([]byte("small"))}, store))
	require.NoError(t, err)

	assert.Len(t, res.Recipients, 2, "EF-051: every object is sealed for two recipients")
	assert.Equal(t, "zstd", res.Codec)
}

// A source that already compressed its output must not be compressed again.
// PostgreSQL can compress server-side (EF-011), and paying for zstd on top
// would cost CPU for nothing.
func TestRun_DoesNotCompressAnAlreadyCompressedStream(t *testing.T) {
	defer goleak.VerifyNone(t)

	payload := bytes.Repeat([]byte("already compressed"), 5000)
	store := newFakeStorage()

	src := &fakeSource{reader: bytes.NewReader(payload)}
	req := request(t, src, store)
	req.SourceCodec = source.CodecZstd

	res, err := pipeline.Run(t.Context(), req)
	require.NoError(t, err)
	assert.Equal(t, "zstd", res.Codec, "the codec recorded is the one the bytes carry")

	// The bytes must arrive unchanged: sealed, not re-compressed.
	_, opener := sealer(t)
	_ = opener
	assert.Greater(t, res.Object.Size, int64(len(payload)/2),
		"a second compression pass would have shrunk incompressible input further")
}

func TestRun_SidecarsAreReturned(t *testing.T) {
	defer goleak.VerifyNone(t)

	store := newFakeStorage()
	src := &fakeSource{
		reader:   bytes.NewReader([]byte("dump")),
		sidecars: map[string][]byte{"globals.sql": []byte("CREATE ROLE probe;")},
	}
	res, err := pipeline.Run(t.Context(), request(t, src, store))
	require.NoError(t, err)
	assert.Equal(t, []byte("CREATE ROLE probe;"), res.Sidecars["globals.sql"])
}

// ENF-001: memory must be bounded and independent of the database's size. A
// pipeline that buffered would pass every correctness test above and then fail
// on the first real backup.
func TestRun_MemoryIsBounded(t *testing.T) {
	defer goleak.VerifyNone(t)

	const size = 256 << 20
	// A ceiling in absolute terms, not relative to the stream: that is the whole
	// claim of ENF-001. zstd's window and age's chunk buffers account for a few
	// megabytes; anything approaching the stream's size means it is being held.
	const ceiling = 64 << 20

	store := &discardStorage{}
	req := request(t, &fakeSource{reader: io.LimitReader(&patternReader{state: 1}, size)}, store)

	runtime.GC()
	res, err := pipeline.Run(t.Context(), req)
	require.NoError(t, err)
	assert.Equal(t, int64(size), res.BytesRead)

	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	assert.Less(t, int64(after.HeapAlloc), int64(ceiling),
		"the pipeline is holding the stream rather than passing it through")
	t.Logf("%d MiB streamed, %d MiB stored, heap %d MiB",
		size>>20, store.written.Load()>>20, after.HeapAlloc>>20)
}

// --- Concurrency invariants: one test per invariant ---

// When storage fails, the source must be torn down and the failure attributed
// to storage. A disk full on S3 reported as "pg_dump failed" sends an operator
// to the wrong machine.
func TestRun_StorageFailureKillsSourceAndIsAttributed(t *testing.T) {
	defer goleak.VerifyNone(t)

	store := newFakeStorage()
	store.failAfter = 64 << 10

	src := &fakeSource{reader: io.LimitReader(&patternReader{state: 1}, 64<<20)}
	_, err := pipeline.Run(t.Context(), request(t, src, store))

	require.Error(t, err)
	assert.Equal(t, catalog.ErrClassStorage, pipeline.ClassOf(err))
	assert.ErrorIs(t, err, errStorageDied, "the storage error itself must survive")
	assert.True(t, src.closed.Load(), "the source was left running after storage failed")
	assert.Zero(t, store.count(), "a failed upload must leave nothing behind")
}

// A source that exits non-zero is the source's failure, and is retried with
// backoff rather than treated as a configuration problem (ENF-011).
func TestRun_SourceFailureIsAttributed(t *testing.T) {
	defer goleak.VerifyNone(t)

	store := newFakeStorage()
	src := &fakeSource{
		reader:  &errAfterReader{data: bytes.Repeat([]byte("y"), 128<<10), err: errSourceDied},
		waitErr: errSourceDied,
	}

	_, err := pipeline.Run(t.Context(), request(t, src, store))
	require.Error(t, err)
	assert.Equal(t, catalog.ErrClassSource, pipeline.ClassOf(err))
	assert.ErrorIs(t, err, errSourceDied)
	assert.Zero(t, store.count(), "a truncated dump must not be stored as a backup")
}

// A source that cannot be started at all is a configuration problem: retrying
// it on a schedule would only repeat the same failure.
func TestRun_SourceOpenFailureIsConfiguration(t *testing.T) {
	defer goleak.VerifyNone(t)

	_, err := pipeline.Run(t.Context(),
		request(t, failingSource{err: errSourceDied}, newFakeStorage()))
	require.Error(t, err)
	assert.Equal(t, catalog.ErrClassConfig, pipeline.ClassOf(err))
}

// EF-095, first threshold: a source that never produces a byte must not hold a
// job open forever. Databases can be slow to answer, so this budget is separate
// from the one between bytes.
func TestRun_FirstByteTimeout(t *testing.T) {
	defer goleak.VerifyNone(t)

	store := newFakeStorage()
	reader := newBlockingReader(nil)
	defer reader.Release()

	req := request(t, &fakeSource{reader: reader}, store)
	req.FirstByteTimeout = 200 * time.Millisecond
	req.StallTimeout = time.Hour // must not be the one that fires

	start := time.Now()
	_, err := pipeline.Run(t.Context(), req)
	require.Error(t, err)

	assert.Equal(t, catalog.ErrClassStalled, pipeline.ClassOf(err))
	assert.Less(t, time.Since(start), 10*time.Second)
	assert.Contains(t, err.Error(), "first byte",
		"the message must say which budget expired, or the operator tunes the wrong one")
}

// EF-095, second threshold: an upload that stops moving must fail rather than
// hang. This is the failure a health check cannot see.
func TestRun_StallTimeout(t *testing.T) {
	defer goleak.VerifyNone(t)

	store := newFakeStorage()
	store.blockAfter = 32 << 10
	defer close(store.unblock)

	req := request(t, &fakeSource{reader: io.LimitReader(&patternReader{state: 1}, 64<<20)}, store)
	req.FirstByteTimeout = time.Hour // must not be the one that fires
	req.StallTimeout = 300 * time.Millisecond

	start := time.Now()
	_, err := pipeline.Run(t.Context(), req)
	require.Error(t, err)

	assert.Equal(t, catalog.ErrClassStalled, pipeline.ClassOf(err))
	assert.Less(t, time.Since(start), 10*time.Second)
	assert.Contains(t, err.Error(), "stall")
}

// A job the operator cancelled is not a failure to diagnose. Reporting it as a
// source failure would have the scheduler retry something nobody asked for.
func TestRun_CancellationIsNotMisattributed(t *testing.T) {
	defer goleak.VerifyNone(t)

	store := newFakeStorage()
	store.blockAfter = 32 << 10
	defer close(store.unblock)

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	req := request(t, &fakeSource{reader: io.LimitReader(&patternReader{state: 1}, 64<<20)}, store)
	_, err := pipeline.Run(ctx, req)

	require.Error(t, err)
	assert.Equal(t, catalog.ErrClassCanceled, pipeline.ClassOf(err))
}

// The five-actor precedence, minus the manifest branch that arrives in M2.
//
// When several things fail at once -- and they do, because each failure causes
// the others -- the one reported must be the cause, not whichever goroutine
// noticed first. Storage dying makes the source die too; blaming the source
// sends an operator to the wrong machine.
func TestRun_AttributionPrefersTheCause(t *testing.T) {
	defer goleak.VerifyNone(t)

	store := newFakeStorage()
	store.failAfter = 32 << 10

	// The source will also fail, because storage failing starves it.
	src := &fakeSource{
		reader:  io.LimitReader(&patternReader{state: 1}, 64<<20),
		waitErr: errSourceDied,
	}

	_, err := pipeline.Run(t.Context(), request(t, src, store))
	require.Error(t, err)
	assert.Equal(t, catalog.ErrClassStorage, pipeline.ClassOf(err),
		"storage was the cause; the source failed because of it")
}

// The stall watcher outranks everything: it is the only actor that fires when
// nothing else has noticed anything is wrong.
func TestRun_StallOutranksOtherFailures(t *testing.T) {
	defer goleak.VerifyNone(t)

	store := newFakeStorage()
	store.blockAfter = 32 << 10
	defer close(store.unblock)

	src := &fakeSource{
		reader:  io.LimitReader(&patternReader{state: 1}, 64<<20),
		waitErr: errSourceDied,
	}
	req := request(t, src, store)
	req.StallTimeout = 200 * time.Millisecond

	_, err := pipeline.Run(t.Context(), req)
	require.Error(t, err)
	assert.Equal(t, catalog.ErrClassStalled, pipeline.ClassOf(err))
}

// The teardown path that never ran the process. Returning early must release
// everything already built, or a configuration error at 3 AM leaks a goroutine
// per attempt for as long as the scheduler keeps retrying.
func TestRun_EarlyReturnLeaksNothing(t *testing.T) {
	defer goleak.VerifyNone(t)

	for range 20 {
		_, err := pipeline.Run(t.Context(),
			request(t, failingSource{err: errSourceDied}, newFakeStorage()))
		require.Error(t, err)
	}
}

// A source whose Close hangs must not hang the job. Step 5 established that an
// uncooperative target is possible; the pipeline must survive one.
func TestRun_SlowSourceCloseDoesNotHangTheJob(t *testing.T) {
	defer goleak.VerifyNone(t)

	store := newFakeStorage()
	src := &fakeSource{
		reader:       bytes.NewReader([]byte("payload")),
		blockOnClose: 300 * time.Millisecond,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := pipeline.Run(t.Context(), request(t, src, store))
		assert.NoError(t, err)
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Run blocked on a slow source teardown")
	}
	assert.Equal(t, int32(1), src.closeCount.Load(), "the stream must be closed exactly once")
}

func TestRun_RejectsIncompleteRequest(t *testing.T) {
	defer goleak.VerifyNone(t)

	base := request(t, &fakeSource{reader: bytes.NewReader(nil)}, newFakeStorage())

	for name, mutate := range map[string]func(*pipeline.Request){
		"no source":  func(r *pipeline.Request) { r.Source = nil },
		"no storage": func(r *pipeline.Request) { r.Storage = nil },
		"no sealer":  func(r *pipeline.Request) { r.Sealer = nil },
		"no key":     func(r *pipeline.Request) { r.Key = "" },
	} {
		t.Run(name, func(t *testing.T) {
			req := base
			mutate(&req)
			_, err := pipeline.Run(t.Context(), req)
			require.Error(t, err)
			assert.Equal(t, catalog.ErrClassConfig, pipeline.ClassOf(err))
		})
	}
}

// patternReader is an endless, cheap, incompressible source of bytes.
//
// Incompressible on purpose. A repeating pattern collapses under zstd, so a
// test that expects the stored object to reach a given size would never get
// there -- which is exactly how the storage-failure test first passed while
// testing nothing.
type patternReader struct{ state uint64 }

func (r *patternReader) Read(p []byte) (int, error) {
	for i := range p {
		// xorshift64: cheap, deterministic, and not compressible.
		r.state ^= r.state << 13
		r.state ^= r.state >> 7
		r.state ^= r.state << 17
		if r.state == 0 {
			r.state = 0x9e3779b97f4a7c15
		}
		p[i] = byte(r.state)
	}
	return len(p), nil
}
