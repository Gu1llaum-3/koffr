package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// CFG-01 — an unknown key is an error that names the key and its line, wherever
// it sits. A typo must never silently disable a backup (E-032, § 5.1 F1.1).
func TestCFG01UnknownKeyIsAnErrorNamingTheKeyAndItsLine(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		key  string
		line int
	}{
		{
			name: "at the root of a section",
			yaml: `
agent:
  id: "prod-fr-01"
  timezon: "Europe/Paris"
`,
			key:  "timezon",
			line: 3,
		},
		{
			name: "in a section of its own",
			yaml: `
agent:
  id: "prod-fr-01"
  timezone: "Europe/Paris"
encryptions:
  recipients_file: /etc/koffr/recipients.txt
`,
			key:  "encryptions",
			line: 4,
		},
		{
			name: "in databases[0]",
			yaml: `
agent:
  id: "prod-fr-01"
  timezone: "Europe/Paris"
databases:
  - id: shop
    engine: postgresql
    hostname: 10.0.3.12
`,
			key:  "hostname",
			line: 7,
		},
		{
			name: "in destinations[0]",
			yaml: `
agent:
  id: "prod-fr-01"
  timezone: "Europe/Paris"
destinations:
  - id: local-disk
    type: filesystem
    directory: /srv/backups
`,
			key:  "directory",
			line: 7,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(write(t, c.yaml))
			if err == nil {
				t.Fatal("an unknown key was accepted")
			}

			got := err.Error()
			if !strings.Contains(got, c.key) {
				t.Errorf("the error does not name the key %q:\n%s", c.key, got)
			}
			if want := "line " + strconv.Itoa(c.line); !strings.Contains(got, want) {
				t.Errorf("the error does not give %q:\n%s", want, got)
			}
		})
	}
}

// A well-formed document is accepted, so that the test above fails for the
// right reason.
func TestAMinimalDocumentIsAccepted(t *testing.T) {
	_, err := Load(write(t, `
agent:
  id: "prod-fr-01"
  timezone: "Europe/Paris"
`))
	if err != nil {
		t.Fatalf("a minimal document was refused: %v", err)
	}
}

// A missing file is named, not swallowed.
func TestAMissingFileIsAnError(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err == nil {
		t.Fatal("a missing configuration file was accepted")
	}
}

func write(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "koffr.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimPrefix(body, "\n")), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	return path
}
