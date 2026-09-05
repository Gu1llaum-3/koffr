// Package pipeline carries a backup from a source to a destination.
//
// It is the shortest description of what Koffr does:
//
//	source -> compress -> seal -> hash and count -> storage
//
// Every stage is a reader or a writer, so the memory used is the buffers', not
// the database's (ENF-001). Nothing is ever held whole.
//
// The hard part is not the happy path, it is what happens when one stage fails
// while the others are still running. Each of the invariants below corresponds
// to a real deadlock or a real misdiagnosis, and each has a test of its own:
//
//   - The byte counter measures bytes reaching storage, not bytes leaving the
//     source. A stalled upload is exactly the failure worth detecting, and a
//     counter on the source side would keep ticking while nothing landed.
//   - Failures are attributed by cause, not by whoever noticed first. Storage
//     dying starves the source, so the source dies too; reporting that would
//     send an operator to the wrong machine.
//   - Cancellation carries a typed cause, so "context canceled" never reaches
//     an operator as the explanation.
//   - Teardown is symmetric and runs on every path, including the early return
//     where the source never opened.
//
// M1 scope: one stream, one object. The tee into a manifest reconstruction that
// physical backups need arrives in M2, when a test asks for it.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/crypto"
	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/source"
	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// Default budgets for the byte-stall watcher (EF-095).
//
// They are separate on purpose. A database can take minutes to answer a large
// dump request, so the wait for the first byte is generous; once bytes are
// flowing, a long gap means something is wrong rather than slow.
const (
	DefaultFirstByteTimeout = 15 * time.Minute
	DefaultStallTimeout     = 5 * time.Minute
	defaultCompressionLevel = zstd.SpeedDefault
)

// Request is one backup to carry.
type Request struct {
	Source   source.Source
	Executor executor.Executor
	Backup   source.Request

	Storage storage.Storage
	Key     string
	Sealer  crypto.Sealer

	// SourceCodec says what compression the source already applied. When it is
	// anything but none, the compression stage is a pass-through: PostgreSQL
	// can compress server-side (EF-011) and paying for zstd on top costs CPU
	// for nothing.
	SourceCodec source.Codec

	PartSize         int64
	FirstByteTimeout time.Duration
	StallTimeout     time.Duration

	// OnProgress reports bytes reaching storage.
	OnProgress func(int64)
}

// Result describes what was stored.
type Result struct {
	Object storage.ObjectInfo
	// SHA256 covers the ciphertext, so integrity is checkable without a key
	// (EF-053).
	SHA256 string
	// BytesRead is the plaintext taken from the source, which is what a
	// compression ratio is measured against.
	BytesRead    int64
	Codec        string
	Recipients   []string
	SourceResult source.Result
	Sidecars     map[string][]byte
}

// Error carries the classification that decides whether a job is retried.
//
// The class is the point (ENF-011): the message is for a human, the class is
// for the scheduler, and conflating them is how a configuration mistake gets
// retried every hour for a week.
type Error struct {
	Class catalog.ErrorClass
	Op    string
	Err   error
}

func (e *Error) Error() string { return e.Op + ": " + e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// ClassOf reports how an error should be treated. An error from outside this
// package is a source failure: unclassified means "unknown", and retrying an
// unknown failure is safer than declaring it permanent.
func ClassOf(err error) catalog.ErrorClass {
	var e *Error
	if errors.As(err, &e) {
		return e.Class
	}
	return catalog.ErrClassSource
}

func classify(class catalog.ErrorClass, op string, err error) error {
	return &Error{Class: class, Op: op, Err: err}
}

// Sentinel causes, used to tell apart the reasons the internal context was
// cancelled. They never reach an operator unwrapped.
var (
	errFirstByteTimeout = errors.New("no first byte from the source within the budget")
	errStalled          = errors.New("no bytes reached storage within the stall budget")
)

// Run carries one backup from source to storage.
func Run(ctx context.Context, req Request) (Result, error) {
	if err := req.validate(); err != nil {
		return Result{}, err
	}
	req.applyDefaults()

	// A cause-carrying context is what lets the failure be attributed. Every
	// stage cancels with the reason it failed, and the reason outlives the
	// cancellation.
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	stream, err := req.Source.Open(runCtx, req.Executor, req.Backup)
	if err != nil {
		// A source that cannot even be opened is a configuration problem:
		// retrying it on a schedule repeats the same failure (ENF-011).
		return Result{}, classify(catalog.ErrClassConfig, "open source", err)
	}

	// Closed on every path from here, including panics: the source holds a
	// process, a tunnel and a credentials file.
	closer := &streamCloser{stream: stream}
	defer closer.close()

	counter := &byteCounter{onProgress: req.OnProgress}
	plaintext := &byteCounter{}
	stopWatcher := watchForStall(runCtx, plaintext, counter,
		req.FirstByteTimeout, req.StallTimeout, cancel)
	defer stopWatcher()

	digest := sha256.New()

	// The writing half runs in a goroutine because storage pulls: Put reads
	// from the pipe while this fills it. io.Pipe is unbuffered, so a write here
	// completes only once storage has taken the bytes, which is what makes the
	// counter a measure of bytes reaching storage rather than bytes produced.
	pr, pw := io.Pipe()
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- writeChain(runCtx, req, stream, pw, digest, counter, plaintext)
	}()

	// Cancellation alone does not unblock a source that is already inside Read:
	// a context is checked between reads, not during one. Closing the stream is
	// what kills the process and ends the read, so the teardown has to be
	// active rather than merely requested. Without this, a stalled backup hangs
	// the job the stall watcher just tried to end.
	stopTeardown := teardownOnCancel(runCtx, closer, pr)
	defer stopTeardown()

	info, putErr := req.Storage.Put(runCtx, req.Key, pr, storage.PutOptions{
		PartSize: req.PartSize,
	})
	if putErr != nil {
		// Unblock the writer, which is otherwise waiting for a reader that has
		// gone. Without this the goroutine outlives the job.
		_ = pr.CloseWithError(putErr)
		cancel(putErr)
	} else {
		_ = pr.Close()
	}

	writeErr := <-writeDone

	// Sidecars are collected while the source is still open, and that ordering
	// is not cosmetic. pg_dumpall needs the tunnel and the credentials file the
	// stream is holding, and Close is what tears both down -- so collecting
	// after it produces a globals sidecar that cannot connect, on a backup that
	// otherwise looks fine.
	//
	// The dump itself has finished by now: writeDone means the reader reached
	// EOF. Only a healthy run is annotated; there is nothing to add to a dump
	// that failed, and asking a dying source for more is how one failure
	// becomes two confusing ones.
	//
	// The stall watcher stops first. It counts bytes reaching storage, and no
	// byte is going to move while pg_dumpall runs.
	stopWatcher()

	var (
		sidecars   map[string][]byte
		sidecarErr error
	)
	if putErr == nil && writeErr == nil {
		sidecars, sidecarErr = stream.Sidecars()
	}

	closer.close()

	if err := req.attribute(runCtx, ctx, putErr, writeErr, closer.err()); err != nil {
		return Result{}, err
	}
	if sidecarErr != nil {
		return Result{}, classify(catalog.ErrClassSource, "collect sidecars", sidecarErr)
	}

	return Result{
		Object:       info,
		SHA256:       hex.EncodeToString(digest.Sum(nil)),
		BytesRead:    plaintext.total.Load(),
		Codec:        req.storedCodec(),
		Recipients:   req.Sealer.Recipients(),
		SourceResult: stream.Result(),
		Sidecars:     sidecars,
	}, nil
}

// writeChain assembles compress -> seal -> hash and count -> pipe, and drives
// the copy.
//
// The closers are unwound in the reverse order they were built, and every one
// is checked: closing the age writer is what writes its final chunk marker, so
// an ignored Close error is an object that cannot be opened.
func writeChain(
	ctx context.Context,
	req Request,
	stream *source.Stream,
	pw *io.PipeWriter,
	digest hash.Hash,
	stored, plaintext *byteCounter,
) (err error) {
	// Whatever happens, the reader must see an end. A pipe left open is a
	// storage Put that never returns.
	defer func() { _ = pw.CloseWithError(err) }()

	// Order matters: the digest and the counter sit closest to the pipe, so
	// they measure the bytes that actually leave (EF-053).
	tap := io.MultiWriter(pw, digest)
	rawSealed, err := req.Sealer.Seal(&countingWriter{w: tap, counter: stored})
	if err != nil {
		return classify(catalog.ErrClassCrypto, "start encryption", err)
	}

	// The sealer is guarded because the compressor writes to it from goroutines
	// of its own. On the error path, zstd's Close returns while a block writer
	// is still going, and the race detector caught it writing into the age
	// writer being closed here. Serialising the two makes a late write fail
	// cleanly against a closed writer instead of corrupting one that is open.
	sealed := &lockedWriteCloser{wc: rawSealed}

	var sink io.Writer = sealed
	var compressor *zstd.Encoder
	if req.SourceCodec == source.CodecNone {
		compressor, err = zstd.NewWriter(sealed, zstd.WithEncoderLevel(defaultCompressionLevel))
		if err != nil {
			_ = sealed.Close()
			return classify(catalog.ErrClassConfig, "start compression", err)
		}
		sink = compressor
	}

	// writerOnly hides the destination's ReadFrom from io.Copy.
	//
	// zstd's Encoder implements io.ReaderFrom, and io.Copy prefers it. Its
	// ReadFrom races with the block goroutines it starts as soon as the
	// downstream writer returns an error mid-stream, which the race detector
	// reproduces within a few repeats of the storage-failure tests. Still
	// present in klauspost/compress v1.20.0.
	//
	// Plain Write calls avoid that path entirely, and the throughput is set by
	// compression rather than by call overhead (P-003 measured 4.8 GiB/s).
	_, copyErr := io.Copy(writerOnly{sink}, &countingReader{
		ctx: ctx, r: stream.Reader, counter: plaintext,
	})

	// The compressor is closed on every path, including failure. zstd
	// compresses blocks in goroutines of its own, and Close is what waits for
	// them; returning without it leaves them racing on encoder state after this
	// function has gone. The race detector found that the hard way, so this is
	// not a tidiness measure.
	//
	// Their writes land in a sealer guarded by lockedWriteCloser, so a late one
	// fails cleanly against a closed writer rather than corrupting an open one.
	if compressor != nil {
		if err := compressor.Close(); err != nil && copyErr == nil {
			copyErr = err
		}
	}
	if copyErr != nil {
		// Abandoned: the deferred CloseWithError ends the pipe, which is what
		// stops storage. There is no object left to finalise.
		return copyErr
	}
	// Closing the sealer writes age's final chunk marker. Skipping it leaves an
	// object that reads as truncated -- correct behaviour, but not something to
	// rely on when the backup actually succeeded.
	if err := sealed.Close(); err != nil {
		return classify(catalog.ErrClassCrypto, "finish encryption", err)
	}
	return nil
}

// attribute decides which of the concurrent failures to report.
//
// Precedence, and the reason for it: the stall watcher fires when nothing else
// has noticed, so it wins. Storage failing starves the source, so storage
// outranks the source. Cancellation is the operator's own doing and is not a
// failure to diagnose. The source's own exit comes last, because by then it is
// usually a consequence rather than a cause.
func (req Request) attribute(runCtx, callerCtx context.Context, putErr, writeErr, closeErr error) error {
	switch cause := context.Cause(runCtx); {
	case errors.Is(cause, errFirstByteTimeout):
		return classify(catalog.ErrClassStalled, "read from source", errFirstByteTimeout)
	case errors.Is(cause, errStalled):
		return classify(catalog.ErrClassStalled, "write to storage", errStalled)
	}

	if callerCtx.Err() != nil {
		return classify(catalog.ErrClassCanceled, "backup", callerCtx.Err())
	}

	// The source's own failure comes before storage's, because when the source
	// dies the pipe closes and storage reports the very same error back. Only
	// the marker tells them apart.
	var fromSource *sourceReadError
	if errors.As(writeErr, &fromSource) {
		return classify(catalog.ErrClassSource, "read from source", fromSource.err)
	}
	var typed *Error
	if errors.As(writeErr, &typed) {
		return writeErr
	}

	if putErr != nil {
		return classify(catalog.ErrClassStorage, "write to storage", putErr)
	}
	if writeErr != nil {
		return classify(catalog.ErrClassSource, "read from source", writeErr)
	}
	if closeErr != nil {
		return classify(catalog.ErrClassSource, "finish source", closeErr)
	}
	return nil
}

func (req *Request) validate() error {
	var missing []string
	if req.Source == nil {
		missing = append(missing, "source")
	}
	if req.Storage == nil {
		missing = append(missing, "storage")
	}
	if req.Sealer == nil {
		// Not optional: EF-051 makes an unencrypted backup a deliberate,
		// separate decision, not something that happens because a field was
		// left nil.
		missing = append(missing, "sealer")
	}
	if req.Key == "" {
		missing = append(missing, "key")
	}
	if len(missing) > 0 {
		return classify(catalog.ErrClassConfig, "start backup",
			fmt.Errorf("incomplete request: missing %v", missing))
	}
	return nil
}

func (req *Request) applyDefaults() {
	// The zero value of source.Codec is "", not CodecNone. Comparing against
	// CodecNone without normalising silently skipped compression and recorded
	// the wrong codec -- a backup that restores, labelled as something it is
	// not. Normalising here means no other comparison has to remember.
	if req.SourceCodec == "" {
		req.SourceCodec = source.CodecNone
	}
	if req.FirstByteTimeout == 0 {
		req.FirstByteTimeout = DefaultFirstByteTimeout
	}
	if req.StallTimeout == 0 {
		req.StallTimeout = DefaultStallTimeout
	}
}

// storedCodec is what the bytes in storage actually carry, which is what a
// restore needs to know.
func (req Request) storedCodec() string {
	if req.SourceCodec != source.CodecNone {
		return string(req.SourceCodec)
	}
	return "zstd"
}

// byteCounter is the single measure of progress, shared by the digest side and
// the stall watcher.
type byteCounter struct {
	total      atomic.Int64
	onProgress func(int64)
}

func (c *byteCounter) add(n int64) {
	total := c.total.Add(n)
	if c.onProgress != nil {
		c.onProgress(total)
	}
}

type countingWriter struct {
	w       io.Writer
	counter *byteCounter
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if n > 0 {
		w.counter.add(int64(n))
	}
	return n, err
}

// writerOnly exposes nothing but Write, so io.Copy cannot reach for a
// ReadFrom or WriteTo shortcut. The same trick appears in the standard
// library for the same reason.
type writerOnly struct{ io.Writer }

// lockedWriteCloser serialises writes and the close against each other.
type lockedWriteCloser struct {
	mu sync.Mutex
	wc io.WriteCloser
}

func (l *lockedWriteCloser) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.wc.Write(p)
}

func (l *lockedWriteCloser) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.wc.Close()
}

// sourceReadError marks an error as having come from the source rather than
// from anything downstream.
//
// io.Copy reports one error and does not say which side produced it, and the
// two sides mean opposite things. When storage fails it closes the pipe, so the
// copy fails too; attributing that to the source would send an operator to the
// database host over a full disk on S3. The marker is what keeps the two apart.
type sourceReadError struct{ err error }

func (e *sourceReadError) Error() string { return e.err.Error() }
func (e *sourceReadError) Unwrap() error { return e.err }

// countingReader counts plaintext and honours cancellation between reads, so a
// cancelled job stops at the next chunk rather than at the end of the dump.
type countingReader struct {
	ctx     context.Context
	r       io.Reader
	counter *byteCounter
}

func (r *countingReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	if n > 0 {
		r.counter.add(int64(n))
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return n, &sourceReadError{err: err}
	}
	return n, err
}

// streamCloser closes a source stream exactly once, from whichever goroutine
// gets there first.
type streamCloser struct {
	stream   *source.Stream
	once     sync.Once
	mu       sync.Mutex
	closeErr error
}

func (c *streamCloser) close() {
	c.once.Do(func() {
		err := c.stream.Close()
		c.mu.Lock()
		c.closeErr = err
		c.mu.Unlock()
	})
}

func (c *streamCloser) err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeErr
}

// teardownOnCancel tears the job down when the context ends, rather than
// waiting for stages that cannot notice.
//
// Two things are needed and neither is optional. Closing the stream kills the
// process, which is the only way a read already in progress ends. Closing the
// pipe reader unblocks storage, which is otherwise waiting for a writer that is
// itself waiting for that process.
func teardownOnCancel(ctx context.Context, closer *streamCloser, pr *io.PipeReader) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		select {
		case <-ctx.Done():
			closer.close()
			_ = pr.CloseWithError(context.Cause(ctx))
		case <-done:
		}
	}()
	// Idempotent, and deliberately so: this is both deferred and called
	// explicitly once the work it guards is over. A stop that can only be
	// called once is a trap the next reader falls into.
	return sync.OnceFunc(func() {
		close(done)
		<-finished
	})
}

// watchForStall implements EF-095.
//
// It is the only mechanism that turns a hung backup into a failed one. A health
// check cannot see this: the process is alive, the connection is open, and
// nothing is moving.
func watchForStall(
	ctx context.Context,
	fromSource, toStorage *byteCounter,
	firstByte, stall time.Duration,
	cancel context.CancelCauseFunc,
) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		defer close(finished)
		// The two budgets are checked separately so the error can say which one
		// expired. An operator who cannot tell them apart tunes the wrong one.
		ticker := time.NewTicker(tickFor(min(firstByte, stall)))
		defer ticker.Stop()

		started := time.Now()
		var lastStored int64
		lastMoved := started

		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				// The first-byte budget watches the SOURCE, not storage.
				//
				// Watching storage looks equivalent and is not: sealing writes
				// an age header the moment the chain is built, before the
				// source has produced anything, so a counter on that side is
				// never zero and the budget silently never applies. That is a
				// timeout that exists in the code and not in reality.
				if fromSource.total.Load() == 0 {
					if now.Sub(started) >= firstByte {
						cancel(errFirstByteTimeout)
						return
					}
					continue
				}

				// Once the source has spoken, what matters is whether bytes are
				// still reaching storage. A source that keeps producing into an
				// upload that has wedged is exactly the stall to catch.
				stored := toStorage.total.Load()
				if stored > lastStored {
					lastStored = stored
					lastMoved = now
					continue
				}
				if now.Sub(lastMoved) >= stall {
					cancel(errStalled)
					return
				}
			}
		}
	}()

	// Idempotent, and deliberately so: this is both deferred and called
	// explicitly once the work it guards is over. A stop that can only be
	// called once is a trap the next reader falls into.
	return sync.OnceFunc(func() {
		close(done)
		<-finished
	})
}

// tickFor samples often enough to notice a stall promptly without spinning.
func tickFor(budget time.Duration) time.Duration {
	const (
		floor = 20 * time.Millisecond
		ceil  = 5 * time.Second
	)
	return min(max(budget/10, floor), ceil)
}
