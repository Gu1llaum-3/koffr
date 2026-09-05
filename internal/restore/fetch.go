package restore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"

	"github.com/klauspost/compress/zstd"

	"github.com/Gu1llaum-3/koffr/internal/crypto"
	"github.com/Gu1llaum-3/koffr/internal/manifest"
	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// Fetcher reads objects back out of a repository.
//
// It is the exact inverse of the backup pipeline and deliberately the only
// inverse: storage, then digest, then age, then zstd. Anything that needs a
// backup's bytes -- restoring, verifying, or the fetch command handing a file
// to a human -- goes through here, so there is one place where the layering can
// be wrong.
type Fetcher struct {
	Storage storage.Storage
	Opener  crypto.Opener
}

// FetchOptions tunes a single fetch.
type FetchOptions struct {
	// Raw stops after decryption, handing over the bytes `age -d` would
	// produce: still compressed if the object was compressed. It is what makes
	// the fetch command and RESTORE.md agree on what a file contains.
	Raw bool

	// OnProgress is called with a running count of *stored* bytes read, not
	// plaintext bytes produced. Stored bytes are what the manifest records and
	// what a user watching a download expects to see reach the total.
	OnProgress func(bytes int64)
}

// Manifest reads a backup's plaintext manifest.
//
// It is plaintext by design (EF-055): listing backups, checking digests and
// planning a restore must all work for someone who does not hold a key.
func (f Fetcher) Manifest(ctx context.Context, b storage.Backup) (manifest.Manifest, error) {
	rc, err := f.Storage.Get(ctx, b.ManifestKey())
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("restore: read %s: %w", b.ManifestKey(), err)
	}
	defer func() { _ = rc.Close() }()

	m, err := manifest.Decode(rc)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("restore: %s: %w", b.ManifestKey(), err)
	}
	return m, nil
}

// Details reads the encrypted sidecar: database and relation names, which are
// exactly the part a repository holder should not be able to read.
func (f Fetcher) Details(ctx context.Context, b storage.Backup) (manifest.Details, error) {
	var buf writeCounter
	if _, err := f.Object(ctx, manifest.Object{Key: b.DetailsKey(), Codec: "none"}, &buf, FetchOptions{}); err != nil {
		return manifest.Details{}, err
	}
	d, err := manifest.DecodeDetails(&buf)
	if err != nil {
		return manifest.Details{}, fmt.Errorf("restore: %s: %w", b.DetailsKey(), err)
	}
	return d, nil
}

// Object streams one object into w and returns the number of plaintext bytes
// written.
//
// The digest is verified as the object streams, which means a mismatch is
// reported only once the last byte has been read -- by which time w has already
// seen the data. That is unavoidable without buffering the whole object, and
// buffering an 80 GiB dump is the thing this tool exists not to do. Callers
// that cannot unwind (restoring straight into a live database) must treat the
// error as "this restore is not trustworthy", not as a warning.
func (f Fetcher) Object(ctx context.Context, o manifest.Object, w io.Writer, opt FetchOptions) (int64, error) {
	if o.Key == "" {
		return 0, errors.New("restore: fetch needs an object key")
	}
	rc, err := f.Storage.Get(ctx, o.Key)
	if err != nil {
		return 0, fmt.Errorf("restore: fetch %s: %w", o.Key, err)
	}
	defer func() { _ = rc.Close() }()

	var (
		src    io.Reader = rc
		digest hash.Hash
	)
	if o.SHA256 != "" {
		digest = sha256.New()
		src = io.TeeReader(src, digest)
	}
	if opt.OnProgress != nil {
		src = &progressReader{r: src, report: opt.OnProgress}
	}

	plain, err := f.Opener.Open(src)
	if err != nil {
		return 0, fmt.Errorf("restore: decrypt %s: %w", o.Key, err)
	}
	if !opt.Raw && o.Codec == "zstd" {
		dec, err := zstd.NewReader(plain)
		if err != nil {
			return 0, fmt.Errorf("restore: decompress %s: %w", o.Key, err)
		}
		defer dec.Close()
		plain = dec
	}

	n, copyErr := io.Copy(w, plain)

	// The digest covers every stored byte, so it can only be checked once the
	// reader is exhausted. Decompression stops at the frame's end marker and
	// may leave trailing bytes unread, so drain first: a digest computed over
	// part of an object would pass on a truncated one.
	//
	// This runs even when the copy failed, and that ordering is the point. age
	// authenticates every chunk, so a damaged object usually fails here first,
	// with "corrupted or tampered with" -- two possibilities an operator cannot
	// act on differently. The digest separates them: if the stored bytes do not
	// match the manifest, the repository is damaged, and nobody needs to go
	// looking for an attacker.
	_, _ = io.Copy(io.Discard, src)
	if digest != nil {
		if got := hex.EncodeToString(digest.Sum(nil)); got != o.SHA256 {
			return n, fmt.Errorf(
				"restore: %s: digest mismatch, the object in the repository is not the one that was "+
					"backed up (manifest %s, read %s)", o.Key, short(o.SHA256), short(got))
		}
	}
	if copyErr != nil {
		return n, fmt.Errorf("restore: fetch %s: %w", o.Key, copyErr)
	}
	return n, nil
}

func short(sum string) string {
	if len(sum) > 12 {
		return sum[:12] + "..."
	}
	return sum
}

// progressReader reports bytes read from the repository.
type progressReader struct {
	r      io.Reader
	n      int64
	report func(int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.n += int64(n)
		p.report(p.n)
	}
	return n, err
}

// writeCounter is a minimal in-memory sink for the small sidecars, kept here so
// details.json never needs a temporary file.
type writeCounter struct {
	buf []byte
	off int
}

func (b *writeCounter) Write(p []byte) (int, error) { b.buf = append(b.buf, p...); return len(p), nil }

func (b *writeCounter) Read(p []byte) (int, error) {
	if b.off >= len(b.buf) {
		return 0, io.EOF
	}
	n := copy(p, b.buf[b.off:])
	b.off += n
	return n, nil
}
