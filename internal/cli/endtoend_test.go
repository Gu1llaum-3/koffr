package cli

import (
	"bytes"
	"encoding/json"
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

	"github.com/Gu1llaum-3/koffr/internal/domain/backup"
	"github.com/Gu1llaum-3/koffr/internal/domain/catalog"
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
	// E-024 — the seven steps are seven since the wave 4 of the lot 3. Nothing
	// is declared absent any more, and a command that still announced a
	// "pending" step would be describing a release that no longer exists.
	if strings.Contains(out, "pending ") {
		t.Errorf("the command still declares a step as pending:\n%s", out)
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
		// The manifest deposited beside the archive is not one (E-057).
		if !entry.IsDir() && !strings.HasSuffix(path, ".json") {
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

// A-16, BKP-17 — on a real machine: a buffer left by a job that was killed is
// gone after the next job, and the staging directory is empty once a backup
// succeeds. The recette found 13 MB sitting there for ever.
func TestAKilledJobsBufferIsGoneAfterTheNextBackup(t *testing.T) {
	server := startPostgresServer(t, 2000)
	site := newFleetSite(t, server)

	staging := filepath.Join(site.stateDir, "tmp")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatalf("make the staging directory: %v", err)
	}

	// As a job killed mid-stream would have left it: a pid the kernel has not
	// handed out.
	orphan := filepath.Join(staging, backup.StagingFileName(2147483646, "01JQ8F3K2M7X9P4W"))
	if err := os.WriteFile(orphan, make([]byte, 13<<20), 0o600); err != nil {
		t.Fatalf("plant the orphan buffer: %v", err)
	}

	run(t, site.args("backup", fleetDatabase)...)

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("the buffer of a killed job survived the next backup: %s", orphan)
	}

	left, err := os.ReadDir(staging)
	if err != nil {
		t.Fatalf("read the staging directory: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("the staging directory holds %d entries after a successful backup", len(left))
	}
}

// A-18, BKP-20 — on a real machine: the file of E-026 carries the seven steps
// of a job, what it wrote and how big it was. The acceptance run found 21 log
// lines for 21 commands, all of them "command started".
func TestTheLogFileCarriesWhatABackupDid(t *testing.T) {
	server := startPostgresServer(t, 2000)
	site := newFleetSite(t, server)

	logDir := t.TempDir()
	run(t, append(site.args("backup", fleetDatabase), "--log-dir", logDir)...)

	written, err := os.ReadFile(filepath.Join(logDir, "koffr.log"))
	if err != nil {
		t.Fatalf("read the log file: %v", err)
	}

	journalled := string(written)

	for _, step := range []string{
		"resolution", "dump", "compression", "encryption", "write", "verification", "manifest",
	} {
		if !strings.Contains(journalled, `"step":"`+step+`"`) {
			t.Errorf("the journal lost the step %q", step)
		}
	}

	for _, fact := range []string{"sha256_stored", "stored_bytes", "path", "pipeline", "tool"} {
		if !strings.Contains(journalled, `"`+fact+`"`) {
			t.Errorf("the journal does not carry %q:\n%s", fact, head(journalled))
		}
	}

	// E-115 — and not a trace of what opens the database.
	if strings.Contains(journalled, fleetPassword) {
		t.Error("the database password is in the log file")
	}
}

func head(of string) string {
	if len(of) > 800 {
		return of[:800]
	}

	return of
}

// Q-04, CRY-05 — a database that declares its own recipients is encrypted for
// **them alone**. Proven where it counts: the fleet key, which would open every
// other archive, opens nothing here.
func TestADatabaseWithItsOwnRecipientsIsEncryptedForThemAlone(t *testing.T) {
	server := startPostgresServer(t, 500)
	site := newFleetSite(t, server)

	// One key of its own, declared on the database.
	own := newIdentity(t)
	ownFile := filepath.Join(filepath.Dir(site.config), "shop-recipients.txt")
	writeFleetFile(t, ownFile, own.Recipient().String()+"\n")

	configuration, err := os.ReadFile(site.config)
	if err != nil {
		t.Fatalf("read the configuration: %v", err)
	}

	writeFleetFile(t, site.config, strings.Replace(string(configuration),
		"    staging: stage\n", "    staging: stage\n    recipients_file: "+ownFile+"\n", 1))

	run(t, site.args("backup", fleetDatabase)...)

	archive := site.onlyArchive(t)

	// The key the database declares opens it.
	if _, err := age.Decrypt(bytes.NewReader(archive.contents), own); err != nil {
		t.Fatalf("the key the database declares does not open its archive: %v", err)
	}

	// And the fleet key — which opens every other archive — does not.
	fleet, err := os.ReadFile(site.identity)
	if err != nil {
		t.Fatalf("read the fleet identity: %v", err)
	}

	fleetKey, err := age.ParseX25519Identity(strings.TrimSpace(string(fleet)))
	if err != nil {
		t.Fatalf("parse the fleet identity: %v", err)
	}

	if _, err := age.Decrypt(bytes.NewReader(archive.contents), fleetKey); err == nil {
		t.Error("the fleet key opened an archive encrypted for the database's own: the lists were merged")
	}
}

// N-9 — on a real machine: a successful backup lands in the catalogue, with its
// database, its sizes, its checksum and where the copy is. Not verified yet —
// that is the wave 4, and P4 says so out loud in the meantime.
func TestASuccessfulBackupIsIndexed(t *testing.T) {
	server := startPostgresServer(t, 500)
	site := newFleetSite(t, server)
	book := &fakeCatalog{}

	var out, errs bytes.Buffer

	root := NewRoot(WithCatalog(func(string) (catalog.Catalog, func() error, error) {
		return book, func() error { return nil }, nil
	}))
	root.SetOut(&out)
	root.SetErr(&errs)
	root.SetArgs(site.args("backup", fleetDatabase))

	if err := root.Execute(); err != nil {
		t.Fatalf("backup: %v\n%s", err, errs.String())
	}

	if len(book.databases) != 1 || book.databases[0].ID != fleetDatabase {
		t.Fatalf("the database was not indexed: %+v", book.databases)
	}
	if len(book.backups) != 1 {
		t.Fatalf("got %d indexed backups, want 1", len(book.backups))
	}

	indexed := book.backups[0]
	if indexed.StoredBytes == 0 || indexed.SHA256Stored == "" {
		t.Errorf("the index carries neither size nor checksum: %+v", indexed)
	}
	if len(indexed.Locations) != 1 || indexed.Locations[0].Destination != "local" {
		t.Errorf("the index does not say where the copy is: %+v", indexed.Locations)
	}
	// P4, since the wave 4 — a backup that passed both checks is indexed as
	// verified, and a backup that failed them would be indexed as `failed`.
	// What is never indexed is a claim nobody checked.
	if indexed.Verified != catalog.Structure {
		t.Errorf("a verified backup is indexed as %q, want %q", indexed.Verified, catalog.Structure)
	}
	if indexed.VerifiedAt.IsZero() {
		t.Error("the index does not say when it was verified")
	}
}

// E-057, E-058, E-059 — on a real machine: the manifest is deposited beside the
// archive, it reads as JSON without any key, it says how to open the archive,
// and it carries no credential.
func TestTheManifestIsDepositedAndReadsWithoutAKey(t *testing.T) {
	server := startPostgresServer(t, 500)
	site := newFleetSite(t, server)

	run(t, site.args("backup", fleetDatabase)...)

	archive := site.onlyArchive(t)

	written, err := os.ReadFile(filepath.Join(site.destination, archive.path+".json"))
	if err != nil {
		t.Fatalf("no manifest beside the archive: %v", err)
	}

	var manifest catalog.Manifest
	if err := json.Unmarshal(written, &manifest); err != nil {
		t.Fatalf("the manifest is not JSON: %v\n%s", err, written)
	}

	if manifest.BackupID == "" || manifest.DatabaseID != fleetDatabase {
		t.Errorf("the manifest does not say what it describes: %+v", manifest)
	}
	if manifest.SizeStored != int64(len(archive.contents)) {
		t.Errorf("size_stored = %d, the archive is %d bytes", manifest.SizeStored, len(archive.contents))
	}
	if manifest.SHA256Stored == "" || manifest.SizeRaw == 0 {
		t.Errorf("the manifest carries no checksum or no raw size: %+v", manifest)
	}
	if manifest.DurationMS <= 0 {
		t.Errorf("duration_ms = %d: the job took no time at all?", manifest.DurationMS)
	}

	// What says how to open it, six months later, without koffr.
	if manifest.Format == "" || len(manifest.Pipeline) == 0 || manifest.Tool.Name == "" {
		t.Errorf("the manifest does not say how the archive was written: %+v", manifest)
	}
	if len(manifest.Tool.Argv) == 0 {
		t.Errorf("the manifest does not carry the argv of the tool: %+v", manifest.Tool)
	}
	if len(manifest.Recipients) != 2 {
		t.Errorf("recipients = %v, want the two keys of the fleet", manifest.Recipients)
	}

	// P4 — the manifest carries the verification, because it is written after
	// it (E-024). Both halves passed, so both say so.
	if !manifest.Verified.Checksum || !manifest.Verified.Structure {
		t.Errorf("a verified archive does not say so in its manifest: %+v", manifest.Verified)
	}
	if manifest.Verified.At == "" {
		t.Error("the manifest does not say when the archive was verified")
	}

	// E-059, E-114 — and not a credential in sight, argv included.
	for what, value := range map[string]string{
		"the password":    fleetPassword,
		"the user":        fleetUser,
		"a private key":   "AGE-SECRET-KEY",
		"the server host": server.host,
	} {
		if strings.Contains(string(written), value) {
			t.Errorf("%s is in the manifest:\n%s", what, written)
		}
	}
}
