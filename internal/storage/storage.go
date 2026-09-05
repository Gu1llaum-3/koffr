// Package storage persists backup objects to a destination.
//
// Keys are the repository paths defined in layout.go, which is the single
// source of truth for the on-disk structure.
package storage

import (
	"context"
	"io"
	"iter"
	"time"
)

// Storage is an object store.
type Storage interface {
	// Put streams r to key. It must not buffer the whole object: implementations
	// use multipart upload with a bounded part size (ENF-001).
	Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (ObjectInfo, error)

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

	// OnProgress receives the running byte count. It is called from the upload
	// goroutine and must not block.
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
}
