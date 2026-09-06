// Package fs stores backups on a local or mounted filesystem.
//
// It is the simplest destination and the one used when a repository lives on a
// NAS mount or a directly attached disk. It is also the reference against which
// the contract suite was written.
package fs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// partialPrefix marks an upload in flight. Such a file is never a repository
// object: it is filtered from listings and removed on failure, so a crash mid
// upload leaves litter rather than a backup that looks complete.
const partialPrefix = ".koffr-partial-"

// dirPerm and filePerm keep a repository private. Backups are encrypted, but
// the manifest is deliberately plaintext (EF-055) and says which databases
// exist and when they were last backed up.
const (
	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600
)

// Storage is a filesystem-backed object store rooted at a directory.
type Storage struct{ root string }

// New opens (and creates if needed) a repository rooted at dir.
func New(dir string) (*Storage, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", dir, err)
	}
	if err := os.MkdirAll(abs, dirPerm); err != nil {
		return nil, fmt.Errorf("create repository directory %q: %w", abs, err)
	}
	return &Storage{root: abs}, nil
}

// path maps a repository key onto a filesystem path.
//
// Keys are built solely by layout.go, which already rejects traversal, but this
// is the boundary where a mistake escapes the repository, so it checks again.
func (s *Storage) path(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("storage: empty key")
	}
	p := filepath.Join(s.root, filepath.FromSlash(key))
	if p != s.root && !strings.HasPrefix(p, s.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("storage: key %q escapes the repository", key)
	}
	return p, nil
}

// Put writes an object atomically: the content goes to a temporary file in the
// destination directory, and only a successful rename makes it visible.
//
// Nothing is created at the destination key until the write succeeds, so a
// source that fails immediately -- or halfway -- leaves no object, and leaves
// any previous object at that key untouched (ENF-010).
func (s *Storage) Put(ctx context.Context, key string, r io.Reader, opts storage.PutOptions) (storage.ObjectInfo, error) {
	if opts.Immutable {
		return storage.ObjectInfo{}, fmt.Errorf(
			"storage/fs: immutability was requested but a filesystem cannot enforce it; " +
				"use an S3 destination with Object Lock, or drop the requirement explicitly")
	}
	if err := ctx.Err(); err != nil {
		return storage.ObjectInfo{}, err
	}

	dest, err := s.path(key)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("create %q: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, partialPrefix+"*")
	if err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("create temporary file in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Cleanup runs on every path that does not reach the rename. Both calls are
	// best-effort: the file is already lost to us, and reporting a cleanup
	// failure would mask the real error.
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(filePerm); err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("set permissions on %q: %w", tmpName, err)
	}

	written, err := io.Copy(tmp, &progressReader{ctx: ctx, r: r, onProgress: opts.OnProgress})
	if err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("write %q: %w", key, err)
	}
	// A backup that is only in the page cache is not a backup: fsync before the
	// rename, so a crash cannot leave a visible object full of zeroes.
	if err := tmp.Sync(); err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("sync %q: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("close %q: %w", key, err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("publish %q: %w", key, err)
	}
	committed = true

	info, err := os.Stat(dest)
	if err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("stat %q after write: %w", key, err)
	}
	return storage.ObjectInfo{
		Key:          key,
		Size:         written,
		LastModified: info.ModTime(),
	}, nil
}

// PutIfAbsent creates an object only if the key is free.
//
// O_EXCL makes the check and the creation one syscall, which is what a lock
// needs. A Stat followed by a Put would let two Koffr instances through the gap
// between them, and both would then back up the same source at once.
func (s *Storage) PutIfAbsent(ctx context.Context, key string, content []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dest, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), dirPerm); err != nil {
		return fmt.Errorf("create %q: %w", filepath.Dir(dest), err)
	}

	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm) //nolint:gosec // dest is confined to the repository by path
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%q: %w", key, storage.ErrAlreadyExists)
		}
		return fmt.Errorf("create %q: %w", key, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(content); err != nil {
		// The holder is us, so removing it is right: a lock file nobody holds
		// would block every later attempt.
		_ = os.Remove(dest)
		return fmt.Errorf("write %q: %w", key, err)
	}
	if err := f.Sync(); err != nil {
		_ = os.Remove(dest)
		return fmt.Errorf("sync %q: %w", key, err)
	}
	return nil
}

// Get opens an object for reading.
func (s *Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p) //nolint:gosec // p is confined to the repository by path
	if err != nil {
		return nil, translate(key, err)
	}
	return f, nil
}

// GetRange opens part of an object. A length of -1 reads to the end.
func (s *Storage) GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if offset < 0 {
		return nil, fmt.Errorf("storage: negative offset %d for %q", offset, key)
	}
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p) //nolint:gosec // p is confined to the repository by path
	if err != nil {
		return nil, translate(key, err)
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("stat %q: %w", key, err)
	}
	if offset >= info.Size() {
		_ = f.Close()
		// Not ErrNotFound: the object exists, the range does not. Conflating
		// them would make a caller delete an object it merely mis-addressed.
		return nil, fmt.Errorf("storage: offset %d is past the end of %q (%d bytes)",
			offset, key, info.Size())
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("seek %q: %w", key, err)
	}
	if length < 0 {
		return f, nil
	}
	return &limitedFile{Reader: io.LimitReader(f, length), closer: f}, nil
}

// Stat reports an object's size and modification time.
func (s *Storage) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return storage.ObjectInfo{}, err
	}
	p, err := s.path(key)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return storage.ObjectInfo{}, translate(key, err)
	}
	if info.IsDir() {
		return storage.ObjectInfo{}, fmt.Errorf("%q: %w", key, storage.ErrNotFound)
	}
	return storage.ObjectInfo{Key: key, Size: info.Size(), LastModified: info.ModTime()}, nil
}

// List walks the repository, yielding every object whose key starts with prefix.
//
// The prefix is a string prefix, not a directory: "sources/a" matches
// "sources/ab/..." as well, which is what callers building keys from layout.go
// expect.
func (s *Storage) List(ctx context.Context, prefix string) iter.Seq2[storage.ObjectInfo, error] {
	return func(yield func(storage.ObjectInfo, error) bool) {
		walkErr := filepath.WalkDir(s.root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if d.IsDir() || strings.HasPrefix(d.Name(), partialPrefix) {
				return nil
			}

			rel, relErr := filepath.Rel(s.root, p)
			if relErr != nil {
				return relErr
			}
			key := filepath.ToSlash(rel)
			if !strings.HasPrefix(key, prefix) {
				return nil
			}

			info, infoErr := d.Info()
			if infoErr != nil {
				return infoErr
			}
			if !yield(storage.ObjectInfo{
				Key: key, Size: info.Size(), LastModified: info.ModTime(),
			}, nil) {
				return errStopIteration
			}
			return nil
		})
		if walkErr != nil && !errors.Is(walkErr, errStopIteration) {
			yield(storage.ObjectInfo{}, fmt.Errorf("list %q: %w", prefix, walkErr))
		}
	}
}

// errStopIteration unwinds WalkDir when the consumer breaks out of the range.
var errStopIteration = errors.New("stop iteration")

// Delete removes an object. Removing something that is already gone succeeds,
// so an interrupted prune can be retried.
func (s *Storage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete %q: %w", key, err)
	}
	s.pruneEmptyDirs(filepath.Dir(p))
	return nil
}

// pruneEmptyDirs removes directories a deletion left behind, up to the root.
//
// S3 has no directories, so nothing there needs this and the Storage contract
// does not mention it. A filesystem does, and an operator asking "did the purge
// work" answers it with ls rather than with an API -- a repository full of
// empty directories says no even when it worked.
//
// Errors are ignored on purpose. os.Remove refuses a directory that is not
// empty, which is exactly the stop condition, and a concurrent writer that has
// just created one wins the race harmlessly.
func (s *Storage) pruneEmptyDirs(dir string) {
	for dir != s.root && strings.HasPrefix(dir, s.root) {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// Capabilities reports what a filesystem can honestly do.
func (s *Storage) Capabilities() storage.Capabilities {
	return storage.Capabilities{
		// Immutability would need the filesystem's own controls, which vary
		// too much to promise anything.
		Immutable:  false,
		Multipart:  false,
		RangeReads: true,
		// A filesystem gives the bytes back when the file goes, which is the
		// one thing it is unambiguously good at.
		DeleteReclaimsSpace: true,
	}
}

// translate maps a filesystem error onto the storage sentinel.
func translate(key string, err error) error {
	if os.IsNotExist(err) {
		return fmt.Errorf("%q: %w", key, storage.ErrNotFound)
	}
	return fmt.Errorf("open %q: %w", key, err)
}

// progressReader reports bytes read and honours cancellation between reads.
//
// Cancellation is checked here rather than around io.Copy so that a long
// upload stops promptly instead of at the end of the object.
type progressReader struct {
	ctx        context.Context
	r          io.Reader
	onProgress func(int64)
	total      int64
}

func (p *progressReader) Read(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := p.r.Read(b)
	if n > 0 {
		p.total += int64(n)
		if p.onProgress != nil {
			p.onProgress(p.total)
		}
	}
	return n, err
}

// limitedFile bounds a read while still closing the underlying file.
type limitedFile struct {
	io.Reader
	closer io.Closer
}

func (l *limitedFile) Close() error { return l.closer.Close() }

// Compile-time check that the contract is implemented.
var _ storage.Storage = (*Storage)(nil)
