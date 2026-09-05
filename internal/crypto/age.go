package crypto

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"filippo.io/age"
)

// MinRecipients is why this package refuses to build a Sealer for a single key.
//
// EF-051 requires an offline recovery recipient alongside the operational one.
// The check lives here rather than in configuration validation because this is
// the last place a backup can be prevented from becoming unrecoverable, and
// because a code path that constructs a Sealer directly must not be able to
// skip it.
const MinRecipients = 2

// sealer encrypts for a fixed set of age recipients.
type sealer struct {
	recipients []age.Recipient
	strings    []string
}

// NewSealer parses recipients and refuses a set that leaves no way back if the
// Koffr host is lost.
func NewSealer(recipients []string) (Sealer, error) {
	if len(recipients) < MinRecipients {
		return nil, fmt.Errorf(
			"backups need at least %d age recipients, got %d: configure an offline recovery "+
				"recipient alongside the operational key, or a lost Koffr host means lost backups",
			MinRecipients, len(recipients))
	}

	parsed := make([]age.Recipient, 0, len(recipients))
	for i, r := range recipients {
		p, err := age.ParseX25519Recipient(strings.TrimSpace(r))
		if err != nil {
			// The recipient is a public key, so naming it in the error is safe
			// and is what makes a typo findable.
			return nil, fmt.Errorf("recipient %d (%q) is not a valid age recipient: %w", i, r, err)
		}
		parsed = append(parsed, p)
	}

	return &sealer{recipients: parsed, strings: slices.Clone(recipients)}, nil
}

// Seal wraps w. The returned WriteCloser must be closed: closing writes age's
// final chunk marker, and without it the object is unreadable. The pipeline
// therefore treats a Close error as a failed backup rather than a cleanup
// detail.
//
// No buffer is interposed here. P-003 measured a 1 MiB bufio making the real
// chain 6% slower: zstd already emits large blocks, so the buffer only adds a
// copy.
func (s *sealer) Seal(w io.Writer) (io.WriteCloser, error) {
	wc, err := age.Encrypt(w, s.recipients...)
	if err != nil {
		return nil, fmt.Errorf("start encryption: %w", err)
	}
	return wc, nil
}

// Recipients returns the recipient strings, in configuration order, for the
// manifest to record. The slice is copied: a caller trimming it would silently
// misreport who can open the backup.
func (s *sealer) Recipients() []string { return slices.Clone(s.strings) }

// opener decrypts with one identity.
type opener struct{ identity age.Identity }

// NewOpener parses an age identity.
//
// The identity is a secret, so it is never echoed in an error, however useful
// that would be for debugging a typo (ENF-021).
func NewOpener(identity string) (Opener, error) {
	id, err := age.ParseX25519Identity(strings.TrimSpace(identity))
	if err != nil {
		return nil, errors.New("invalid age identity: it must start with AGE-SECRET-KEY-1")
	}
	return &opener{identity: id}, nil
}

// Open decrypts r.
//
// A truncated or altered stream is not detected here: age authenticates each
// 64 KiB chunk as it is read, so the error surfaces from the returned reader.
// That is the desired behaviour for a pipeline, which would otherwise have to
// buffer the whole object before trusting any of it.
func (o *opener) Open(r io.Reader) (io.Reader, error) {
	dec, err := age.Decrypt(r, o.identity)
	if err != nil {
		return nil, fmt.Errorf("open encrypted stream: %w", err)
	}
	return dec, nil
}
