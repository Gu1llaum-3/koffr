package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const leak = "hunter2-do-not-print-me"

// A configuration whose every sensitive field is loaded, each through a
// different form, so that no path escapes the check.
func loadedSecrets(t *testing.T) *Config {
	t.Helper()

	t.Setenv("KOFFR_TEST_SECRET", leak)
	file := writeFile(t, "smtp", leak+"\n")

	return load(t, header+`
databases:
  - id: shop
    engine: postgresql
    password: `+leak+`
destinations:
  - id: s3-ovh
    type: s3
    access_key_id_env: KOFFR_TEST_SECRET
alerts:
  channels:
    - id: ops-mail
      type: email
      smtp:
        host: smtp.example.org
        password_file: `+file+`
`)
}

// CFG-06 — a secret never prints itself, whichever way a caller reaches for it.
// A password in a log line or a crash dump is the leak E-115 is about.
func TestCFG06AConfigurationNeverPrintsItsSecrets(t *testing.T) {
	config := loadedSecrets(t)

	renderings := map[string]string{
		"%v":  fmt.Sprintf("%v", config),
		"%+v": fmt.Sprintf("%+v", config),
		"%#v": fmt.Sprintf("%#v", config),
		// A secret printed on its own, which is the likeliest slip.
		// The verb is the point of the test, so the simplification staticcheck
		// suggests would remove what is being checked.
		"%s of a secret":  fmt.Sprintf("%s", config.Databases[0].Password), //nolint:staticcheck // S1025: the verb is under test
		"%v of a secret":  fmt.Sprintf("%v", config.Databases[0].Password),
		"%#v of a secret": fmt.Sprintf("%#v", config.Databases[0].Password),
	}
	for verb, rendered := range renderings {
		if strings.Contains(rendered, leak) {
			t.Errorf("%s leaks the secret:\n%s", verb, rendered)
		}
	}
}

// CFG-06 — and slog does not leak it either, which is where it would actually
// end up (E-121 writes JSON logs).
func TestCFG06SlogNeverPrintsASecret(t *testing.T) {
	config := loadedSecrets(t)

	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, nil))
	logger.Info("configuration loaded",
		"config", config,
		"password", config.Databases[0].Password,
	)

	if strings.Contains(out.String(), leak) {
		t.Errorf("slog leaks the secret:\n%s", out.String())
	}
}

// CFG-06 — the secret is still reachable on purpose, and only on purpose.
func TestCFG06ASecretIsReachableThroughExposeOnly(t *testing.T) {
	config := loadedSecrets(t)

	for name, secret := range map[string]Secret{
		"databases[0].password":            config.Databases[0].Password,
		"destinations[0].access_key_id":    config.Destinations[0].AccessKeyID,
		"alerts.channels[0].smtp.password": config.Alerts.Channels[0].SMTP.Password,
	} {
		if got := secret.Expose(); got != leak {
			t.Errorf("%s: Expose() = %q, want the secret", name, got)
		}
		if got := secret.String(); got != redacted {
			t.Errorf("%s: String() = %q, want %q", name, got, redacted)
		}
	}
}

// CFG-06 — what `koffr config show --redact` prints carries the topology and
// no secret: an operator can paste it into a ticket.
func TestCFG06RedactedRendersTheTopologyWithoutASecret(t *testing.T) {
	config := loadedSecrets(t)

	shown, err := config.Redacted()
	if err != nil {
		t.Fatalf("Redacted: %v", err)
	}

	if strings.Contains(shown, leak) {
		t.Fatalf("the redacted rendering leaks the secret:\n%s", shown)
	}
	if !strings.Contains(shown, redacted) {
		t.Errorf("the redacted rendering does not mark where the secrets were:\n%s", shown)
	}
	// The topology is what stays: hosts, identifiers, destinations.
	for _, want := range []string{"shop", "postgresql", "s3-ovh", "smtp.example.org", "Europe/Paris"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the redacted rendering lost %q:\n%s", want, shown)
		}
	}
}

// CFG-06 — a sensitive field nobody filled in is not printed at all: an empty
// marker would suggest a secret is set where none is.
func TestCFG06AnUnsetSensitiveFieldIsNotPrinted(t *testing.T) {
	config := load(t, header+`
databases:
  - id: shop
    engine: postgresql
destinations:
  - id: s3-ovh
    type: s3
alerts:
  channels:
    - id: ops-mail
      type: email
      smtp:
        host: smtp.example.org
`)

	shown, err := config.Redacted()
	if err != nil {
		t.Fatalf("Redacted: %v", err)
	}

	// The three secret-bearing keys, none of them set in the document above.
	for _, unwanted := range []string{"password:", "access_key_id:", "secret_access_key:"} {
		if strings.Contains(shown, unwanted) {
			t.Errorf("%s is printed although nothing set it:\n%s", unwanted, shown)
		}
	}
	// The forms that point at a secret are topology and stay.
	if !strings.Contains(shown, "password_env") {
		t.Errorf("the rendering lost the keys that say where a secret comes from:\n%s", shown)
	}
}
