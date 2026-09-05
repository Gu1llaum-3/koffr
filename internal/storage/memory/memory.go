// Package memory keeps a repository in memory.
//
// It exists so that tests of everything above storage can use a real backend
// rather than a hand-written double. A double drifts from the interface it
// pretends to implement; this one runs the same contract suite as fs and s3, so
// a test that passes against it is testing against behaviour that is actually
// required.
//
// Not for production: nothing survives the process.
package memory

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"iter"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// Storage is an in-memory object store.
type Storage struct {
	mu      sync.RWMutex
	objects map[string]object
	now     func() time.Time
}

type object struct {
	content  []byte
	modified time.Time
}

// New returns an empty store.
func New() *Storage {
	return &Storage{objects: make(map[string]object), now: time.Now}
}

// Put stores an object.
//
// The content is buffered before anything is recorded, so a reader that fails
// partway leaves nothing behind and leaves a previous object untouched
// (ENF-010). That is the same guarantee fs gets from a rename and s3 from a
// multipart completion.
func (s *Storage) Put(ctx context.Context, key string, r io.Reader, opts storage.PutOptions) (storage.ObjectInfo, error) {
	if opts.Immutable {
		return storage.ObjectInfo{}, fmt.Errorf(
			"storage/memory: immutability was requested but memory cannot enforce it")
	}
	if err := ctx.Err(); err != nil {
		return storage.ObjectInfo{}, err
	}

	var buf bytes.Buffer
	chunk := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return storage.ObjectInfo{}, err
		}
		n, err := r.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
			if opts.OnProgress != nil {
				opts.OnProgress(int64(buf.Len()))
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			// The reader's error must survive: it is what tells a source
			// failure from a storage one.
			return storage.ObjectInfo{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	info := storage.ObjectInfo{Key: key, Size: int64(buf.Len()), LastModified: s.now()}
	s.objects[key] = object{content: buf.Bytes(), modified: info.LastModified}
	return info, nil
}

// PutIfAbsent writes only if the key is free.
func (s *Storage) PutIfAbsent(ctx context.Context, key string, content []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, taken := s.objects[key]; taken {
		return fmt.Errorf("%q: %w", key, storage.ErrAlreadyExists)
	}
	s.objects[key] = object{content: slices.Clone(content), modified: s.now()}
	return nil
}

func (s *Storage) get(key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("%q: %w", key, storage.ErrNotFound)
	}
	return o.content, nil
}

// Get opens an object for reading.
func (s *Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	content, err := s.get(key)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

// GetRange opens part of an object. A length of -1 reads to the end.
func (s *Storage) GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if offset < 0 {
		return nil, fmt.Errorf("storage/memory: negative offset %d for %q", offset, key)
	}
	content, err := s.get(key)
	if err != nil {
		return nil, err
	}
	if offset >= int64(len(content)) {
		return nil, fmt.Errorf("storage/memory: offset %d is past the end of %q (%d bytes)",
			offset, key, len(content))
	}
	content = content[offset:]
	if length >= 0 && length < int64(len(content)) {
		content = content[:length]
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

// Stat reports an object's size and modification time.
func (s *Storage) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return storage.ObjectInfo{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.objects[key]
	if !ok {
		return storage.ObjectInfo{}, fmt.Errorf("%q: %w", key, storage.ErrNotFound)
	}
	return storage.ObjectInfo{Key: key, Size: int64(len(o.content)), LastModified: o.modified}, nil
}

// List yields every object whose key starts with prefix.
func (s *Storage) List(ctx context.Context, prefix string) iter.Seq2[storage.ObjectInfo, error] {
	return func(yield func(storage.ObjectInfo, error) bool) {
		s.mu.RLock()
		keys := slices.Sorted(maps.Keys(s.objects))
		infos := make([]storage.ObjectInfo, 0, len(keys))
		for _, k := range keys {
			if !strings.HasPrefix(k, prefix) {
				continue
			}
			o := s.objects[k]
			infos = append(infos, storage.ObjectInfo{
				Key: k, Size: int64(len(o.content)), LastModified: o.modified,
			})
		}
		s.mu.RUnlock()

		for _, info := range infos {
			if err := ctx.Err(); err != nil {
				yield(storage.ObjectInfo{}, err)
				return
			}
			if !yield(info, nil) {
				return
			}
		}
	}
}

// Delete removes an object, and succeeds if it was already gone.
func (s *Storage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

// Capabilities reports what memory can honestly do.
func (s *Storage) Capabilities() storage.Capabilities {
	return storage.Capabilities{Immutable: false, Multipart: false, RangeReads: true}
}

var _ storage.Storage = (*Storage)(nil)
