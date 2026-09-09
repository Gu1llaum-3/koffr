package binlog

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/Gu1llaum-3/koffr/internal/crypto"
	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// Archive is one source's binary-log archive in a repository.
//
// The repository is the truth (ADR-0004): what is archived is what a listing
// says, and nothing here keeps a second opinion in a database. The index beside
// each object carries what a listing cannot -- the digest, the plain size and
// when the file's first event happened -- and it is written after the object,
// never before, so an index always names something that exists.
type Archive struct {
	Source  storage.Source
	Storage storage.Storage
	Sealer  crypto.Sealer
}

// Entry is the index of one archived file.
type Entry struct {
	Name string `json:"name"`
	// PlainSize is the file as the server wrote it; StoredSize is after zstd and
	// age. The ratio is what an operator sizing a destination wants to know.
	PlainSize  int64  `json:"plain_size"`
	StoredSize int64  `json:"stored_size"`
	SHA256     string `json:"sha256"`
	// FirstEventAt is the timestamp of the file's first event, which is what
	// decides whether a file is needed to reach a point in time. Read from the
	// file's own header, not from its mtime: mtime is when the receiver closed
	// it, which on a quiet server can be hours after the events inside it.
	FirstEventAt time.Time `json:"first_event_at"`
	ArchivedAt   time.Time `json:"archived_at"`
	Codec        string    `json:"codec"`
	Encryption   string    `json:"encryption"`
	Recipients   []string  `json:"recipients"`
}

// Archived lists what the repository holds, from the objects and not the
// indexes: an object whose index was never written -- a crash between the two
// -- is still an archived file, and Reindex is how it gets its index back.
func (a *Archive) Archived(ctx context.Context) ([]Name, error) {
	var names []Name
	for info, err := range a.Storage.List(ctx, a.Source.BinlogPrefix()) {
		if err != nil {
			return nil, fmt.Errorf("binlog: list the archive: %w", err)
		}
		name, isObject := storage.ParseBinlogKey(info.Key)
		if !isObject {
			continue
		}
		n, err := Parse(name)
		if err != nil {
			// Not ours. Something else put a file under binlog/; reporting it as
			// a hole in the sequence would be wrong, and deleting it would be
			// worse. Left alone, and left out.
			continue
		}
		names = append(names, n)
	}
	return names, nil
}

// Indexed reports whether the index of an archived file exists.
func (a *Archive) Indexed(ctx context.Context, n Name) (bool, error) {
	key, err := a.Source.BinlogIndexKey(n.String())
	if err != nil {
		return false, err
	}
	_, err = a.Storage.Stat(ctx, key)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, storage.ErrNotFound):
		return false, nil
	default:
		return false, fmt.Errorf("binlog: stat index of %s: %w", n, err)
	}
}

// Store seals one closed spool file into the repository and indexes it.
//
// Streamed, never buffered: a binary log is max_binlog_size, a gigabyte by
// default, and a version that read it into memory would be a version that
// worked in tests and not in production (ENF-001). The digest covers the
// ciphertext, so the object can be checked without a key (EF-053).
func (a *Archive) Store(ctx context.Context, spoolPath string, n Name, now time.Time) (Entry, error) {
	key, err := a.Source.BinlogKey(n.String())
	if err != nil {
		return Entry{}, err
	}

	f, err := os.Open(spoolPath) //nolint:gosec // a path the spool scanner produced
	if err != nil {
		return Entry{}, fmt.Errorf("binlog: open %s: %w", n, err)
	}
	defer func() { _ = f.Close() }()

	firstEvent, err := firstEventTime(f)
	if err != nil {
		return Entry{}, fmt.Errorf("binlog: %s is not a binary log: %w", n, err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Entry{}, err
	}

	digest := sha256.New()
	pr, pw := io.Pipe()
	var plain int64
	go func() {
		w, err := a.Sealer.Seal(io.MultiWriter(pw, digest))
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		z, err := zstd.NewWriter(w)
		if err != nil {
			_ = w.Close()
			_ = pw.CloseWithError(err)
			return
		}
		plain, err = io.Copy(z, f)
		if err == nil {
			err = z.Close()
		} else {
			_ = z.Close()
		}
		if err == nil {
			// Closing writes age's final chunk marker; without it the object
			// reads as truncated.
			err = w.Close()
		} else {
			_ = w.Close()
		}
		_ = pw.CloseWithError(err)
	}()

	info, err := a.Storage.Put(ctx, key, pr, storage.PutOptions{})
	if err != nil {
		// Unblock the sealing goroutine, which is otherwise writing into a pipe
		// nobody reads.
		_ = pr.CloseWithError(err)
		return Entry{}, fmt.Errorf("binlog: store %s: %w", n, err)
	}

	e := Entry{
		Name:         n.String(),
		PlainSize:    plain,
		StoredSize:   info.Size,
		SHA256:       hex.EncodeToString(digest.Sum(nil)),
		FirstEventAt: firstEvent.UTC(),
		ArchivedAt:   now.UTC(),
		Codec:        "zstd",
		Encryption:   "age",
		Recipients:   a.Sealer.Recipients(),
	}
	if err := a.writeIndex(ctx, e); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// Reindex rebuilds the index of an archived file whose index is missing.
//
// It reads the object back, which costs a download: a crash between object and
// index is rare, and paying for it once beats keeping a second copy of the
// digest somewhere that could disagree with the object.
func (a *Archive) Reindex(ctx context.Context, n Name, opener crypto.Opener, now time.Time) (Entry, error) {
	key, err := a.Source.BinlogKey(n.String())
	if err != nil {
		return Entry{}, err
	}
	info, err := a.Storage.Stat(ctx, key)
	if err != nil {
		return Entry{}, fmt.Errorf("binlog: stat %s: %w", n, err)
	}
	rc, err := a.Storage.Get(ctx, key)
	if err != nil {
		return Entry{}, fmt.Errorf("binlog: read back %s: %w", n, err)
	}
	defer func() { _ = rc.Close() }()

	digest := sha256.New()
	unsealed, err := opener.Open(io.TeeReader(rc, digest))
	if err != nil {
		return Entry{}, fmt.Errorf("binlog: unseal %s: %w", n, err)
	}
	z, err := zstd.NewReader(unsealed)
	if err != nil {
		return Entry{}, fmt.Errorf("binlog: decompress %s: %w", n, err)
	}
	defer z.Close()

	head := make([]byte, headerBytes)
	if _, err := io.ReadFull(z, head); err != nil {
		return Entry{}, fmt.Errorf("binlog: %s is not a binary log: %w", n, err)
	}
	firstEvent, err := firstEventTime(strings.NewReader(string(head)))
	if err != nil {
		return Entry{}, err
	}
	rest, err := io.Copy(io.Discard, z)
	if err != nil {
		return Entry{}, fmt.Errorf("binlog: read back %s: %w", n, err)
	}

	e := Entry{
		Name:         n.String(),
		PlainSize:    int64(headerBytes) + rest,
		StoredSize:   info.Size,
		SHA256:       hex.EncodeToString(digest.Sum(nil)),
		FirstEventAt: firstEvent.UTC(),
		ArchivedAt:   now.UTC(),
		Codec:        "zstd",
		Encryption:   "age",
		Recipients:   a.Sealer.Recipients(),
	}
	return e, a.writeIndex(ctx, e)
}

// Index reads one entry.
func (a *Archive) Index(ctx context.Context, n Name) (Entry, error) {
	key, err := a.Source.BinlogIndexKey(n.String())
	if err != nil {
		return Entry{}, err
	}
	rc, err := a.Storage.Get(ctx, key)
	if err != nil {
		return Entry{}, fmt.Errorf("binlog: read index of %s: %w", n, err)
	}
	defer func() { _ = rc.Close() }()
	var e Entry
	if err := json.NewDecoder(rc).Decode(&e); err != nil {
		return Entry{}, fmt.Errorf("binlog: index of %s is not readable: %w", n, err)
	}
	return e, nil
}

func (a *Archive) writeIndex(ctx context.Context, e Entry) error {
	key, err := a.Source.BinlogIndexKey(e.Name)
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	if _, err := a.Storage.Put(ctx, key, strings.NewReader(string(body)+"\n"), storage.PutOptions{}); err != nil {
		return fmt.Errorf("binlog: write index of %s: %w", e.Name, err)
	}
	return nil
}

// Delete removes an archived file and its index. The index goes first: an
// object without an index is recoverable (Reindex), an index without an object
// is a lie.
func (a *Archive) Delete(ctx context.Context, n Name) (int64, error) {
	idx, err := a.Source.BinlogIndexKey(n.String())
	if err != nil {
		return 0, err
	}
	key, err := a.Source.BinlogKey(n.String())
	if err != nil {
		return 0, err
	}
	if err := a.Storage.Delete(ctx, idx); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return 0, fmt.Errorf("binlog: delete index of %s: %w", n, err)
	}
	info, statErr := a.Storage.Stat(ctx, key)
	if err := a.Storage.Delete(ctx, key); err != nil {
		return 0, fmt.Errorf("binlog: delete %s: %w", n, err)
	}
	if statErr != nil {
		return 0, nil //nolint:nilerr // the object was already gone: nothing to count, nothing to report
	}
	return info.Size, nil
}

// headerBytes is the binary-log magic plus one event header: enough to read the
// first event's timestamp.
const headerBytes = 4 + 19

var binlogMagic = []byte{0xfe, 'b', 'i', 'n'}

// firstEventTime reads the timestamp of a binary log's first event.
//
// The format is stable across every MariaDB and MySQL version: four magic
// bytes, then events, each starting with a four-byte Unix timestamp. The first
// event is the format description, written when the file is opened -- which is
// exactly when the file starts being relevant to a point in time.
func firstEventTime(r io.Reader) (time.Time, error) {
	head := make([]byte, headerBytes)
	if _, err := io.ReadFull(r, head); err != nil {
		return time.Time{}, fmt.Errorf("too short for a binary log: %w", err)
	}
	if string(head[:4]) != string(binlogMagic) {
		return time.Time{}, errors.New("missing the binary log magic")
	}
	ts := binary.LittleEndian.Uint32(head[4:8])
	if ts == 0 {
		return time.Time{}, errors.New("first event carries no timestamp")
	}
	return time.Unix(int64(ts), 0), nil
}

// spoolName is the spool file a name lives in.
func spoolName(spoolDir string, n Name) string { return path.Join(spoolDir, n.String()) }
