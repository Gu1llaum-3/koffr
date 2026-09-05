package pipeline_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"iter"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/crypto"
	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/source"
	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// Doubles, not mocks.
//
// A mock checks that the code called what we expected; a double checks that the
// code works. For a pipeline whose failures are deadlocks and leaked
// goroutines, only the second is worth anything: no assertion about call order
// would have caught the SSH reaper deadlock of step 5.

var (
	errSourceDied  = errors.New("source died")
	errStorageDied = errors.New("storage died")
)

// fakeSource hands out a stream the test controls.
type fakeSource struct {
	reader io.Reader
	// waitErr is what the underlying process reports, as an exit status would.
	waitErr error
	// blockOnClose delays Close, standing in for a process that will not die.
	blockOnClose time.Duration

	closed     atomic.Bool
	closeCount atomic.Int32
	sidecars   map[string][]byte
}

func (f *fakeSource) Probe(context.Context, executor.Executor) (source.Info, error) {
	return source.Info{Engine: source.EnginePostgreSQL, Kinds: []source.Kind{source.KindLogical}}, nil
}

func (f *fakeSource) Open(context.Context, executor.Executor, source.Request) (*source.Stream, error) {
	return &source.Stream{
		Reader: f.reader,
		Codec:  source.CodecNone,
		Sidecars: func() (map[string][]byte, error) {
			return f.sidecars, nil
		},
		Result: func() source.Result { return source.Result{} },
		Closer: closerFunc(func() error {
			f.closed.Store(true)
			f.closeCount.Add(1)
			if f.blockOnClose > 0 {
				time.Sleep(f.blockOnClose)
			}
			// A real source's Close kills the process, which ends any read in
			// progress. A double that did not do this would let the pipeline
			// pass a test it should fail.
			if b, ok := f.reader.(*blockingReader); ok {
				b.Release()
			}
			return f.waitErr
		}),
	}, nil
}

type closerFunc func() error

func (c closerFunc) Close() error { return c() }

// failingSource cannot be opened at all.
type failingSource struct{ err error }

func (f failingSource) Probe(context.Context, executor.Executor) (source.Info, error) {
	return source.Info{}, f.err
}
func (f failingSource) Open(context.Context, executor.Executor, source.Request) (*source.Stream, error) {
	return nil, f.err
}

// errAfterReader yields n bytes and then fails, as pg_dump dying halfway does.
type errAfterReader struct {
	data []byte
	at   int
	err  error
}

func (r *errAfterReader) Read(p []byte) (int, error) {
	if r.at >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.at:])
	r.at += n
	return n, nil
}

// blockingReader yields a prefix and then blocks until released, which is what
// a source that has silently wedged looks like from here.
type blockingReader struct {
	prefix      []byte
	at          int
	release     chan struct{}
	releaseOnce sync.Once
}

func newBlockingReader(prefix []byte) *blockingReader {
	return &blockingReader{prefix: prefix, release: make(chan struct{})}
}

func (r *blockingReader) Read(p []byte) (int, error) {
	if r.at < len(r.prefix) {
		n := copy(p, r.prefix[r.at:])
		r.at += n
		return n, nil
	}
	<-r.release
	return 0, io.EOF
}

// Release is idempotent: teardown paths call it more than once.
func (r *blockingReader) Release() { r.releaseOnce.Do(func() { close(r.release) }) }

// fakeStorage is an in-memory object store that can be told how to fail.
type fakeStorage struct {
	mu        sync.Mutex
	objects   map[string][]byte
	progress  []int64
	putCalls  atomic.Int32
	failAfter int64 // bytes read before Put fails; 0 means never
	// blockAfter stalls the upload after this many bytes, which is the failure
	// the byte-stall watcher exists for (EF-095).
	blockAfter int64
	unblock    chan struct{}
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{objects: make(map[string][]byte), unblock: make(chan struct{})}
}

func (s *fakeStorage) Put(ctx context.Context, key string, r io.Reader, opts storage.PutOptions) (storage.ObjectInfo, error) {
	s.putCalls.Add(1)

	var buf bytes.Buffer
	chunk := make([]byte, 32<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return storage.ObjectInfo{}, err
		}
		n, err := r.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
			total += int64(n)
			if opts.OnProgress != nil {
				opts.OnProgress(total)
			}
			s.mu.Lock()
			s.progress = append(s.progress, total)
			s.mu.Unlock()

			if s.failAfter > 0 && total >= s.failAfter {
				return storage.ObjectInfo{}, errStorageDied
			}
			if s.blockAfter > 0 && total >= s.blockAfter {
				select {
				case <-s.unblock:
				case <-ctx.Done():
					return storage.ObjectInfo{}, ctx.Err()
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// The source's error must survive: it is what tells a source
			// failure from a storage one (ENF-011).
			return storage.ObjectInfo{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = buf.Bytes()
	return storage.ObjectInfo{Key: key, Size: total, LastModified: time.Now()}, nil
}

func (s *fakeStorage) object(key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[key]
	return b, ok
}

func (s *fakeStorage) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.objects)
}

func (s *fakeStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := s.object(key)
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (s *fakeStorage) GetRange(context.Context, string, int64, int64) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (s *fakeStorage) Stat(_ context.Context, key string) (storage.ObjectInfo, error) {
	b, ok := s.object(key)
	if !ok {
		return storage.ObjectInfo{}, storage.ErrNotFound
	}
	return storage.ObjectInfo{Key: key, Size: int64(len(b))}, nil
}

func (s *fakeStorage) List(context.Context, string) iter.Seq2[storage.ObjectInfo, error] {
	return func(func(storage.ObjectInfo, error) bool) {}
}

func (s *fakeStorage) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

func (s *fakeStorage) Capabilities() storage.Capabilities {
	return storage.Capabilities{Multipart: true, RangeReads: true}
}

var _ storage.Storage = (*fakeStorage)(nil)

// discardStorage counts what it is given and keeps none of it.
//
// It exists for the memory test. Measuring the pipeline's footprint against a
// double that holds the whole object measures the double: a bytes.Buffer
// doubles as it grows, so 256 MiB of backup can transiently occupy 512 MiB that
// the pipeline never touched. With nothing retained, whatever is on the heap is
// the pipeline's.
type discardStorage struct {
	fakeStorage
	written atomic.Int64
}

func (s *discardStorage) Put(ctx context.Context, key string, r io.Reader, opts storage.PutOptions) (storage.ObjectInfo, error) {
	n, err := io.Copy(io.Discard, &progressCounter{r: r, onProgress: opts.OnProgress})
	s.written.Store(n)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	return storage.ObjectInfo{Key: key, Size: n}, nil
}

type progressCounter struct {
	r          io.Reader
	total      int64
	onProgress func(int64)
}

func (c *progressCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.total += int64(n)
		if c.onProgress != nil {
			c.onProgress(c.total)
		}
	}
	return n, err
}

// nopExecutor stands where a real transport would. The pipeline never touches
// it: reaching the database is the source's business.
type nopExecutor struct{}

func (nopExecutor) Dial(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("not implemented")
}
func (nopExecutor) Start(context.Context, executor.Command) (executor.Process, error) {
	return nil, errors.New("not implemented")
}
func (nopExecutor) Capabilities() executor.Capabilities {
	return executor.Capabilities{CanDial: true, Direct: true, Target: "fake"}
}
func (nopExecutor) Close() error { return nil }

// sealer returns an age sealer and the identity that opens it.
func sealer(t *testing.T) (crypto.Sealer, crypto.Opener) {
	t.Helper()
	operational := newIdentity(t)
	recovery := newIdentity(t)

	s, err := crypto.NewSealer([]string{
		operational.Recipient().String(),
		recovery.Recipient().String(),
	})
	require.NoError(t, err)
	o, err := crypto.NewOpener(operational.String())
	require.NoError(t, err)
	return s, o
}
