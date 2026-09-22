package crypto

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"filippo.io/age"
)

// Recipients are the public keys an archive is encrypted for. koffr holds
// nothing else: the private key lives off this machine, which is what makes a
// stolen agent give away the databases as they are now and not the history of
// what they were (ADR-0007, § 5.6).
type Recipients struct {
	// Keys are the parsed public keys, in the order they were declared.
	Keys []*age.X25519Recipient

	// From is the file they were read from, for an error to quote.
	From string
}

// LoadRecipients reads a recipients file: one age public key per line, blank
// lines and # comments ignored.
func LoadRecipients(path string) (Recipients, error) {
	file, err := os.Open(path)
	if err != nil {
		return Recipients{}, fmt.Errorf("read the recipients: %w", err)
	}
	defer func() { _ = file.Close() }()

	loaded := Recipients{From: path}

	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}

		key, err := age.ParseX25519Recipient(text)
		if err != nil {
			// The file and the line, so that the fix is one edit away (CRY-01).
			return Recipients{}, fmt.Errorf("%s line %d: %q is not an age public key: %w",
				path, line, text, err)
		}

		loaded.Keys = append(loaded.Keys, key)
	}
	if err := scanner.Err(); err != nil {
		return Recipients{}, fmt.Errorf("read %s: %w", path, err)
	}

	if len(loaded.Keys) == 0 {
		return Recipients{}, fmt.Errorf("%s declares no recipient: koffr does not encrypt for nobody", path)
	}

	return loaded, nil
}

// Warning says what is worth saying about this set of recipients, or nothing.
//
// One key is allowed and warned about: losing it would condemn every archive it
// ever wrote. E-132 asks for a warning at start-up rather than a refusal, so
// that a configuration which is otherwise fine still works (CRY-02).
func (r Recipients) Warning() string {
	if len(r.Keys) >= 2 {
		return ""
	}

	return fmt.Sprintf(
		"%s declares one recipient: add an escrow key kept elsewhere, "+
			"or losing this one makes every archive it encrypted unreadable forever",
		r.From,
	)
}

// Effective says which recipients apply to a database: **its own when it
// declares any, the fleet's otherwise**. Never both.
//
// `Q-04`, tranchée par ADR-0016. A merge would be convenient — add one key to a
// sensitive database without repeating the escrow — and would mean that nobody
// could say, reading one database, for whom its archives are encrypted. The
// escrow warning of E-132 follows the list that applies, so a database with one
// key of its own is warned about even when the fleet declares two.
func Effective(fleet, database Recipients) Recipients {
	if len(database.Keys) > 0 {
		return database
	}

	return fleet
}
