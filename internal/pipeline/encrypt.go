package pipeline

import (
	"fmt"
	"io"

	"filippo.io/age"

	"github.com/Gu1llaum-3/koffr/internal/domain/crypto"
)

// Encrypt wraps a writer so that everything written to it comes out encrypted
// for the given recipients, **in one pass**. Nothing is held whole: a 40 GB
// dump must not become 40 GB of memory (E-025, § 4.5).
//
// What comes out is an age file and nothing else — no header of ours, no
// container of ours. E-075 promises that the archive stays readable with the
// standard `age` tool, and the only way to keep that promise is to write
// exactly what age writes.
//
// The caller closes the returned writer: age finishes its last chunk there, and
// an archive whose writer was not closed is truncated.
func Encrypt(into io.Writer, recipients crypto.Recipients) (io.WriteCloser, error) {
	if len(recipients.Keys) == 0 {
		return nil, fmt.Errorf("encrypt: no recipient, and koffr does not encrypt for nobody")
	}

	to := make([]age.Recipient, 0, len(recipients.Keys))
	for _, key := range recipients.Keys {
		to = append(to, key)
	}

	encrypted, err := age.Encrypt(into, to...)
	if err != nil {
		return nil, fmt.Errorf("encrypt for %d recipients: %w", len(to), err)
	}

	return encrypted, nil
}
