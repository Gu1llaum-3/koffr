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

// ErrDigestMismatch means the stored bytes are not the ones that were backed
// up. It is a distinct error because it is a distinct situation: the backup
// exists, and it cannot be trusted, which is worse than it being missing.
var ErrDigestMismatch = errors.New("digest mismatch")

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
//
// It takes the manifest rather than guessing, because the codec is recorded
// there and assuming one is how a compressed sidecar comes back as bytes that
// are not JSON.
func (f Fetcher) Details(ctx context.Context, b storage.Backup, m manifest.Manifest) (manifest.Details, error) {
	obj, ok := objectNamed(m, DetailsObject)
	if !ok {
		return manifest.Details{}, fmt.Errorf("restore: backup %s lists no %s", m.BackupID, DetailsObject)
	}
	var buf writeCounter
	if _, err := f.Object(ctx, b.Prefix(), obj, &buf, FetchOptions{}); err != nil {
		return manifest.Details{}, err
	}
	d, err := manifest.DecodeDetails(&buf)
	if err != nil {
		return manifest.Details{}, fmt.Errorf("restore: %s%s: %w", b.Prefix(), obj.Key, err)
	}
	return d, nil
}

// DetailsObject is the sidecar's filename inside a backup.
const DetailsObject = "details.json.age"

// objectNamed finds an object by filename.
func objectNamed(m manifest.Manifest, name string) (manifest.Object, bool) {
	for _, o := range m.Objects {
		if o.Key == name {
			return o, true
		}
	}
	return manifest.Object{}, false
}

// Object streams one object into w and returns the number of plaintext bytes
// written.
//
// The prefix is separate from the object because manifest keys are filenames,
// not repository keys: RESTORE.md tells a reader to download the objects and
// then names them as local files, and a manifest that embedded the prefix would
// make every command in that document wrong. The prefix comes from
// storage.Backup.Prefix(), and passing it is not optional.
//
// The digest is verified as the object streams, which means a mismatch is
// reported only once the last byte has been read -- by which time w has already
// seen the data. That is unavoidable without buffering the whole object, and
// buffering an 80 GiB dump is the thing this tool exists not to do. Callers
// that cannot unwind (restoring straight into a live database) must treat the
// error as "this restore is not trustworthy", not as a warning.
func (f Fetcher) Object(
	ctx context.Context, prefix string, o manifest.Object, w io.Writer, opt FetchOptions,
) (int64, error) {
	if o.Key == "" {
		return 0, errors.New("restore: fetch needs an object key")
	}
	key := prefix + o.Key
	rc, err := f.Storage.Get(ctx, key)
	if err != nil {
		return 0, fmt.Errorf("restore: fetch %s: %w", key, err)
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

	// Every failure below goes through checkDigest, and that is the point.
	//
	// age authenticates what it reads, so a damaged object usually fails there
	// first: "corrupted or tampered with" at the header, or a failed chunk
	// mid-stream. Those are two possibilities an operator cannot act on
	// differently. Comparing the stored bytes against the manifest separates
	// them -- if they do not match, the repository is damaged, and nobody needs
	// to go looking for an attacker.
	plain, err := f.Opener.Open(src)
	if err != nil {
		return 0, checkDigest(src, digest, key, o.SHA256,
			fmt.Errorf("restore: decrypt %s: %w", key, err))
	}
	if !opt.Raw && o.Codec == "zstd" {
		dec, err := zstd.NewReader(plain)
		if err != nil {
			return 0, checkDigest(src, digest, key, o.SHA256,
				fmt.Errorf("restore: decompress %s: %w", key, err))
		}
		defer dec.Close()
		plain = dec
	}

	n, copyErr := io.Copy(w, plain)
	if copyErr != nil {
		copyErr = fmt.Errorf("restore: fetch %s: %w", key, copyErr)
	}
	return n, checkDigest(src, digest, key, o.SHA256, copyErr)
}

// checkDigest drains whatever is left of the stored stream, compares the digest,
// and returns the mismatch if there is one or fallback otherwise.
//
// Draining first is not tidiness. The digest covers every stored byte, and
// decompression stops at the frame's end marker, so a digest computed over the
// part that happened to be read would pass on a truncated object -- which is
// exactly the object it exists to catch.
func checkDigest(src io.Reader, digest hash.Hash, key, want string, fallback error) error {
	if digest == nil || want == "" {
		return fallback
	}
	_, _ = io.Copy(io.Discard, src)
	if got := hex.EncodeToString(digest.Sum(nil)); got != want {
		return fmt.Errorf(
			"restore: %s: %w, the object in the repository is not the one that was "+
				"backed up (manifest %s, read %s)", key, ErrDigestMismatch, short(want), short(got))
	}
	return fallback
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
