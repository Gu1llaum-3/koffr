// Package crypto wraps age-based encryption.
//
// The format is age (X25519 + ChaCha20-Poly1305, STREAM construction): a
// published specification with an independent CLI implementation, which is what
// makes a backup decryptable without Koffr (PD-001, DEC-002).
//
// Envelope encryption and multiple recipients come from the format itself, so
// none of that is reimplemented here.
package crypto

import "io"

// Sealer encrypts a stream for a fixed set of recipients.
//
// At least two recipients are always configured: the operational key held by
// Koffr, and an offline recovery key that Koffr never holds. A configuration
// with a single recipient is an error, not a warning (EF-051).
type Sealer interface {
	// Seal wraps w. Closing the returned WriteCloser finalises the age
	// framing; failing to close truncates the object, and a truncated age file
	// is detected on read rather than silently accepted.
	Seal(w io.Writer) (io.WriteCloser, error)

	// Recipients returns the age recipient strings this Sealer encrypts for,
	// so they can be recorded in the manifest.
	Recipients() []string
}

// Opener decrypts a stream using an identity.
type Opener interface {
	Open(r io.Reader) (io.Reader, error)
}
