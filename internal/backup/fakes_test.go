package backup_test

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/source"
)

// fakeSource stands in for PostgreSQL. What is under test here is the
// orchestration around a source, not the source itself, which has its own
// integration tests against a real server.
type fakeSource struct {
	payload  []byte
	sidecars map[string][]byte
	kinds    []source.Kind
	probeErr error

	// blockUntil holds Open open, so a second job can be attempted while the
	// first still holds the lock.
	blockUntil chan struct{}
	opened     chan struct{}
	once       sync.Once
}

func (f *fakeSource) Probe(context.Context, executor.Executor) (source.Info, error) {
	if f.probeErr != nil {
		return source.Info{}, f.probeErr
	}
	kinds := f.kinds
	if kinds == nil {
		kinds = []source.Kind{source.KindLogical}
	}
	return source.Info{
		Engine:        source.EnginePostgreSQL,
		ServerVersion: "17.11",
		Kinds:         kinds,
		Databases:     []string{"probe_database"},
	}, nil
}

func (f *fakeSource) Open(context.Context, executor.Executor, source.Request) (*source.Stream, error) {
	f.once.Do(func() { f.opened = make(chan struct{}) })
	close(f.opened)
	if f.blockUntil != nil {
		<-f.blockUntil
	}
	return &source.Stream{
		Reader:   bytes.NewReader(f.payload),
		Codec:    source.CodecNone,
		Sidecars: func() (map[string][]byte, error) { return f.sidecars, nil },
		Result:   func() source.Result { return source.Result{} },
		Closer:   closerFunc(func() error { return nil }),
	}, nil
}

// waitUntilOpen blocks until the source has been opened, which is after the
// lock has been taken.
func (f *fakeSource) waitUntilOpen(t *testing.T) {
	t.Helper()
	f.once.Do(func() { f.opened = make(chan struct{}) })
	select {
	case <-f.opened:
	case <-t.Context().Done():
		t.Fatal("the source was never opened")
	}
}

type closerFunc func() error

func (c closerFunc) Close() error { return c() }

func decompress(t *testing.T, b []byte) []byte {
	t.Helper()
	z, err := zstd.NewReader(bytes.NewReader(b))
	if err != nil {
		return b
	}
	defer z.Close()
	out, err := io.ReadAll(z)
	if err != nil {
		return b
	}
	require.NotNil(t, out)
	return out
}
