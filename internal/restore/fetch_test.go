package restore_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/crypto"
	"github.com/Gu1llaum-3/koffr/internal/manifest"
	"github.com/Gu1llaum-3/koffr/internal/restore"
	"github.com/Gu1llaum-3/koffr/internal/storage"
	"github.com/Gu1llaum-3/koffr/internal/storage/memory"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

// stored builds an object the way the pipeline builds one -- compress, then
// seal, then record the digest of what was actually written -- so the fetcher
// is tested against the real layering rather than a convenient one.
func stored(t *testing.T, st storage.Storage, key string, plain []byte, compress bool) (manifest.Object, string) {
	t.Helper()
	identity, recipient := testutil.AgeIdentity(t)
	sealer, err := crypto.NewSealer([]string{recipient, recipient2(t)})
	require.NoError(t, err)

	var buf bytes.Buffer
	w, err := sealer.Seal(&buf)
	require.NoError(t, err)

	codec := "none"
	if compress {
		codec = "zstd"
		enc, err := zstd.NewWriter(w)
		require.NoError(t, err)
		_, err = enc.Write(plain)
		require.NoError(t, err)
		require.NoError(t, enc.Close())
	} else {
		_, err = w.Write(plain)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())

	sealed := buf.Bytes()
	sum := sha256.Sum256(sealed)
	info, err := st.Put(context.Background(), key, bytes.NewReader(sealed), storage.PutOptions{})
	require.NoError(t, err)

	return manifest.Object{
		Key:        key,
		SizeBytes:  info.Size,
		SHA256:     hex.EncodeToString(sum[:]),
		Codec:      codec,
		Encryption: "age",
		Recipients: sealer.Recipients(),
	}, identity
}

// recipient2 is the second recipient every sealed object needs (EF-051).
func recipient2(t *testing.T) string {
	t.Helper()
	_, r := testutil.AgeIdentity(t)
	return r
}

func TestFetch_RoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name     string
		compress bool
	}{
		{"compressed", true},
		{"stored as-is", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := memory.New()
			plain := bytes.Repeat([]byte("PGDMP payload; "), 5000)
			obj, identity := stored(t, st, "logical/x/dump.pgdump.zst.age", plain, tc.compress)

			opener, err := crypto.NewOpener(identity)
			require.NoError(t, err)
			f := restore.Fetcher{Storage: st, Opener: opener}

			var got bytes.Buffer
			n, err := f.Object(context.Background(), "", obj, &got, restore.FetchOptions{})
			require.NoError(t, err)
			assert.Equal(t, plain, got.Bytes(), "what comes out is what pg_dump put in")
			assert.Equal(t, int64(len(plain)), n)
		})
	}
}

// Raw is what `age -d` gives you in RESTORE.md step 3: decrypted, still
// compressed. The two paths have to agree, or the document and the tool
// disagree about what a file is called and what is in it.
func TestFetch_RawStopsAfterDecryption(t *testing.T) {
	st := memory.New()
	plain := []byte("some sql")
	obj, identity := stored(t, st, "logical/x/dump.pgdump.zst.age", plain, true)

	opener, err := crypto.NewOpener(identity)
	require.NoError(t, err)

	var raw bytes.Buffer
	_, err = restore.Fetcher{Storage: st, Opener: opener}.
		Object(context.Background(), "", obj, &raw, restore.FetchOptions{Raw: true})
	require.NoError(t, err)

	dec, err := zstd.NewReader(bytes.NewReader(raw.Bytes()))
	require.NoError(t, err, "raw output must still be a valid zstd frame")
	defer dec.Close()
	out, err := io.ReadAll(dec)
	require.NoError(t, err)
	assert.Equal(t, plain, out)
}

// The digest in the manifest covers the encrypted bytes and is the only thing
// that can catch a repository that rotted. A fetch that ignores it turns bit
// rot into a restore that half works.
func TestFetch_DetectsACorruptedObject(t *testing.T) {
	st := memory.New()
	obj, identity := stored(t, st, "logical/x/dump.pgdump.age", bytes.Repeat([]byte("a"), 200000), false)

	rc, err := st.Get(context.Background(), obj.Key)
	require.NoError(t, err)
	sealed, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())

	// Flip a bit late in the stream, past the age header: an early corruption
	// would be caught by age itself, which would prove nothing about the digest.
	sealed[len(sealed)-32] ^= 0x01
	_, err = st.Put(context.Background(), obj.Key, bytes.NewReader(sealed), storage.PutOptions{})
	require.NoError(t, err)

	opener, err := crypto.NewOpener(identity)
	require.NoError(t, err)
	_, err = restore.Fetcher{Storage: st, Opener: opener}.
		Object(context.Background(), "", obj, io.Discard, restore.FetchOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), obj.Key)
	assert.Contains(t, strings.ToLower(err.Error()), "digest")
}

// A truncated object is the shape a failed upload leaves behind. age's STREAM
// framing marks its last chunk, so this is detectable -- but only if nothing
// swallows the error.
func TestFetch_DetectsATruncatedObject(t *testing.T) {
	st := memory.New()
	obj, identity := stored(t, st, "logical/x/dump.pgdump.age", bytes.Repeat([]byte("b"), 300000), false)

	rc, err := st.Get(context.Background(), obj.Key)
	require.NoError(t, err)
	sealed, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())

	cut := sealed[:len(sealed)/2]
	_, err = st.Put(context.Background(), obj.Key, bytes.NewReader(cut), storage.PutOptions{})
	require.NoError(t, err)

	// The recorded digest is the full object's, so record the truncated one to
	// isolate what is being tested: without this the digest check would fire
	// first and age's framing would never be exercised.
	sum := sha256.Sum256(cut)
	obj.SHA256 = hex.EncodeToString(sum[:])

	opener, err := crypto.NewOpener(identity)
	require.NoError(t, err)
	_, err = restore.Fetcher{Storage: st, Opener: opener}.
		Object(context.Background(), "", obj, io.Discard, restore.FetchOptions{})
	require.Error(t, err)
}

func TestFetch_WrongIdentity(t *testing.T) {
	st := memory.New()
	obj, _ := stored(t, st, "logical/x/dump.pgdump.age", []byte("payload"), false)

	other, _ := testutil.AgeIdentity(t)
	opener, err := crypto.NewOpener(other)
	require.NoError(t, err)

	var got bytes.Buffer
	_, err = restore.Fetcher{Storage: st, Opener: opener}.
		Object(context.Background(), "", obj, &got, restore.FetchOptions{})
	require.Error(t, err)
	assert.Zero(t, got.Len(), "age fails on the header, so nothing should reach the writer")
	testutil.AssertNoSecretLeak(t, err.Error())
}

func TestFetch_MissingObject(t *testing.T) {
	opener, err := crypto.NewOpener(firstIdentity(t))
	require.NoError(t, err)
	_, err = restore.Fetcher{Storage: memory.New(), Opener: opener}.
		Object(context.Background(), "", manifest.Object{Key: "logical/x/absent.age"}, io.Discard,
			restore.FetchOptions{})
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestFetch_ReportsProgress(t *testing.T) {
	st := memory.New()
	obj, identity := stored(t, st, "logical/x/dump.pgdump.age", bytes.Repeat([]byte("c"), 400000), false)

	opener, err := crypto.NewOpener(identity)
	require.NoError(t, err)

	var last int64
	var calls int
	_, err = restore.Fetcher{Storage: st, Opener: opener}.
		Object(context.Background(), "", obj, io.Discard, restore.FetchOptions{
			OnProgress: func(n int64) {
				assert.GreaterOrEqual(t, n, last, "progress only ever goes up")
				last, calls = n, calls+1
			},
		})
	require.NoError(t, err)
	assert.Positive(t, calls)
	assert.Equal(t, obj.SizeBytes, last, "progress counts stored bytes, which is what a user sees shrink")
}

func firstIdentity(t *testing.T) string {
	t.Helper()
	id, _ := testutil.AgeIdentity(t)
	return id
}

// Manifest keys are filenames, not repository keys: RESTORE.md downloads the
// objects and then names them as local files, so a manifest carrying the prefix
// would make every command in that document wrong.
//
// The cost of that choice is that anything reading a backup must supply the
// prefix, and forgetting it fails in the least helpful way available -- an
// empty stream handed to pg_restore, which reports a truncated archive.
func TestFetch_JoinsThePrefix(t *testing.T) {
	st := memory.New()
	const prefix = "sources/shop/logical/01ABC/"
	plain := []byte("PGDMP")
	obj, identity := stored(t, st, prefix+"dump.pgdump.age", plain, false)

	// The manifest records the filename alone, as the backup runner writes it.
	obj.Key = "dump.pgdump.age"

	opener, err := crypto.NewOpener(identity)
	require.NoError(t, err)
	f := restore.Fetcher{Storage: st, Opener: opener}

	var got bytes.Buffer
	n, err := f.Object(context.Background(), prefix, obj, &got, restore.FetchOptions{})
	require.NoError(t, err)
	assert.Equal(t, plain, got.Bytes())
	assert.Equal(t, int64(len(plain)), n)

	_, err = f.Object(context.Background(), "", obj, io.Discard, restore.FetchOptions{})
	require.ErrorIs(t, err, storage.ErrNotFound,
		"without the prefix there is nothing there, and it has to say so rather than stream nothing")
}
