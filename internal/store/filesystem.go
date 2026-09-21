package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Gu1llaum-3/koffr/internal/domain/backup"
)

// Filesystem writes archives to a local directory — the destination of E-012a,
// and the one that proves the single interface of E-066 is implementable before
// S3 and SFTP have to fit it too.
type Filesystem struct {
	root string
}

// NewFilesystem builds a destination rooted at a directory.
func NewFilesystem(root string) *Filesystem {
	return &Filesystem{root: root}
}

// Write streams an archive into place. It writes beside the target and renames:
// a path that carries the name of an archive carries a **whole** archive, and
// half of one is worse than none because it looks restorable (BKP-11).
func (f *Filesystem) Write(_ context.Context, path string, from io.Reader) (int64, error) {
	final := f.resolve(path)

	if err := os.MkdirAll(filepath.Dir(final), 0o750); err != nil {
		return 0, fmt.Errorf("prepare %s: %w", filepath.Dir(path), err)
	}

	partial, err := os.CreateTemp(filepath.Dir(final), ".koffr-*.partial")
	if err != nil {
		return 0, fmt.Errorf("start writing %s: %w", path, err)
	}
	// Removed on every path that does not rename it away.
	defer func() { _ = os.Remove(partial.Name()) }()

	written, err := io.Copy(partial, from)
	if err != nil {
		_ = partial.Close()

		return 0, fmt.Errorf("write %s: %w", path, err)
	}

	// Flushed before the rename: a rename that publishes an unflushed file
	// publishes a file that is not there yet.
	if err := partial.Sync(); err != nil {
		_ = partial.Close()

		return 0, fmt.Errorf("flush %s: %w", path, err)
	}
	if err := partial.Close(); err != nil {
		return 0, fmt.Errorf("close %s: %w", path, err)
	}

	if err := os.Rename(partial.Name(), final); err != nil {
		return 0, fmt.Errorf("publish %s: %w", path, err)
	}

	return written, nil
}

// Read streams an archive back.
func (f *Filesystem) Read(_ context.Context, path string) (io.ReadCloser, error) {
	file, err := os.Open(f.resolve(path))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	return file, nil
}

// List returns what lies under a prefix, sorted, so that two listings taken at
// different times can be compared.
func (f *Filesystem) List(_ context.Context, prefix string) ([]backup.Entry, error) {
	var found []backup.Entry

	root := f.resolve(prefix)

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			// A prefix nobody has written to yet holds nothing. That is an
			// answer, not a failure.
			if errors.Is(err, os.ErrNotExist) {
				return filepath.SkipAll
			}

			return err
		}
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".partial") {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		relative, err := filepath.Rel(f.root, path)
		if err != nil {
			return fmt.Errorf("place %s under the destination: %w", path, err)
		}

		found = append(found, backup.Entry{
			Path:  filepath.ToSlash(relative),
			Bytes: info.Size(),
			At:    info.ModTime(),
		})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", prefix, err)
	}

	slices.SortFunc(found, func(a, b backup.Entry) int { return strings.Compare(a.Path, b.Path) })

	return found, nil
}

// Delete removes an archive. Removing what is already gone is not an error:
// retention runs again after a crash, and it must not fail on its own work.
func (f *Filesystem) Delete(_ context.Context, path string) error {
	if err := os.Remove(f.resolve(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete %s: %w", path, err)
	}

	return nil
}

// Check says whether this destination can be written to, and why not when it
// cannot. It writes a marker and removes it, leaving nothing behind (BKP-12).
func (f *Filesystem) Check(_ context.Context) error {
	if err := os.MkdirAll(f.root, 0o750); err != nil {
		return fmt.Errorf("the destination %s cannot be created: %w", f.root, err)
	}

	marker, err := os.CreateTemp(f.root, ".koffr-check-*")
	if err != nil {
		return fmt.Errorf("the destination %s cannot be written to: %w", f.root, err)
	}

	name := marker.Name()
	if err := marker.Close(); err != nil {
		_ = os.Remove(name)

		return fmt.Errorf("the destination %s cannot be written to: %w", f.root, err)
	}

	if err := os.Remove(name); err != nil {
		return fmt.Errorf("the destination %s cannot be cleaned up: %w", f.root, err)
	}

	return nil
}

// resolve keeps everything under the root, whatever the caller asked for.
func (f *Filesystem) resolve(path string) string {
	return filepath.Join(f.root, filepath.FromSlash(path))
}

// Filesystem is a Store, checked by the compiler rather than by hope.
var _ backup.Store = (*Filesystem)(nil)
