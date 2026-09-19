package pipeline_test

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"

	"github.com/Gu1llaum-3/koffr/internal/domain/crypto"
	"github.com/Gu1llaum-3/koffr/internal/pipeline"
)

// BKP-07 — the raw dump is never held whole. A 40 GB database must not become
// 40 GB of anything (E-025, § 4.5). Measured, not asserted: the source watches
// how far ahead of the output it is allowed to get.
func TestBKP07TheStreamIsNeverHeldWhole(t *testing.T) {
	const size = 16 << 20

	var written bytes.Buffer

	counted := &writtenCounter{into: &written}

	// Incompressible on purpose. With repetitive input, zstd emits almost
	// nothing until the end, and the gap between what is read and what is
	// written measures the compression ratio rather than the buffering.
	source := &pacedReader{remaining: size, t: t, sink: counted, random: true}

	result, err := pipeline.Run(counted, source, pipeline.Options{Recipients: pair(t)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.RawBytes != size {
		t.Errorf("RawBytes = %d, want %d", result.RawBytes, size)
	}
	if source.peakUnwritten > size/2 {
		t.Errorf("%d of %d bytes were read before the output caught up: the stream is being held whole",
			source.peakUnwritten, size)
	}
}

// BKP-08 — both checksums are computed on the way through, and both are right.
// Checked independently: the archive is decrypted and decompressed by hand, and
// the result hashed, rather than trusting what the pipeline says about itself.
func TestBKP08BothChecksumsAreComputedOnTheWay(t *testing.T) {
	key := identity(t)
	recipients := crypto.Recipients{Keys: []*age.X25519Recipient{key.Recipient()}}

	const dump = "a dump that is small enough to check by hand, and long enough to compress.\n"
	contents := strings.Repeat(dump, 200)

	var written bytes.Buffer

	result, err := pipeline.Run(&written, strings.NewReader(contents), pipeline.Options{Recipients: recipients})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if want := sum(contents); result.RawSHA256 != want {
		t.Errorf("RawSHA256 = %s, want the checksum of what went in (%s)", result.RawSHA256, want)
	}
	if want := sum(written.String()); result.StoredSHA256 != want {
		t.Errorf("StoredSHA256 = %s, want the checksum of what came out (%s)", result.StoredSHA256, want)
	}
	if int64(written.Len()) != result.StoredBytes {
		t.Errorf("StoredBytes = %d, want %d", result.StoredBytes, written.Len())
	}

	// And the archive really contains the dump, read back the long way.
	if got := decrypt(t, written.Bytes(), key); got != contents {
		t.Error("the archive does not contain what was put in")
	}
}

// BKP-09 — a failure in the middle of the stream is a failure. An archive that
// stops halfway looks exactly like one that finished, and P3 refuses to let
// koffr call that a backup.
func TestBKP09AFailureMidStreamIsNeverASuccess(t *testing.T) {
	cases := []struct {
		name   string
		source io.Reader
		into   io.Writer
	}{
		{
			name:   "the dump stops halfway",
			source: io.MultiReader(strings.NewReader("the beginning of a dump"), failingReader{}),
			into:   &bytes.Buffer{},
		},
		{
			name: "the disk fills up",
			// Incompressible, so that the bytes really reach the writer:
			// a megabyte of the same character comes out as a few hundred.
			source: io.LimitReader(rand.Reader, 1<<20),
			into:   &failingWriter{after: 4096},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, err := pipeline.Run(c.into, c.source, pipeline.Options{Recipients: pair(t)})
			if err == nil {
				t.Fatalf("a broken stream produced a success: %+v", result)
			}
			if result.RawSHA256 != "" || result.StoredSHA256 != "" {
				t.Errorf("a failed run handed back checksums: %+v", result)
			}
		})
	}
}

// BKP-09 — no recipient is a refusal, not an archive nobody can open.
func TestBKP09NoRecipientIsRefused(t *testing.T) {
	if _, err := pipeline.Run(&bytes.Buffer{}, strings.NewReader("x"), pipeline.Options{}); err == nil {
		t.Fatal("a run with no recipient was accepted")
	}
}

// The compression level is zstd:3 by default (N-4) and settable per database.
func TestTheCompressionLevelIsSettable(t *testing.T) {
	contents := strings.Repeat("a dump compresses well when it repeats itself. ", 5000)
	recipients := pair(t)

	fastest, err := pipeline.Run(&bytes.Buffer{}, strings.NewReader(contents),
		pipeline.Options{Recipients: recipients, CompressionLevel: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	best, err := pipeline.Run(&bytes.Buffer{}, strings.NewReader(contents),
		pipeline.Options{Recipients: recipients, CompressionLevel: 11})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if best.StoredBytes >= fastest.StoredBytes {
		t.Errorf("level 11 produced %d bytes and level 1 produced %d: the level does nothing",
			best.StoredBytes, fastest.StoredBytes)
	}

	byDefault, err := pipeline.Run(&bytes.Buffer{}, strings.NewReader(contents),
		pipeline.Options{Recipients: recipients})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if byDefault.StoredBytes >= fastest.StoredBytes {
		t.Errorf("the default compresses no better than level 1: %d against %d",
			byDefault.StoredBytes, fastest.StoredBytes)
	}
}

func sum(of string) string {
	digest := sha256.Sum256([]byte(of))

	return hex.EncodeToString(digest[:])
}

func decrypt(t *testing.T, archive []byte, key *age.X25519Identity) string {
	t.Helper()

	opened, err := age.Decrypt(bytes.NewReader(archive), key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	decompress, err := zstd.NewReader(opened)
	if err != nil {
		t.Fatalf("zstd: %v", err)
	}
	defer decompress.Close()

	read, err := io.ReadAll(decompress)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	return string(read)
}

var errStreamBroke = errors.New("the stream broke")

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errStreamBroke }

type failingWriter struct{ after, written int }

func (f *failingWriter) Write(p []byte) (int, error) {
	f.written += len(p)
	if f.written > f.after {
		return 0, errStreamBroke
	}

	return len(p), nil
}
