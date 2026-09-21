package backup

import (
	"context"
	"io"
	"time"
)

// Entry is one archive a destination holds.
type Entry struct {
	Path  string
	Bytes int64
	At    time.Time
}

// Store is **the** destination interface of E-066: one shape, implemented by
// the filesystem, S3 and SFTP alike. The domain declares it so that a backup
// can be written without knowing which of the three is on the other side — and
// so that the same conformance suite holds all three to the same promises
// (`internal/store/storetest`).
type Store interface {
	// Write streams an archive to path and returns how many bytes landed. It is
	// atomic as far as a reader is concerned: a path either holds a whole
	// archive or does not exist (BKP-11).
	Write(ctx context.Context, path string, from io.Reader) (int64, error)

	// Read streams an archive back. The caller closes it.
	Read(ctx context.Context, path string) (io.ReadCloser, error)

	// List returns what lies under a prefix, sorted by path, so that two
	// listings can be compared.
	List(ctx context.Context, prefix string) ([]Entry, error)

	// Delete removes one archive. Removing what is not there is not an error:
	// retention runs again after a crash.
	Delete(ctx context.Context, path string) error

	// Check says whether this destination can be written to, and **why not**
	// when it cannot (BKP-12). It leaves nothing behind.
	Check(ctx context.Context) error
}
