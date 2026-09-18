package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// CFG-01 — an unknown key is an error that names the section it sits in, the
// key and its line. A typo must never silently disable a backup (E-032,
// § 5.1 F1.1), and the person reading the message is an operator: it names
// `agent`, not the Go type that happens to carry it (A-05).
func TestCFG01UnknownKeyIsAnErrorNamingTheSectionTheKeyAndItsLine(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		key     string
		line    int
		section string
	}{
		{
			name: "at the root of a section",
			yaml: `
agent:
  id: "prod-fr-01"
  timezon: "Europe/Paris"
`,
			key:     "timezon",
			line:    3,
			section: "agent",
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
			key:     "encryptions",
			line:    4,
			section: "the root",
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
			key:     "hostname",
			line:    7,
			section: "databases[0]",
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
			key:     "directory",
			line:    7,
			section: "destinations[0]",
		},
		{
			name: "under tools, decoded by hand",
			yaml: `
agent:
  id: "prod-fr-01"
  timezone: "Europe/Paris"
databases:
  - id: erp
    engine: mariadb
    tools:
      strategy: exec
      containers: erp-mariadb
`,
			key:     "containers",
			line:    9,
			section: "tools",
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
			if !strings.Contains(got, c.section) {
				t.Errorf("the error does not name the section %q:\n%s", c.section, got)
			}

			// A-05 — an operator reads this, not a Go developer.
			// "yaml:" alone would match the temporary file name in the prefix.
			for _, jargon := range []string{"config.", "type ", "yaml: unmarshal", "not found in"} {
				if strings.Contains(got, jargon) {
					t.Errorf("the error leaks %q at the operator:\n%s", jargon, got)
				}
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
