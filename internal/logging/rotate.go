package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	defaultMaxSize  = 10 << 20
	defaultMaxFiles = 5
)

// rotator is a size-limited log file.
//
// Written here rather than taken as a dependency because it is small, and
// because the two properties that matter are easy to state and easy to get
// wrong: a line is never split across a rotation, and the live file always
// keeps its configured name so `tail -f` and any log shipper survive one.
type rotator struct {
	path     string
	maxSize  int64
	maxFiles int

	mu   sync.Mutex
	file *os.File
	size int64
}

func newRotator(path string, maxSize int64, maxFiles int) (*rotator, error) {
	if path == "" {
		return nil, errNoPath
	}
	if maxSize <= 0 {
		maxSize = defaultMaxSize
	}
	if maxFiles <= 0 {
		maxFiles = defaultMaxFiles
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("logging: create the log directory: %w", err)
	}

	r := &rotator{path: path, maxSize: maxSize, maxFiles: maxFiles}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

// Write appends one log record.
//
// slog hands over a whole record per call, so rotating between calls is what
// keeps a line whole. Rotating on a byte count inside a line would leave half a
// JSON document at the end of a file and half at the start of the next, and a
// log nobody can parse is a log nobody reads.
func (r *rotator) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.size > 0 && r.size+int64(len(p)) > r.maxSize {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}

// Close flushes and releases the file.
func (r *rotator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

func (r *rotator) open() error {
	// 0600: a log holds error messages naming sources, hosts and schedules --
	// not secrets, deliberately, but not a public document either.
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("logging: open %s: %w", r.path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("logging: stat %s: %w", r.path, err)
	}
	r.file, r.size = f, info.Size()
	return nil
}

// rotate renames the live file out of the way and starts a new one.
func (r *rotator) rotate() error {
	if err := r.file.Close(); err != nil {
		return fmt.Errorf("logging: close %s before rotating: %w", r.path, err)
	}
	// Numbered rather than timestamped: a name that sorts is a name a human can
	// follow, and .1 is always the most recent of the old ones.
	for i := r.maxFiles - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", r.path, i)
		to := fmt.Sprintf("%s.%d", r.path, i+1)
		if _, err := os.Stat(from); err == nil {
			_ = os.Rename(from, to)
		}
	}
	if err := os.Rename(r.path, r.path+".1"); err != nil {
		return fmt.Errorf("logging: rotate %s: %w", r.path, err)
	}

	if err := r.open(); err != nil {
		return err
	}
	r.size = 0
	r.prune()
	return nil
}

// prune drops what is past the limit.
//
// The point of the limit is that a daemon cannot fill the disk its own backups
// need, so a failure to delete is not worth failing a write over -- but it is
// worth not silently growing either, which is why the count is enforced on
// every rotation rather than at start-up.
func (r *rotator) prune() {
	dir := filepath.Dir(r.path)
	base := filepath.Base(r.path)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	// Sorted by the number, not by the name. Lexically, ".1" ".10" ".2" is the
	// order, so with more than nine files kept the pruning would delete the
	// second-newest and keep the oldest -- the exact opposite of the job.
	type rotatedFile struct {
		name string
		n    int
	}
	var rotated []rotatedFile
	for _, e := range entries {
		suffix, found := strings.CutPrefix(e.Name(), base+".")
		if !found {
			continue
		}
		n, err := strconv.Atoi(suffix)
		if err != nil {
			continue
		}
		rotated = append(rotated, rotatedFile{name: e.Name(), n: n})
	}
	sort.Slice(rotated, func(i, j int) bool { return rotated[i].n < rotated[j].n })

	// maxFiles counts the live one, so this many rotated files may remain.
	keep := max(r.maxFiles-1, 0)
	for i := keep; i < len(rotated); i++ {
		_ = os.Remove(filepath.Join(dir, rotated[i].name))
	}
}
