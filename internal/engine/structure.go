package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
	"github.com/Gu1llaum-3/koffr/internal/domain/verify"
)

// StructureWatcher observes a dump **as it streams past** and says whether the
// engine still recognises it as one.
//
// It is written to by an io.TeeReader, never by a second read of the dump:
// E-025 forbids materialising it, and reading it twice would double the load on
// a production server. And it happens at backup time because that is the only
// moment koffr holds the plaintext — it has no private key (ADR-0017, E-113).
type StructureWatcher interface {
	io.Writer

	// Conclude says what went past. Called once, after the stream has ended.
	Conclude(ctx context.Context) verify.Structure

	// Kept is how many bytes the watcher is holding. A watcher that grows with
	// the dump would break E-025, so a test asserts this stays bounded.
	Kept() int
}

// endMarker is what mysqldump and mariadb-dump write on their last line. Its
// absence is the failure a checksum cannot see: the archive is intact, and the
// dump is incomplete.
const endMarker = "-- Dump completed"

// keptTail is how much of the end is held to look for that marker. The marker
// and its timestamp fit in a line; a few kilobytes cover any line ending.
const keptTail = 4 << 10

// WatchStructure builds the watcher for a family. PostgreSQL needs its restore
// tool — the table of contents is read by pg_restore, not by us.
func WatchStructure(family resolve.Family, restore resolve.Candidate) (StructureWatcher, error) {
	switch family {
	case resolve.PostgreSQL:
		return newTableOfContents(restore)

	case resolve.MySQL, resolve.MariaDB:
		return &endMarkerWatcher{}, nil

	default:
		return nil, fmt.Errorf("%w: %q", resolve.ErrUnsupportedEngine, family)
	}
}

// endMarkerWatcher keeps the last bytes of the flow and nothing else.
type endMarkerWatcher struct {
	tail []byte
	seen int
}

func (e *endMarkerWatcher) Write(p []byte) (int, error) {
	e.seen += len(p)
	e.tail = append(e.tail, p...)

	if len(e.tail) > keptTail {
		e.tail = e.tail[len(e.tail)-keptTail:]
	}

	return len(p), nil
}

func (e *endMarkerWatcher) Kept() int { return len(e.tail) }

func (e *endMarkerWatcher) Conclude(context.Context) verify.Structure {
	if e.seen == 0 {
		return verify.Refused("the dump is empty")
	}

	// At the end, not anywhere: a row of data that quotes the marker is not a
	// dump that finished.
	if !bytes.Contains(e.tail, []byte(endMarker)) {
		return verify.Refused(fmt.Sprintf(
			"the dump does not end with %q: it was cut before its last line", endMarker))
	}

	return verify.Sound(lastLine(e.tail))
}

func lastLine(of []byte) string {
	lines := strings.Split(strings.TrimRight(string(of), "\n"), "\n")

	return strings.TrimSpace(lines[len(lines)-1])
}

// tableOfContents feeds the flow to `pg_restore --list` and lets **it** decide
// when it has read enough. Nothing is buffered: pg_restore reads the table of
// contents at the head of a custom-format dump and exits, at which point the
// pipe breaks and the rest of the dump is discarded as it goes by.
type tableOfContents struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	out     *tail
	said    *tail

	cancel context.CancelFunc
	once   sync.Once
	closed bool
	seen   int
}

// keptListing bounds the table of contents koffr reports. A schema of tens of
// thousands of objects produces a long listing, and the manifest keeps the
// beginning of it rather than all of it.
const keptListing = 64 << 10

func newTableOfContents(restore resolve.Candidate) (*tableOfContents, error) {
	if restore.Path == "" {
		return nil, fmt.Errorf("no %s tool was resolved, so the table of contents cannot be read",
			resolve.Restore)
	}

	ctx, cancel := context.WithCancel(context.Background())

	command := exec.CommandContext(ctx, restore.Path, "--list")
	command.WaitDelay = 5 * time.Second

	watcher := &tableOfContents{
		command: command,
		out:     &tail{limit: keptListing, head: true},
		said:    &tail{limit: keptFromStderr},
		cancel:  cancel,
	}

	command.Stdout = watcher.out
	command.Stderr = watcher.said

	stdin, err := command.StdinPipe()
	if err != nil {
		cancel()

		return nil, fmt.Errorf("feed %s: %w", restore.Path, err)
	}
	watcher.stdin = stdin

	if err := command.Start(); err != nil {
		cancel()

		return nil, fmt.Errorf("start %s: %w", restore.Path, err)
	}

	return watcher, nil
}

func (t *tableOfContents) Write(p []byte) (int, error) {
	t.seen += len(p)

	// pg_restore stops reading once it has the table of contents, and the pipe
	// breaks. That is the normal end of this conversation, not an error: the
	// rest of the dump goes past and is discarded.
	if t.closed {
		return len(p), nil
	}

	if _, err := t.stdin.Write(p); err != nil {
		t.closed = true
		_ = t.stdin.Close()
	}

	return len(p), nil
}

// Kept is zero: the flow is forwarded, never held.
func (t *tableOfContents) Kept() int { return 0 }

func (t *tableOfContents) Conclude(context.Context) verify.Structure {
	t.once.Do(func() {
		if !t.closed {
			_ = t.stdin.Close()
		}
	})

	err := t.command.Wait()
	t.cancel()

	listing := strings.TrimSpace(t.out.String())

	switch {
	case t.seen == 0:
		return verify.Refused("the dump is empty")

	case err != nil:
		return verify.Refused(fmt.Sprintf("%s refused it: %v%s", t.command.Path, err, t.said.suffix()))

	case !strings.Contains(listing, "TOC Entries"):
		return verify.Refused(fmt.Sprintf(
			"%s read no table of contents from it%s", t.command.Path, t.said.suffix()))
	}

	return verify.Sound(listing)
}
