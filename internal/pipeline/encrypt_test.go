package pipeline_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/Gu1llaum-3/koffr/internal/domain/crypto"
	"github.com/Gu1llaum-3/koffr/internal/pipeline"
)

// CRY-03 — encryption is streaming: what goes in is never held whole. A dump of
// 40 GB must not become 40 GB of memory (E-025, § 4.5).
func TestCRY03NothingIsMaterialised(t *testing.T) {
	recipients := pair(t)

	// A reader that would notice being drained into memory: it refuses to be
	// read more than one buffer ahead of what the writer has consumed.
	const size = 8 << 20

	var written bytes.Buffer

	encrypt, err := pipeline.Encrypt(&written, recipients)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	source := &pacedReader{remaining: size, t: t, sink: &written}
	if _, err := io.Copy(encrypt, source); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if err := encrypt.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if written.Len() == 0 {
		t.Fatal("nothing was written")
	}
	if source.peakUnwritten > 4<<20 {
		t.Errorf("%d bytes were read before anything came out: the stream is being buffered whole",
			source.peakUnwritten)
	}
}

// CRY-04 — each declared recipient opens the archive. That is the point of the
// escrow key: losing one does not condemn what was written (E-073).
func TestCRY04EachRecipientCanOpenTheArchive(t *testing.T) {
	first, second := identity(t), identity(t)
	recipients := crypto.Recipients{Keys: []*age.X25519Recipient{first.Recipient(), second.Recipient()}}

	const secret = "the contents of a database nobody else should read"
	archive := encrypted(t, recipients, secret)

	for name, key := range map[string]*age.X25519Identity{"operational": first, "escrow": second} {
		opened, err := age.Decrypt(bytes.NewReader(archive), key)
		if err != nil {
			t.Fatalf("the %s key could not open the archive: %v", name, err)
		}

		read, err := io.ReadAll(opened)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(read) != secret {
			t.Errorf("the %s key opened something else", name)
		}
	}
}

// CRY-04 — and no other key opens anything. An agent that is stolen gives away
// the databases as they are now, never the history (ADR-0007).
func TestCRY04AnotherKeyOpensNothing(t *testing.T) {
	mine, stranger := identity(t), identity(t)
	recipients := crypto.Recipients{Keys: []*age.X25519Recipient{mine.Recipient()}}

	archive := encrypted(t, recipients, "something worth stealing")

	if _, err := age.Decrypt(bytes.NewReader(archive), stranger); err == nil {
		t.Fatal("a key that is not a recipient opened the archive")
	}
}

// The archive is an age file, not a format of ours: E-075 asks that the
// standard tool read it, and the shape is the first half of that promise. The
// other half is scripts/check-age-interop.sh, which runs the real age binary.
func TestCRY03TheArchiveIsAnAgeFile(t *testing.T) {
	archive := encrypted(t, pair(t), "x")

	if !bytes.HasPrefix(archive, []byte("age-encryption.org/")) {
		t.Errorf("the archive does not start like an age file: %q", archive[:min(len(archive), 32)])
	}
}

func encrypted(t *testing.T, recipients crypto.Recipients, contents string) []byte {
	t.Helper()

	var out bytes.Buffer

	encrypt, err := pipeline.Encrypt(&out, recipients)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := io.Copy(encrypt, strings.NewReader(contents)); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if err := encrypt.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	return out.Bytes()
}

func identity(t *testing.T) *age.X25519Identity {
	t.Helper()

	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	return key
}

func pair(t *testing.T) crypto.Recipients {
	t.Helper()

	return crypto.Recipients{Keys: []*age.X25519Recipient{
		identity(t).Recipient(), identity(t).Recipient(),
	}}
}

// pacedReader hands out bytes and watches how far ahead of the output it gets.
type pacedReader struct {
	remaining     int
	peakUnwritten int
	read          int
	t             *testing.T
	sink          *bytes.Buffer
}

func (p *pacedReader) Read(into []byte) (int, error) {
	if p.remaining == 0 {
		return 0, io.EOF
	}

	n := min(len(into), p.remaining)
	p.remaining -= n
	p.read += n

	if ahead := p.read - p.sink.Len(); ahead > p.peakUnwritten {
		p.peakUnwritten = ahead
	}

	return n, nil
}
