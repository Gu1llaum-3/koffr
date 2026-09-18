package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const reference = "../config/testdata/reference.yaml"

// CFG-05, seen from the command line: the target form of § 5.1 is accepted as
// written. `config validate` checks the shape only at this stage — reaching the
// databases and the tools is E-034, at lot 1.
func TestConfigValidateAcceptsTheTargetForm(t *testing.T) {
	out := run(t, "config", "validate", "--config", reference)

	if !strings.Contains(out, "ok") {
		t.Errorf("a valid configuration did not say so:\n%s", out)
	}
}

// CFG-01, seen from the command line: a typo is named, with its line, and the
// command fails.
func TestConfigValidateRefusesAnUnknownKeyAndSaysWhere(t *testing.T) {
	path := typo(t)

	out, err := failing(t, "config", "validate", "--config", path)
	if err == nil {
		t.Fatalf("a mistyped key was accepted:\n%s", out)
	}

	for _, want := range []string{"timezon", "line 3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not give %q:\n%v", want, err)
		}
	}
}

// CFG-06, seen from the command line: the topology is printed in full.
func TestConfigShowPrintsTheTopology(t *testing.T) {
	out := run(t, "config", "show", "--config", reference)

	for _, want := range []string{
		"boutique-prod", "postgresql", "10.0.3.12", "Europe/Paris",
		"s3-ovh", "sftp-nas", "ops-mail",
		// The forms that say where a secret comes from are topology too: they
		// name a variable and a path, never a value.
		"password_file", "password_env", "access_key_id_env",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the topology lost %q:\n%s", want, out)
		}
	}
}

// CFG-06, seen from the command line: a secret written in the file comes out
// masked, and a field that carries none is not printed at all.
func TestConfigShowRedactsASecretWrittenInTheFile(t *testing.T) {
	const secret = "hunter2-do-not-print-me"

	path := filepath.Join(t.TempDir(), "koffr.yaml")
	body := "agent:\n  id: shop\n  timezone: Europe/Paris\n" +
		"databases:\n  - id: shop\n    engine: postgresql\n    password: " + secret + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	out := run(t, "config", "show", "--config", path)

	if strings.Contains(out, secret) {
		t.Fatalf("config show printed the password:\n%s", out)
	}
	if !strings.Contains(out, "[redacted]") {
		t.Errorf("config show did not mark where the password was:\n%s", out)
	}
	if strings.Contains(out, "access_key_id") {
		t.Errorf("config show invented a secret that is not in the file:\n%s", out)
	}
}

// N-15 — koffr has no way to print a secret. The flag exists so that the
// command reads as the plan writes it, and refusing it is the whole point.
func TestConfigShowRefusesToStopRedacting(t *testing.T) {
	if _, err := failing(t, "config", "show", "--config", reference, "--redact=false"); err == nil {
		t.Fatal("--redact=false was accepted: koffr offered to print secrets")
	}
}

// A missing configuration is named, not swallowed.
func TestConfigValidateNamesAMissingFile(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent.yaml")

	if _, err := failing(t, "config", "validate", "--config", absent); err == nil {
		t.Fatal("a missing configuration file was accepted")
	}
}

func typo(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "koffr.yaml")
	body := "agent:\n  id: \"prod-fr-01\"\n  timezon: \"Europe/Paris\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	return path
}

// failing runs the root command and returns its output and its error.
func failing(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer
	root := NewRoot()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)

	return out.String(), root.Execute()
}
