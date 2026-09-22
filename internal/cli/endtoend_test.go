package cli

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
)

// fixtureDir is where the end-to-end tests lay out what external tools need to
// open an archive koffr wrote. A flag rather than an environment variable:
// AR-05 reserves reading the environment to internal/config (`N-8`).
var fixtureDir = flag.String("fixture-dir", "",
	"write an archive and a private key here, for scripts/check-backup-e2e.sh")

func TestMain(m *testing.M) {
	whyNoDocker = dockerFailure()

	os.Exit(m.Run())
}

// Scenario 6 of § 8, and the exit criterion of this lot: `koffr backup` writes
// an encrypted archive of a real PostgreSQL to a real destination.
//
// What this test can prove on its own is that the archive is an age file and
// **not** the dump in clear. That the standard `age` tool opens it, and that
// `pg_restore` then reads what comes out, is proven by
// scripts/check-backup-e2e.sh: only a binary that is not ours can show that
// koffr invented no format (`N-15`, same bargain as `N-8`).
func TestBackupOfARealPostgreSQLWritesAnEncryptedArchive(t *testing.T) {
	server := startPostgresServer(t, 2000)
	site := newFleetSite(t, server)

	out := run(t, site.args("backup", fleetDatabase)...)

	archive := site.onlyArchive(t)
	if !bytes.HasPrefix(archive.contents, []byte("age-encryption.org/")) {
		t.Errorf("the archive is not an age file: %q", firstBytes(archive.contents))
	}
	if bytes.Contains(archive.contents, []byte("PGDMP")) {
		t.Error("the dump is readable inside the archive: it was not encrypted")
	}
	if bytes.Contains(archive.contents, []byte(seededTable)) {
		t.Error("the name of a table is readable inside the archive")
	}

	// The path is the one BKP-10 promises, reconstructible without the catalogue.
	if want := fmt.Sprintf("%s/%04d/%02d/", fleetDatabase, time.Now().Year(), int(time.Now().Month())); !strings.Contains(archive.path, want) {
		t.Errorf("the archive is at %q, want it under %q", archive.path, want)
	}

	for _, said := range []string{"archive", "staging", "sha256", "sent to"} {
		if !strings.Contains(out, said) {
			t.Errorf("the command does not report %q:\n%s", said, out)
		}
	}
	// E-024: what is not implemented is declared, not skipped in silence.
	for _, pending := range []string{"verification", "manifest"} {
		if !strings.Contains(out, pending) {
			t.Errorf("the command does not declare %q as pending:\n%s", pending, out)
		}
	}

	site.layOutFixture(t, "postgresql", archive)
}

// ADR-0015 end to end — a MariaDB is backed up through the client of its own
// container. On a machine carrying both families this is the only way to reach
// the right client, so it is the way this test runs it.
func TestBackupOfARealMariaDBWritesAnEncryptedArchive(t *testing.T) {
	server := startMariaDBServer(t)
	site := newFleetSite(t, server)

	run(t, site.args("backup", fleetDatabase)...)

	archive := site.onlyArchive(t)
	if !bytes.HasPrefix(archive.contents, []byte("age-encryption.org/")) {
		t.Errorf("the archive is not an age file: %q", firstBytes(archive.contents))
	}
	if bytes.Contains(archive.contents, []byte("CREATE TABLE")) {
		t.Error("the dump is readable inside the archive: it was not encrypted")
	}

	site.layOutFixture(t, "mariadb", archive)
}

// Scenario 11, first half — on a database large enough for it to mean
// something, staging never holds more than the **compressed** archive, and the
// connection to the database is gone before anything is sent.
//
// The second claim is checked as a violation detector: at every sample where
// something has appeared at the destination, no dump connection may be open.
// The deterministic half of it is BKP-14, which holds Close before the first
// write by construction.
func TestInStageNothingBiggerThanTheArchiveAppearsAndTheConnectionCloses(t *testing.T) {
	server := startPostgresServer(t, 60000)
	site := newFleetSite(t, server)

	watcher := site.watch(t, server)

	out := run(t, site.args("backup", fleetDatabase)...)

	watcher.stop()

	stored, dumped := sizesReported(t, out)

	if dumped < 4<<20 {
		t.Fatalf("the dump is only %d bytes: too small for this test to mean anything", dumped)
	}
	if stored >= dumped/2 {
		t.Errorf("the archive is %d bytes for a dump of %d: nothing was really compressed", stored, dumped)
	}

	// § 4.5 — what is buffered is the compressed and encrypted stream, never
	// the raw dump. A little slack for the block the encoder is holding.
	if peak := watcher.peakStaged.Load(); peak > stored*12/10 {
		t.Errorf("staging held %d bytes for an archive of %d: the raw dump was buffered", peak, stored)
	}

	if !watcher.sawDump.Load() {
		t.Error("no dump connection was ever seen: the watcher proved nothing")
	}
	if watcher.overlapped.Load() {
		t.Error("something was being sent while a dump connection was still open (E-055)")
	}
}

// ---- a site: one configuration, one real server, one destination -----------

type fleetSite struct {
	config      string
	stateDir    string
	destination string
	identity    string
}

func newFleetSite(t *testing.T, server fleetServer) fleetSite {
	t.Helper()

	root := t.TempDir()
	destination := filepath.Join(root, "archives")

	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatalf("make the destination: %v", err)
	}

	// Two recipients, as E-132 asks: the operational key and the escrow one.
	// The first is kept so that a script can open the archive afterwards.
	mine, escrow := newIdentity(t), newIdentity(t)

	recipients := filepath.Join(root, "recipients.txt")
	writeFleetFile(t, recipients, mine.Recipient().String()+"\n"+escrow.Recipient().String()+"\n")

	identity := filepath.Join(root, "identity.txt")
	writeFleetFile(t, identity, mine.String()+"\n")

	tools := ""
	if server.engine != "postgresql" {
		// ADR-0015 — this family resolves and dumps inside its own container.
		tools = fmt.Sprintf("    tools: { strategy: exec, container: %s }\n", server.container)
	}

	writeFleetFile(t, filepath.Join(root, "koffr.yaml"), fmt.Sprintf(`agent:
  id: prod-fr-01
  timezone: Europe/Paris

encryption:
  recipients_file: %s

databases:
  - id: %s
    engine: %s
    host: %s
    port: %d
    database: %s
    user: %s
    password: %s
    staging: stage
%s    destinations: [local]

destinations:
  - id: local
    type: filesystem
    path: %s
`, recipients, fleetDatabase, server.engine, server.host, server.port,
		fleetDatabase, fleetUser, fleetPassword, tools, destination))

	return fleetSite{
		config:      filepath.Join(root, "koffr.yaml"),
		stateDir:    filepath.Join(root, "state"),
		destination: destination,
		identity:    identity,
	}
}

func (s fleetSite) args(rest ...string) []string {
	return append(rest, "--config", s.config, "--state-dir", s.stateDir)
}

type writtenArchive struct {
	path     string
	contents []byte
}

// onlyArchive finds the single archive under the destination, wherever the path
// of BKP-10 put it.
func (s fleetSite) onlyArchive(t *testing.T) writtenArchive {
	t.Helper()

	var found []string

	err := filepath.WalkDir(s.destination, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			found = append(found, path)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walk the destination: %v", err)
	}

	if len(found) != 1 {
		t.Fatalf("the destination holds %d files, want one archive: %v", len(found), found)
	}

	contents, err := os.ReadFile(found[0])
	if err != nil {
		t.Fatalf("read the archive: %v", err)
	}

	relative, err := filepath.Rel(s.destination, found[0])
	if err != nil {
		t.Fatalf("relative path: %v", err)
	}

	return writtenArchive{path: filepath.ToSlash(relative), contents: contents}
}

// layOutFixture hands the archive and a private key to the shell script that
// runs `age` and `pg_restore` over them. Without -fixture-dir it does nothing.
func (s fleetSite) layOutFixture(t *testing.T, engine string, archive writtenArchive) {
	t.Helper()

	if *fixtureDir == "" {
		return
	}

	directory := filepath.Join(*fixtureDir, engine)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("make the fixture directory: %v", err)
	}

	identity, err := os.ReadFile(s.identity)
	if err != nil {
		t.Fatalf("read the identity: %v", err)
	}

	writeFleetFile(t, filepath.Join(directory, "identity.txt"), string(identity))
	writeFleetFile(t, filepath.Join(directory, "expected-table.txt"), seededTable+"\n")

	if err := os.WriteFile(filepath.Join(directory, "archive.age"), archive.contents, 0o600); err != nil {
		t.Fatalf("write the archive fixture: %v", err)
	}
}

// fleetWatcher samples the machine while a backup runs.
type fleetWatcher struct {
	peakStaged atomic.Int64
	sawDump    atomic.Bool
	overlapped atomic.Bool
	done       chan struct{}
	stopped    chan struct{}
}

func (w *fleetWatcher) stop() {
	close(w.done)
	<-w.stopped
}

// watch samples the staging directory and the server together, so that the
// question "was anything being sent while a dump was still connected" is
// answered from **one** sample rather than from two clocks.
func (s fleetSite) watch(t *testing.T, server fleetServer) *fleetWatcher {
	t.Helper()

	watcher := &fleetWatcher{done: make(chan struct{}), stopped: make(chan struct{})}
	staging := filepath.Join(s.stateDir, "tmp")

	go func() {
		defer close(watcher.stopped)

		for {
			select {
			case <-watcher.done:
				return

			case <-time.After(20 * time.Millisecond):
			}

			if bytes := directoryBytes(staging); bytes > watcher.peakStaged.Load() {
				watcher.peakStaged.Store(bytes)
			}

			live := dumpConnections(t, server)
			if live > 0 {
				watcher.sawDump.Store(true)
			}

			if live > 0 && directoryBytes(s.destination) > 0 {
				watcher.overlapped.Store(true)
			}
		}
	}()

	return watcher
}

// directoryBytes is how much a directory tree holds right now, partial files
// included: a `.partial` at the destination is the earliest sign of sending.
func directoryBytes(root string) int64 {
	var total int64

	_ = filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // a tree being written under us is expected
		}

		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}

		return nil
	})

	return total
}

var reportedSizes = regexp.MustCompile(`size\s+(\d+) bytes stored, (\d+) dumped`)

// sizesReported reads what the command told the operator. Parsing its output on
// purpose: those two figures are what somebody reads to decide whether a
// backup went as expected, so they are part of what is under test.
func sizesReported(t *testing.T, out string) (stored, dumped int64) {
	t.Helper()

	matched := reportedSizes.FindStringSubmatch(out)
	if matched == nil {
		t.Fatalf("the command does not report the two sizes:\n%s", out)
	}

	stored, err := strconv.ParseInt(matched[1], 10, 64)
	if err != nil {
		t.Fatalf("stored size: %v", err)
	}

	dumped, err = strconv.ParseInt(matched[2], 10, 64)
	if err != nil {
		t.Fatalf("dumped size: %v", err)
	}

	return stored, dumped
}

func newIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()

	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate a key pair: %v", err)
	}

	return key
}

func firstBytes(of []byte) string {
	return string(of[:min(len(of), 48)])
}
