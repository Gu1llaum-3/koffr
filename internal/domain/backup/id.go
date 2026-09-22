package backup

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// entropy is the source identifiers are drawn from: **cryptographic**, and
// monotonic within a millisecond so that two jobs started together still sort
// in the order they started (`N-2`, ADR-0006).
//
// MonotonicEntropy is not safe for concurrent use, hence the mutex.
var entropy = struct {
	sync.Mutex
	source *ulid.MonotonicEntropy
}{source: ulid.Monotonic(rand.Reader, 0)}

// NewJobID is the identifier of a job and of the archive it writes: a ULID,
// sortable by time, unique without coordination, and readable by anything that
// knows the format — which is the point of using a public one (ADR-0006).
//
// Never ulid.Make: it draws from math/rand and panics through MustNew when its
// source fails. A backup agent does not panic at two in the morning (`N-2`).
func NewJobID() string {
	entropy.Lock()
	defer entropy.Unlock()

	id, err := ulid.New(ulid.Timestamp(time.Now()), entropy.source)
	if err != nil {
		// The monotonic source overflows only after 2^80 draws in one
		// millisecond; a fresh one keeps the job running rather than stopping
		// a backup over an identifier.
		entropy.source = ulid.Monotonic(rand.Reader, 0)

		id, err = ulid.New(ulid.Timestamp(time.Now()), entropy.source)
		if err != nil {
			return fmt.Sprintf("%026X", time.Now().UnixNano())
		}
	}

	return id.String()
}
