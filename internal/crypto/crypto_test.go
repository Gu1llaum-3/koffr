package crypto_test

import (
	"bytes"
	"crypto/rand"
	"io"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/crypto"
)

func newIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	return id
}

func seal(t *testing.T, s crypto.Sealer, plaintext []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w, err := s.Seal(&out)
	require.NoError(t, err)
	_, err = w.Write(plaintext)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return out.Bytes()
}

func TestRoundTrip(t *testing.T) {
	operational, recovery := newIdentity(t), newIdentity(t)
	s, err := crypto.NewSealer([]string{
		operational.Recipient().String(),
		recovery.Recipient().String(),
	})
	require.NoError(t, err)

	plaintext := []byte("pg_dump output would be here")
	ciphertext := seal(t, s, plaintext)
	assert.NotContains(t, string(ciphertext), "pg_dump output")

	// EF-051's whole point: either key alone opens the backup. The operational
	// one keeps day-to-day restores working; the offline one survives the loss
	// or compromise of the Koffr host.
	for name, id := range map[string]*age.X25519Identity{
		"operational": operational,
		"recovery":    recovery,
	} {
		t.Run(name, func(t *testing.T) {
			o, err := crypto.NewOpener(id.String())
			require.NoError(t, err)
			r, err := o.Open(bytes.NewReader(ciphertext))
			require.NoError(t, err)
			got, err := io.ReadAll(r)
			require.NoError(t, err)
			assert.Equal(t, plaintext, got)
		})
	}
}

// EF-051 makes the absence of an offline recovery key a configuration error,
// not a warning. This is the only guard against "the Koffr host burned with its
// key", so it is enforced where the Sealer is built rather than left to a
// linting pass over the configuration.
func TestNewSealer_RequiresTwoRecipients(t *testing.T) {
	one := newIdentity(t).Recipient().String()

	_, err := crypto.NewSealer(nil)
	require.Error(t, err)

	_, err = crypto.NewSealer([]string{one})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "recovery")

	_, err = crypto.NewSealer([]string{one, newIdentity(t).Recipient().String()})
	assert.NoError(t, err)
}

func TestNewSealer_RejectsMalformedRecipient(t *testing.T) {
	good := newIdentity(t).Recipient().String()
	for _, bad := range []string{"", "not-an-age-key", "age1", good + "tampered"} {
		_, err := crypto.NewSealer([]string{good, bad})
		assert.Error(t, err, "recipient %q should be rejected at construction", bad)
	}
}

func TestNewOpener_RejectsMalformedIdentity(t *testing.T) {
	for _, bad := range []string{"", "not-a-key", "AGE-SECRET-KEY-1"} {
		_, err := crypto.NewOpener(bad)
		assert.Error(t, err, "identity %q should be rejected at construction", bad)
	}
}

func TestRecipients_ExactAndOrdered(t *testing.T) {
	a := newIdentity(t).Recipient().String()
	b := newIdentity(t).Recipient().String()
	s, err := crypto.NewSealer([]string{a, b})
	require.NoError(t, err)
	assert.Equal(t, []string{a, b}, s.Recipients(),
		"the manifest records these verbatim; reordering them would misreport who can open a backup")
}

// Recipients() must not hand out a slice the caller can mutate: the manifest
// records it, and a caller trimming it would silently misreport who can open
// the backup.
func TestRecipients_NotAliased(t *testing.T) {
	a := newIdentity(t).Recipient().String()
	b := newIdentity(t).Recipient().String()
	s, err := crypto.NewSealer([]string{a, b})
	require.NoError(t, err)

	got := s.Recipients()
	got[0] = "tampered"
	assert.Equal(t, a, s.Recipients()[0])
}

// A stranger's key must not open the backup. Without this, every test above
// would pass just as well against a no-op cipher.
func TestOpen_WrongIdentityFails(t *testing.T) {
	s, err := crypto.NewSealer([]string{
		newIdentity(t).Recipient().String(),
		newIdentity(t).Recipient().String(),
	})
	require.NoError(t, err)
	ciphertext := seal(t, s, []byte("secret"))

	o, err := crypto.NewOpener(newIdentity(t).String())
	require.NoError(t, err)
	_, err = o.Open(bytes.NewReader(ciphertext))
	assert.Error(t, err)
}

// age's STREAM construction marks the final chunk, so a stream cut short is
// detected rather than accepted as a shorter backup. This is the property that
// makes a truncated upload a loud failure instead of a silent data loss.
func TestOpen_DetectsTruncation(t *testing.T) {
	id := newIdentity(t)
	s, err := crypto.NewSealer([]string{id.Recipient().String(), newIdentity(t).Recipient().String()})
	require.NoError(t, err)

	plaintext := make([]byte, 256<<10) // larger than one 64 KiB STREAM chunk
	_, err = rand.Read(plaintext)
	require.NoError(t, err)
	ciphertext := seal(t, s, plaintext)

	o, err := crypto.NewOpener(id.String())
	require.NoError(t, err)
	r, err := o.Open(bytes.NewReader(ciphertext[:len(ciphertext)-100]))
	require.NoError(t, err, "truncation is detected while reading, not while opening")
	_, err = io.ReadAll(r)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, io.EOF, "a truncated stream must not look like a clean end")
}

func TestOpen_DetectsTampering(t *testing.T) {
	id := newIdentity(t)
	s, err := crypto.NewSealer([]string{id.Recipient().String(), newIdentity(t).Recipient().String()})
	require.NoError(t, err)
	ciphertext := seal(t, s, bytes.Repeat([]byte("koffr"), 4096))

	// Flip a bit well past the header, inside the payload.
	tampered := bytes.Clone(ciphertext)
	tampered[len(tampered)-32] ^= 0x01

	o, err := crypto.NewOpener(id.String())
	require.NoError(t, err)
	r, err := o.Open(bytes.NewReader(tampered))
	require.NoError(t, err)
	_, err = io.ReadAll(r)
	assert.Error(t, err)
}

// Failing to close the writer leaves the age framing unfinished. Closing must
// therefore be the thing that makes the object valid, and the pipeline must
// treat a Close error as a failed backup.
func TestSeal_UnclosedWriterProducesUnreadableOutput(t *testing.T) {
	id := newIdentity(t)
	s, err := crypto.NewSealer([]string{id.Recipient().String(), newIdentity(t).Recipient().String()})
	require.NoError(t, err)

	var out bytes.Buffer
	w, err := s.Seal(&out)
	require.NoError(t, err)
	_, err = w.Write(bytes.Repeat([]byte("x"), 1024))
	require.NoError(t, err)
	// Deliberately not closed.

	o, err := crypto.NewOpener(id.String())
	require.NoError(t, err)
	r, err := o.Open(bytes.NewReader(out.Bytes()))
	if err == nil {
		_, err = io.ReadAll(r)
	}
	assert.Error(t, err)
}

func TestRoundTrip_LargePayload(t *testing.T) {
	id := newIdentity(t)
	s, err := crypto.NewSealer([]string{id.Recipient().String(), newIdentity(t).Recipient().String()})
	require.NoError(t, err)

	plaintext := make([]byte, 3<<20)
	_, err = rand.Read(plaintext)
	require.NoError(t, err)

	o, err := crypto.NewOpener(id.String())
	require.NoError(t, err)
	r, err := o.Open(bytes.NewReader(seal(t, s, plaintext)))
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(plaintext, got))
}
