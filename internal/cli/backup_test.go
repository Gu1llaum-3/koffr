package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
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
