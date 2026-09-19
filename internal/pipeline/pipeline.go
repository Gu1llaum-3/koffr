package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"

	"github.com/klauspost/compress/zstd"

	"github.com/Gu1llaum-3/koffr/internal/domain/crypto"
)

// defaultCompression is zstd:3 (N-4, hypothesis of Q-08): fast enough not to
// starve the dump, and enough to turn 40 GB into something a disk can hold.
const defaultCompression = 3

// Options configures one run. The zero value has no recipients, which is
// refused: koffr does not encrypt for nobody.
type Options struct {
	Recipients crypto.Recipients

	// CompressionLevel is the zstd level. Zero means the default.
	CompressionLevel int
}

// Result is what a finished run knows about what it wrote. It is only filled in
// when the run succeeded: a failure hands back nothing that could be mistaken
// for a backup (BKP-09).
type Result struct {
	// RawSHA256 is the checksum of the dump **before** compression, and
	// StoredSHA256 that of what was actually written. Two of them because only
	// the second can be checked without decrypting (N-7).
	RawSHA256    string
	StoredSHA256 string

	RawBytes    int64
	StoredBytes int64
}

// Run streams a dump through compression, encryption and both checksums, in a
// single pass over the sub-process output.
//
// Nothing is materialised: what the reader hands over goes straight through
// zstd into age into the writer, and the two checksums are computed as the
// bytes go by. That is E-025, and it is the reason a 40 GB database does not
// need 40 GB of anything.
//
// The order matters and is the order of § 4.1: compress, then encrypt. The
// other way round would compress random-looking bytes and gain nothing.
func Run(into io.Writer, from io.Reader, options Options) (Result, error) {
	if len(options.Recipients.Keys) == 0 {
		return Result{}, fmt.Errorf("run the pipeline: no recipient, and koffr does not encrypt for nobody")
	}

	stored := newCounter(sha256.New())
	raw := newCounter(sha256.New())

	// Built from the outside in: the writer first, then what wraps it.
	encrypt, err := Encrypt(io.MultiWriter(into, stored), options.Recipients)
	if err != nil {
		return Result{}, err
	}

	compress, err := zstd.NewWriter(encrypt, zstd.WithEncoderLevel(levelOf(options.CompressionLevel)))
	if err != nil {
		return Result{}, fmt.Errorf("start the compression: %w", err)
	}

	if _, err := io.Copy(io.MultiWriter(compress, raw), from); err != nil {
		// Close what was opened, and say nothing about a result: a stream that
		// stopped halfway looks exactly like one that finished (BKP-09).
		_ = compress.Close()
		_ = encrypt.Close()

		return Result{}, fmt.Errorf("stream the dump: %w", err)
	}

	// Closed in order, innermost first: zstd flushes its last block into age,
	// which writes its last chunk into the destination. Skipping either leaves
	// an archive that is truncated and looks complete.
	if err := compress.Close(); err != nil {
		_ = encrypt.Close()

		return Result{}, fmt.Errorf("finish the compression: %w", err)
	}
	if err := encrypt.Close(); err != nil {
		return Result{}, fmt.Errorf("finish the encryption: %w", err)
	}

	return Result{
		RawSHA256:    raw.sum(),
		StoredSHA256: stored.sum(),
		RawBytes:     raw.bytes,
		StoredBytes:  stored.bytes,
	}, nil
}

func levelOf(asked int) zstd.EncoderLevel {
	if asked <= 0 {
		asked = defaultCompression
	}

	return zstd.EncoderLevelFromZstd(asked)
}

// counter hashes what goes through it and counts it.
type counter struct {
	digest hash.Hash
	bytes  int64
}

func newCounter(digest hash.Hash) *counter {
	return &counter{digest: digest}
}

func (c *counter) Write(p []byte) (int, error) {
	c.bytes += int64(len(p))

	return c.digest.Write(p) //nolint:wrapcheck // hash.Hash never errors
}

func (c *counter) sum() string {
	return hex.EncodeToString(c.digest.Sum(nil))
}
