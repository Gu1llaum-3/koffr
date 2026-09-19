package crypto_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/domain/crypto"
)

// Two real age public keys, generated for these tests. They are public by
// definition: nothing here is a secret.
const (
	operational = "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"
	escrow      = "age1lggyhqrw2nlhcxprm67z43rta597azn8gknawjehu9d9dl0jq3yqqvfafg"
)

// CRY-01 — a valid key is accepted, a malformed one is an error that names the
// file and the line. An operator fixes a typo in a file they can see.
func TestCRY01AKeyIsAcceptedOrRefusedWithItsLine(t *testing.T) {
	path := writeRecipients(t, strings.Join([]string{
		"# the operational key",
		operational,
		"",
		"not-an-age-key",
	}, "\n"))

	_, err := crypto.LoadRecipients(path)
	if err == nil {
		t.Fatal("a malformed key was accepted")
	}

	for _, want := range []string{path, "line 4", "not-an-age-key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not give %q:\n%v", want, err)
		}
	}
}

// CRY-01 — comments and blank lines are ignored, and what remains is read.
func TestCRY01CommentsAndBlankLinesAreIgnored(t *testing.T) {
	loaded, err := crypto.LoadRecipients(writeRecipients(t, strings.Join([]string{
		"# operational",
		operational,
		"",
		"   ",
		"# escrow, kept off this machine",
		escrow,
	}, "\n")))
	if err != nil {
		t.Fatalf("LoadRecipients: %v", err)
	}

	if len(loaded.Keys) != 2 {
		t.Fatalf("got %d recipients, want 2: %+v", len(loaded.Keys), loaded.Keys)
	}
}

// CRY-01 — encrypting for nobody is not a thing koffr does.
func TestCRY01AnEmptyRecipientsFileIsAnError(t *testing.T) {
	for _, body := range []string{"", "# only a comment\n", "\n\n"} {
		if _, err := crypto.LoadRecipients(writeRecipients(t, body)); err == nil {
			t.Errorf("a recipients file with no key was accepted: %q", body)
		}
	}

	if _, err := crypto.LoadRecipients(filepath.Join(t.TempDir(), "absent.txt")); err == nil {
		t.Error("a missing recipients file was accepted")
	}
}

// CRY-02 — one key is allowed and warned about. Losing it would condemn every
// archive it ever wrote, and E-132 wants that said out loud rather than
// discovered on the day it matters.
func TestCRY02ASingleRecipientWarnsAboutTheEscrowKey(t *testing.T) {
	alone, err := crypto.LoadRecipients(writeRecipients(t, operational))
	if err != nil {
		t.Fatalf("a single recipient was refused: %v", err)
	}

	warning := alone.Warning()
	if warning == "" {
		t.Fatal("a single recipient passed without a word")
	}
	for _, want := range []string{"escrow", "one"} {
		if !strings.Contains(strings.ToLower(warning), want) {
			t.Errorf("the warning does not mention %q:\n%s", want, warning)
		}
	}

	// And two keys say nothing.
	pair, err := crypto.LoadRecipients(writeRecipients(t, operational+"\n"+escrow))
	if err != nil {
		t.Fatalf("LoadRecipients: %v", err)
	}
	if got := pair.Warning(); got != "" {
		t.Errorf("two recipients produced a warning: %s", got)
	}
}

func writeRecipients(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "recipients.txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	return path
}
