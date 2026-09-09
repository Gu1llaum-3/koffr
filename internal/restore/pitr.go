package restore

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/Gu1llaum-3/koffr/internal/binlog"
	"github.com/Gu1llaum-3/koffr/internal/crypto"
	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/manifest"
	"github.com/Gu1llaum-3/koffr/internal/source/mariadb"
	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// Target is where a point-in-time recovery stops (EF-082).
//
// Exactly one of Time and Position is set. Time is what an operator has --
// "just before the DELETE at 15:42" -- and is cut at whole seconds by the
// tool; Position is exact, for when the second is not fine enough.
type Target struct {
	Time     time.Time
	Position uint64
	// PositionFile names the file Position is in. A position alone means
	// nothing: every file starts near zero.
	PositionFile string
}

func (t Target) isZero() bool { return t.Time.IsZero() && t.Position == 0 }

// ErrBinlogGap says the archive is missing a file the replay needs.
var ErrBinlogGap = errors.New("restore: the binary log archive has a hole")

// ErrTargetBeforeAnchor says the requested point is earlier than the backup.
var ErrTargetBeforeAnchor = errors.New("restore: the target is before the backup's own position")

// PITR replays archived binary logs onto a restored MariaDB database.
type PITR struct {
	Config  mariadb.Config
	Archive *binlog.Archive
	Opener  crypto.Opener
	// Workdir is where decrypted files wait for mariadb-binlog, which reads
	// paths and not pipes. Emptied on the way out, and bounded by the files a
	// replay needs -- restore-time disk, like --prepare needs (CT-005).
	Workdir string
}

// Plan lists the archived files a replay from anchor to target needs, in
// order, and refuses if any is missing.
//
// A hole is a refusal and never a partial replay: a replay that skipped a file
// and reported success would have rebuilt a database that never existed.
func (p PITR) Plan(ctx context.Context, anchor manifest.MariaDBDetails, target Target) ([]binlog.Name, error) {
	if anchor.BinlogFile == "" {
		return nil, errors.New("restore: this backup carries no binary log position, so there is nothing to replay from; " +
			"a point-in-time recovery needs a backup taken with the binary log on and RELOAD granted")
	}
	start, err := binlog.Parse(anchor.BinlogFile)
	if err != nil {
		return nil, fmt.Errorf("restore: the backup anchors to %q: %w", anchor.BinlogFile, err)
	}
	if target.PositionFile != "" {
		end, err := binlog.Parse(target.PositionFile)
		if err != nil {
			return nil, err
		}
		if end.Base != start.Base || end.Seq < start.Seq ||
			(end.Seq == start.Seq && target.Position < anchor.BinlogPos) {
			return nil, fmt.Errorf("%w: backup at %s:%d, target %s:%d",
				ErrTargetBeforeAnchor, anchor.BinlogFile, anchor.BinlogPos, target.PositionFile, target.Position)
		}
	}

	archived, err := p.Archive.Archived(ctx)
	if err != nil {
		return nil, err
	}
	if err := binlog.Sort(archived); err != nil {
		return nil, err
	}

	var files []binlog.Name
	for _, n := range archived {
		if n.Base != start.Base || n.Seq < start.Seq {
			continue
		}
		if target.PositionFile != "" {
			end, _ := binlog.Parse(target.PositionFile)
			if n.Seq > end.Seq {
				break
			}
		}
		if !target.Time.IsZero() && len(files) > 0 {
			// A file whose first event is already past the target holds
			// nothing the replay wants, and mariadb-binlog would read it for
			// nothing. The one straddling the target is kept: --stop-datetime
			// cuts inside it.
			e, err := p.Archive.Index(ctx, n)
			if err != nil {
				return nil, err
			}
			if e.FirstEventAt.After(target.Time) {
				break
			}
		}
		files = append(files, n)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("%w: the file the backup anchors to, %s, is not archived",
			ErrBinlogGap, anchor.BinlogFile)
	}
	if files[0].Seq != start.Seq {
		return nil, fmt.Errorf("%w: the archive starts at %s but the backup anchors to %s",
			ErrBinlogGap, files[0], anchor.BinlogFile)
	}
	gaps, err := binlog.Gaps(files)
	if err != nil {
		return nil, err
	}
	if len(gaps) > 0 {
		return nil, fmt.Errorf("%w: missing %s; the recovery cannot cross it and is refused rather than "+
			"replayed around it", ErrBinlogGap, gaps[0])
	}
	if !target.Time.IsZero() {
		first, err := p.Archive.Index(ctx, files[0])
		if err != nil {
			return nil, err
		}
		if target.Time.Before(first.FirstEventAt) {
			return nil, fmt.Errorf("%w: the backup's log %s begins at %s, target is %s",
				ErrTargetBeforeAnchor, files[0], first.FirstEventAt.Format(time.RFC3339), target.Time.Format(time.RFC3339))
		}
	}
	return files, nil
}

// Replay fetches the planned files and pipes them through mariadb-binlog into
// the mariadb client, from the anchor's position to the target.
func (p PITR) Replay(
	ctx context.Context, ex executor.Executor, anchor manifest.MariaDBDetails,
	target Target, files []binlog.Name, from, into string,
) error {
	if target.isZero() {
		return errors.New("restore: a point-in-time recovery needs a target time or position")
	}
	binlogBin, err := p.Config.ResolveBin(mariadb.BinBinlog)
	if err != nil {
		return err
	}
	clientBin, err := p.Config.ResolveBin("mariadb")
	if err != nil {
		return err
	}

	work, err := os.MkdirTemp(p.Workdir, "koffr-pitr-")
	if err != nil {
		return fmt.Errorf("restore: create the replay directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	paths := make([]string, 0, len(files))
	for _, n := range files {
		path, err := p.fetchTo(ctx, work, n)
		if err != nil {
			return err
		}
		paths = append(paths, path)
	}

	cfg := p.Config
	cfg.Database = into
	sess, err := cfg.Open(ctx, ex)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()

	// mariadb-binlog reads files, so it runs where they are: on this host.
	// Its output is SQL, piped into the client, which is what the server's
	// own documentation does -- Koffr only picks the files and the bounds.
	args := []string{
		// The anchor's position applies to the first file only; the others
		// are replayed whole up to the target.
		"--start-position=" + strconv.FormatUint(anchor.BinlogPos, 10),
	}
	// The events name the database they were written to. --database on the
	// client is only a default, and a USE in the stream overrides it, so a
	// recovery under another name on the same server would have replayed into
	// the live database. The rewrite is done where the events are decoded,
	// which is the only place that can do it for row events too.
	if from != into {
		args = append(args, "--rewrite-db="+from+"->"+into)
	}
	switch {
	case !target.Time.IsZero():
		args = append(args, "--stop-datetime="+target.Time.UTC().Format("2006-01-02 15:04:05"))
	case target.PositionFile != "":
		args = append(args, "--stop-position="+strconv.FormatUint(target.Position, 10))
	}
	args = append(args, paths...)

	// TZ is pinned. mariadb-binlog reads --stop-datetime in the client's local
	// time zone while the events carry UTC seconds, so a Koffr host in Paris
	// asking for 15:41 UTC would have stopped two hours early -- which the
	// first run of this did, silently, leaving the target short by every row
	// written that afternoon. Pinning the zone and formatting in it makes the
	// cut land where it was asked, on any host.
	env := append(sess.Env(binlogBin), "TZ=UTC")
	producer, err := p.Config.ToolRunner.Start(ctx, executor.Command{
		Path: binlogBin, Args: args, Env: env,
	})
	if err != nil {
		return fmt.Errorf("restore: start %s: %w", mariadb.BinBinlog, err)
	}
	// stderr is where mariadb-binlog explains a refusal; it is kept.
	pTail := newTail()
	copied := make(chan struct{})
	go func() { defer close(copied); _, _ = io.Copy(pTail, producer.Stderr()) }()

	err = run(ctx, p.Config.ToolRunner, executor.Command{
		Path: clientBin,
		Args: []string{sess.DefaultsFile(), "--protocol=TCP", "--batch", "--database=" + into},
		Env:  sess.Env(clientBin),
	}, producer.Stdout(), "mariadb (replay)")

	// The producer is waited for after the consumer: it may have stopped on
	// its own at the target, which is success, or failed, which the consumer
	// saw as a short input.
	perr := producer.Wait()
	<-copied
	if perr != nil && err == nil {
		if msg := pTail.String(); msg != "" {
			return fmt.Errorf("restore: %s failed: %w: %s", mariadb.BinBinlog, perr, msg)
		}
		return fmt.Errorf("restore: %s failed: %w", mariadb.BinBinlog, perr)
	}
	if err != nil && pTail.String() != "" {
		return fmt.Errorf("%w (%s said: %s)", err, mariadb.BinBinlog, pTail.String())
	}
	return err
}

// fetchTo decrypts and decompresses one archived file into the work directory,
// checking its digest against the index.
func (p PITR) fetchTo(ctx context.Context, dir string, n binlog.Name) (string, error) {
	e, err := p.Archive.Index(ctx, n)
	if err != nil {
		return "", err
	}
	key, err := p.Archive.Source.BinlogKey(n.String())
	if err != nil {
		return "", err
	}
	rc, err := p.Archive.Storage.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("restore: fetch %s: %w", n, err)
	}
	defer func() { _ = rc.Close() }()

	digest := sha256.New()
	src := io.TeeReader(rc, digest)
	plain, err := p.Opener.Open(src)
	if err != nil {
		return "", checkDigest(src, digest, key, e.SHA256, fmt.Errorf("restore: decrypt %s: %w", n, err))
	}
	dec, err := zstd.NewReader(plain)
	if err != nil {
		return "", checkDigest(src, digest, key, e.SHA256, fmt.Errorf("restore: decompress %s: %w", n, err))
	}
	defer dec.Close()

	path := filepath.Join(dir, n.String())
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // under the replay directory this call created
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(f, dec)
	if cerr := f.Close(); copyErr == nil {
		copyErr = cerr
	}
	if copyErr != nil {
		copyErr = fmt.Errorf("restore: write %s: %w", n, copyErr)
	}
	return path, checkDigest(src, digest, key, e.SHA256, copyErr)
}

// ParseTarget reads --until: an RFC 3339 time, or <file>:<position>.
func ParseTarget(s string) (Target, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Target{}, errors.New("restore: --until is empty")
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return Target{Time: t}, nil
	}
	if file, pos, ok := strings.Cut(s, ":"); ok {
		n, err := strconv.ParseUint(pos, 10, 64)
		if err == nil {
			if _, perr := binlog.Parse(file); perr == nil {
				return Target{Position: n, PositionFile: file}, nil
			}
		}
	}
	return Target{}, fmt.Errorf("restore: %q is not a target; want an RFC 3339 time such as "+
		"2026-09-08T15:41:00Z, or <binlog file>:<position> such as mariadb-bin.000042:1234", s)
}

// Ensure the storage import is used where the archive's source is referenced.
var _ storage.Source

// ErrReplayIncompatible says the mariadb-binlog in use decodes events into SQL
// the target server cannot run -- a client newer than the server, typically.
var ErrReplayIncompatible = errors.New("restore: this mariadb-binlog produces SQL the target cannot run")

// Preflight runs the replay's preamble on the target before anything is
// restored. mariadb-binlog opens its output with SET @@session statements
// naming every variable it knows; a client newer than the target names one
// the target does not have, and the replay dies on it -- after the dump has
// been loaded, on the first run of this against MariaDB 10.6 with a 12.3
// client. Trying those statements first, on a throwaway session, is the one
// check that is exactly the failure and nothing else; a version comparison
// would refuse pairs that work.
func (p PITR) Preflight(ctx context.Context, ex executor.Executor, anchor manifest.MariaDBDetails, first binlog.Name, into string) error {
	work, err := os.MkdirTemp(p.Workdir, "koffr-pitr-preflight-")
	if err != nil {
		return fmt.Errorf("restore: create the preflight directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()
	path, err := p.fetchTo(ctx, work, first)
	if err != nil {
		return err
	}
	statements, err := p.preamble(ctx, path, anchor.BinlogPos)
	if err != nil {
		return err
	}
	return p.TryPreamble(ctx, ex, into, statements)
}

// preamble decodes the head of a binary log file and keeps the SET @@session
// statements, which is what the replay will run before the first event.
func (p PITR) preamble(ctx context.Context, path string, pos uint64) ([]string, error) {
	bin, err := p.Config.ResolveBin(mariadb.BinBinlog)
	if err != nil {
		return nil, err
	}
	// Only the head is wanted; the process is cancelled once it is read.
	hctx, cancel := context.WithCancel(ctx)
	defer cancel()
	proc, err := p.Config.ToolRunner.Start(hctx, executor.Command{
		Path: bin,
		Args: []string{"--start-position=" + strconv.FormatUint(pos, 10), path},
		Env:  []string{"TZ=UTC"},
	})
	if err != nil {
		return nil, fmt.Errorf("restore: start %s: %w", mariadb.BinBinlog, err)
	}
	go func() { _, _ = io.Copy(io.Discard, proc.Stderr()) }()
	head := make([]byte, 0, preambleBytes)
	buf := make([]byte, 4096)
	for len(head) < preambleBytes {
		n, rerr := proc.Stdout().Read(buf)
		head = append(head, buf[:n]...)
		if rerr != nil {
			break
		}
	}
	cancel()
	_ = proc.Wait()

	var statements []string
	for _, line := range strings.Split(string(head), "\n") {
		if !strings.HasPrefix(line, "SET @@session.") {
			continue
		}
		statements = append(statements, strings.TrimSuffix(strings.TrimSpace(line), "/*!*/;"))
	}
	if len(statements) == 0 {
		return nil, fmt.Errorf("restore: %s printed no session preamble for %s; refusing to guess what the replay would run",
			mariadb.BinBinlog, filepath.Base(path))
	}
	return statements, nil
}

// preambleBytes is how much of the decoded log the preflight reads. The
// preamble sits in the first kilobyte; the margin is for a long first event.
const preambleBytes = 64 << 10

// TryPreamble runs the given statements on the target through the same client
// and session the replay will use, and turns a refusal into
// ErrReplayIncompatible naming the variable and the way out.
func (p PITR) TryPreamble(ctx context.Context, ex executor.Executor, into string, statements []string) error {
	clientBin, err := p.Config.ResolveBin("mariadb")
	if err != nil {
		return err
	}
	cfg := p.Config
	cfg.Database = into
	sess, err := cfg.Open(ctx, ex)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	input := strings.NewReader(strings.Join(statements, ";\n") + ";\n")
	err = run(ctx, p.Config.ToolRunner, executor.Command{
		Path: clientBin,
		Args: []string{sess.DefaultsFile(), "--protocol=TCP", "--batch"},
		Env:  sess.Env(clientBin),
	}, input, "mariadb (preflight)")
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v. The %s on this host is newer than the target server; "+
		"point bin_dir at a client of the target's version and run the recovery again. Nothing was restored",
		ErrReplayIncompatible, err, mariadb.BinBinlog)
}
