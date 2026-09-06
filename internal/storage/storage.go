// Package storage persists backup objects to a destination.
//
// Keys are the repository paths defined in layout.go, which is the single
// source of truth for the on-disk structure.
package storage

import (
	"context"
	"errors"
	"io"
	"iter"
	"time"
)

// ErrAlreadyExists is returned by PutIfAbsent when the key is taken.
//
// It is how a repository lock is acquired (EF-045), so it must mean exactly
// "someone else holds it" and never "the destination is unreachable": a caller
// that confuses the two either runs a second backup against a source already
// being backed up, or refuses to run at all.
var ErrAlreadyExists = errors.New("storage: object already exists")

// ErrNotFound is returned when a key does not exist.
//
// It is a sentinel rather than a per-backend error because retention, catalog
// rebuilding and restore all have to distinguish "absent" from "the destination
// is broken", and getting that wrong in either direction is damaging: treating
// a broken destination as absent deletes history, and treating an absent object
// as broken stops a prune that should have run.
var ErrNotFound = errors.New("storage: object not found")

// Storage is an object store.
//
// The contract every implementation must satisfy is executable: see
// storagetest.Suite. Three of its clauses are worth stating here because they
// are what a backup depends on and what a naive implementation gets wrong.
//
//   - Put is atomic. An object becomes visible complete or not at all. A reader
//     must never observe a half-written backup.
//   - A failed Put leaves nothing behind, and leaves any previous object at
//     that key untouched (ENF-010). P-007 found pg_basebackup creating its
//     output file before failing its own preflight check; a backend that
//     mirrored that would store an empty object and call it a backup.
//   - Delete is idempotent, so a prune interrupted between removing an object
//     and recording the removal can be retried.
type Storage interface {
	// Put streams r to key. It must not buffer the whole object: implementations
	// use multipart upload with a bounded part size (ENF-001).
	Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (ObjectInfo, error)

	// PutIfAbsent writes only if the key is free, and reports ErrAlreadyExists
	// otherwise. The check and the write are one operation: two Koffr instances
	// racing for the same lock must not both win, and a Stat followed by a Put
	// would let them.
	PutIfAbsent(ctx context.Context, key string, content []byte) error

	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// GetRange fetches part of an object, so reading a manifest or a single WAL
	// segment never pulls a whole base backup.
	GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error)

	Stat(ctx context.Context, key string) (ObjectInfo, error)
	List(ctx context.Context, prefix string) iter.Seq2[ObjectInfo, error]
	Delete(ctx context.Context, key string) error

	// Capabilities reports immutability support, so retention can warn when a
	// destination silently allows deletion (EF-042).
	Capabilities() Capabilities
}

// PutOptions tunes a single upload.
type PutOptions struct {
	PartSize int64

	// Immutable requests write-once semantics (S3 Object Lock and equivalents).
	Immutable   bool
	RetainUntil time.Time

	// OnProgress receives the running count of bytes read from the source, and
	// is called at least once with the final total. It feeds the byte-stall
	// watcher (EF-095), which is what turns a hung upload into a failed job
	// rather than one that never ends. It is called from the upload goroutine
	// and must not block.
	OnProgress func(bytes int64)
}

// ObjectInfo describes one stored object.
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time

	// ETag is whatever the backend reports. It is never used as an integrity
	// guarantee: that role belongs to the SHA-256 recorded in the manifest
	// (EF-053), which is verifiable without any key.
	ETag string
}

// Capabilities reports what a destination supports.
type Capabilities struct {
	Immutable      bool
	Multipart      bool
	RangeReads     bool
	ServerSideCopy bool

	// DeleteReclaimsSpace says whether removing an object actually gives the
	// bytes back.
	//
	// It is false on a versioned or Object-Locked bucket, where a delete writes
	// a marker and the data stays -- and stays billed -- until a lifecycle rule
	// expires it. Retention needs to know: a purge that reports freeing 190 MiB
	// on such a bucket has freed nothing, and reporting it anyway builds
	// confidence on a number that is not true.
	//
	// A filesystem always reclaims. This is phrased as the question an operator
	// asks rather than as two S3 facts, so a backend that does something else
	// again still has one honest answer to give.
	DeleteReclaimsSpace bool
}
