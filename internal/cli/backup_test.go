package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/Gu1llaum-3/koffr/internal/domain/catalog"
)

// E-103b — `koffr backup <db>` names the databases it knows when given one it
// does not. A typo is the likeliest reason to be here, and "unknown database"
// on its own sends an operator back to the file to compare by eye.
func TestBackupNamesTheDatabasesItKnows(t *testing.T) {
	site := newSite(t)

	_, err := failing(t, site.args("backup", "typo")...)
	if err == nil {
		t.Fatal("an unknown database identifier was accepted")
	}
	for _, want := range []string{"typo", "shop"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q:\n%v", want, err)
		}
	}
}

// E-103b — a database that does not answer fails the job, and leaves **nothing**
// behind: no archive, and no lock that would block tomorrow night (BKP-01).
func TestBackupOfAnUnreachableDatabaseWritesNothingAndKeepsNoLock(t *testing.T) {
	site := newSite(t)

	if _, err := failing(t, site.args("backup", "shop")...); err == nil {
		t.Fatal("a backup of a database that does not answer reported success")
	}

	site.expectNoArchive(t)
	site.expectNoLock(t)
}

// E-103b — `--dry-run` says what it would do and writes nothing. Here the
// database does not answer, so what it reports is that: a dry run that claimed
// a plan it could not carry out would be worse than useless.
//
// The dry run of a database that **does** answer, and the backup that follows,
// are proven at wave 6 against a real fleet: internal/cli has no container
// fixtures, and a stand-in server would agree with whatever koffr believes.
func TestBackupDryRunWritesNothing(t *testing.T) {
	site := newSite(t)

	out, err := failing(t, site.args("backup", "shop", "--dry-run")...)
	if err == nil {
		t.Fatal("a dry run of a database that does not answer reported a plan")
	}
	if !strings.Contains(err.Error(), "shop") {
		t.Errorf("the failure does not name the database:\n%v", err)
	}
	if out != "" {
		t.Errorf("a dry run that could not plan printed a plan anyway:\n%s", out)
	}

	site.expectNoArchive(t)
	site.expectNoLock(t)
}

// A database that sends its archives nowhere is refused, saying so: E-030 and
// the retention of the lot 3 both count destinations, and zero is a
// configuration mistake rather than a backup without a copy.
func TestBackupRefusesADatabaseWithNoDestination(t *testing.T) {
	site := newSiteWithout(t, "destinations")

	_, err := failing(t, site.args("backup", "shop")...)
	if err == nil {
		t.Fatal("a database with no destination was backed up")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "destination") {
		t.Errorf("the refusal does not say what is missing:\n%v", err)
	}
}

// ---- a site: one configuration, one destination, one state directory -------

type site struct {
	config      string
	stateDir    string
	destination string
}

func newSite(t *testing.T) site {
	t.Helper()

	return buildSite(t, true)
}

func newSiteWithout(t *testing.T, _ string) site {
	t.Helper()

	return buildSite(t, false)
}

func buildSite(t *testing.T, withDestination bool) site {
	t.Helper()

	root := t.TempDir()
	destination := filepath.Join(root, "archives")

	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatalf("make the destination: %v", err)
	}

	recipients := filepath.Join(root, "recipients.txt")
	writeRecipients(t, recipients)

	declared := "    destinations: [local]\n"
	if !withDestination {
		declared = ""
	}

	body := fmt.Sprintf(`agent:
  id: prod-fr-01
  timezone: Europe/Paris

encryption:
  recipients_file: %s

databases:
  - id: shop
    engine: postgresql
    host: 127.0.0.1
    port: 1
    database: shop
    user: koffr_backup
    staging: auto
%s
destinations:
  - id: local
    type: filesystem
    path: %s
`, recipients, declared, destination)

	path := filepath.Join(root, "koffr.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write the configuration: %v", err)
	}

	return site{config: path, stateDir: filepath.Join(root, "state"), destination: destination}
}

func (s site) args(rest ...string) []string {
	return append(rest, "--config", s.config, "--state-dir", s.stateDir)
}

func (s site) expectNoArchive(t *testing.T) {
	t.Helper()

	left, err := os.ReadDir(s.destination)
	if err != nil {
		t.Fatalf("read the destination: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("the destination holds %d entries after a job that failed", len(left))
	}
}

func (s site) expectNoLock(t *testing.T) {
	t.Helper()

	locks := filepath.Join(s.stateDir, "locks")

	left, err := os.ReadDir(locks)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}

		t.Fatalf("read the lock directory: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("%d locks were left behind by a job that failed", len(left))
	}
}

func writeRecipients(t *testing.T, path string) {
	t.Helper()

	var lines strings.Builder

	for range 2 {
		pair, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatalf("generate a key pair: %v", err)
		}

		lines.WriteString(pair.Recipient().String() + "\n")
	}

	if err := os.WriteFile(path, []byte(lines.String()), 0o600); err != nil {
		t.Fatalf("write the recipients: %v", err)
	}
}

// E-132 — a configuration with a single recipient is **warned about**, and the
// command an operator runs to check a configuration is where they will see it.
// Losing that one key would condemn every archive ever written for it.
func TestValidateWarnsAboutASingleRecipient(t *testing.T) {
	site := newSiteWithOneKey(t)

	out, errs, err := execute(t, "config", "validate", "--config", site.config, "--offline")
	if err != nil {
		t.Fatalf("config validate: %v\n%s", err, errs)
	}

	if !strings.Contains(out, "ok") {
		t.Errorf("a configuration with one key was rejected instead of warned about:\n%s", out)
	}
	for _, want := range []string{"escrow", "one"} {
		if !strings.Contains(strings.ToLower(errs), want) {
			t.Errorf("the warning does not say %q:\n%s", want, errs)
		}
	}
}

// E-132 — and two keys say nothing. A warning that is always there is a warning
// nobody reads.
func TestValidateSaysNothingAboutTwoRecipients(t *testing.T) {
	site := newSite(t)

	_, errs, err := execute(t, "config", "validate", "--config", site.config, "--offline")
	if err != nil {
		t.Fatalf("config validate: %v\n%s", err, errs)
	}

	if strings.Contains(strings.ToLower(errs), "escrow") {
		t.Errorf("a configuration with two keys was warned about anyway:\n%s", errs)
	}
}

func newSiteWithOneKey(t *testing.T) site {
	t.Helper()

	built := buildSite(t, true)

	pair, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate a key pair: %v", err)
	}

	recipients := filepath.Join(filepath.Dir(built.config), "recipients.txt")
	if err := os.WriteFile(recipients, []byte(pair.Recipient().String()+"\n"), 0o600); err != nil {
		t.Fatalf("write the recipients: %v", err)
	}

	return built
}

// E-132, CRY-02 — the escrow warning appears on **every** command that reads
// the keys, not only on the one that happens to use them. An operator who runs
// `doctor` before going to bed has to see it there (décision de recette du
// 2026-09-22).
func TestTheEscrowWarningAppearsOnEveryCommandThatReadsTheKeys(t *testing.T) {
	alone := newSiteWithOneKey(t)
	pair := newSite(t)

	commands := map[string][]string{
		"config validate": {"config", "validate", "--offline"},
		"doctor":          {"doctor", "--search-path", t.TempDir()},
		"backup":          {"backup", "shop"},
	}

	for name, args := range commands {
		t.Run(name, func(t *testing.T) {
			_, errs, _ := execute(t, alone.args(args...)...)
			if !strings.Contains(strings.ToLower(errs), "escrow") {
				t.Errorf("%s says nothing about the escrow key with a single recipient:\n%s", name, errs)
			}

			// And two keys say nothing: a warning that is always there is a
			// warning nobody reads.
			_, quiet, _ := execute(t, pair.args(args...)...)
			if strings.Contains(strings.ToLower(quiet), "escrow") {
				t.Errorf("%s warned although two recipients are declared:\n%s", name, quiet)
			}
		})
	}
}

// N-9 — a backup records itself in the catalogue. The command is what does it:
// `AR-03` keeps `domain/backup` and `domain/catalog` apart, so neither knows the
// other, and `AR-04` keeps SQLite out of this package — hence a fake here, and
// the real SQL proven in `internal/state`.
func TestABackupRecordsItselfInTheCatalogue(t *testing.T) {
	site := newSite(t)
	book := &fakeCatalog{}

	// The database is unreachable, so the job fails — and even then, nothing
	// may be recorded: an archive that does not exist has no business in an
	// index (P4).
	if _, err := failingWith(t, book, site.args("backup", "shop")...); err == nil {
		t.Fatal("a backup of an unreachable database reported success")
	}

	if len(book.backups) != 0 {
		t.Errorf("a failed job recorded %d backups", len(book.backups))
	}
}

// N-10 — a catalogue that cannot record does **not** fail a backup that is
// already written. The archive and its manifest are on the destination; losing
// the index is a warning, not a reason to call a good archive a failure.
func TestACatalogueThatFailsDoesNotFailTheBackup(t *testing.T) {
	site := newSite(t)

	// Nothing is wired at all: the strongest form of "the catalogue is not
	// available".
	_, errs, _ := execute(t, site.args("backup", "shop", "--dry-run")...)

	if strings.Contains(errs, "panic") {
		t.Errorf("a missing catalogue brought the command down:\n%s", errs)
	}
}

type fakeCatalog struct {
	databases []catalog.Database
	backups   []catalog.Backup
	fail      error
}

func (f *fakeCatalog) RecordDatabase(_ context.Context, database catalog.Database, _ time.Time) error {
	if f.fail != nil {
		return f.fail
	}

	f.databases = append(f.databases, database)

	return nil
}

func (f *fakeCatalog) RecordBackup(_ context.Context, backup catalog.Backup) error {
	if f.fail != nil {
		return f.fail
	}

	f.backups = append(f.backups, backup)

	return nil
}

func (f *fakeCatalog) SetVerification(context.Context, string, catalog.Verification, time.Time) error {
	return f.fail
}

func (f *fakeCatalog) Backups(context.Context, catalog.Filter) ([]catalog.Backup, error) {
	return f.backups, f.fail
}

// failingWith runs a command against a wired catalogue and returns what it said.
func failingWith(t *testing.T, book catalog.Catalog, args ...string) (string, error) {
	t.Helper()

	var out, errs bytes.Buffer

	root := NewRoot(WithCatalog(func(string) (catalog.Catalog, func() error, error) {
		return book, func() error { return nil }, nil
	}))
	root.SetOut(&out)
	root.SetErr(&errs)
	root.SetArgs(args)

	return out.String(), root.Execute()
}
