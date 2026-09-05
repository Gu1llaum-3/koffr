package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/crypto"
	"github.com/Gu1llaum-3/koffr/internal/manifest"
	"github.com/Gu1llaum-3/koffr/internal/restore"
	"github.com/Gu1llaum-3/koffr/internal/storage"
	"github.com/Gu1llaum-3/koffr/internal/storage/memory"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

// pg_restore stops reading at the archive's end marker, so on a successful
// restore the streaming goroutine is still blocked writing into a pipe nobody
// will read again. Waiting for it without closing the reader first hangs the
// command forever -- which is what a real restore did, silently, with no
// process left to blame.
func TestPipeObject_StopsWhenTheToolStopsReading(t *testing.T) {
	st := memory.New()
	identity, recipient := testutil.AgeIdentity(t)
	_, recovery := testutil.AgeIdentity(t)

	sealer, err := crypto.NewSealer([]string{recipient, recovery})
	require.NoError(t, err)

	// Big enough that the writer cannot possibly finish into an unbuffered
	// pipe: a small object would complete on its own and prove nothing.
	plain := bytes.Repeat([]byte("PGDMP payload "), 400_000)
	var sealed bytes.Buffer
	w, err := sealer.Seal(&sealed)
	require.NoError(t, err)
	_, err = w.Write(plain)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	const key = "sources/shop/logical/01ABC/dump.pgdump.age"
	info, err := st.Put(t.Context(), key, bytes.NewReader(sealed.Bytes()), storage.PutOptions{})
	require.NoError(t, err)
	sum := sha256.Sum256(sealed.Bytes())

	opener, err := crypto.NewOpener(identity)
	require.NoError(t, err)

	a := &app{}
	r, stop := a.pipeObject(t.Context(), restore.Fetcher{Storage: st, Opener: opener},
		"sources/shop/logical/01ABC/",
		manifest.Object{
			Key: "dump.pgdump.age", SizeBytes: info.Size,
			SHA256: hex.EncodeToString(sum[:]), Codec: "none",
		})

	// Read a little and walk away, exactly as pg_restore does.
	head := make([]byte, 32)
	_, err = io.ReadFull(r, head)
	require.NoError(t, err)
	assert.Equal(t, plain[:32], head)

	returned := make(chan error, 1)
	go func() { returned <- stop() }()

	select {
	case err := <-returned:
		assert.NoError(t, err,
			"a tool that stopped reading is not a repository failure and must not be reported as one")
	case <-time.After(10 * time.Second):
		t.Fatal("stop() never returned: the streaming goroutine is still blocked on a pipe nobody reads")
	}
}

// The other half: a repository that genuinely fails must still be reported,
// because that error is the cause and the tool's complaint is the symptom.
func TestPipeObject_ReportsAMissingObject(t *testing.T) {
	opener, err := crypto.NewOpener(func() string { id, _ := testutil.AgeIdentity(t); return id }())
	require.NoError(t, err)

	a := &app{}
	r, stop := a.pipeObject(context.Background(), restore.Fetcher{Storage: memory.New(), Opener: opener},
		"sources/shop/logical/01ABC/", manifest.Object{Key: "dump.pgdump.age"})
	_, _ = io.Copy(io.Discard, r)

	require.ErrorIs(t, stop(), storage.ErrNotFound)
}
