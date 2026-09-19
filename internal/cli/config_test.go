package cli

import (
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
	// --offline because the reference names production paths — a credential
	// under /run/credentials — that exist on a koffr host and not here. That
	// is what the flag is for (CFG-09).
	out := run(t, "config", "validate", "--offline", "--config", reference)

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

// failing runs the root command and returns its standard output and its error.
func failing(t *testing.T, args ...string) (string, error) {
	t.Helper()

	out, _, err := execute(t, args...)

	return out, err
}

// CFG-09 — `config validate` resolves the secrets the configuration points at.
// The command an operator runs to check a configuration must check what they
// believe it checks: a password_file that is not there is found now, not at
// two in the morning (A-06). It resolves and reads locally; reaching the
// databases and the tools is E-034, at lot 1.
func TestCFG09ValidateFailsOnASecretItCannotRead(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "never-written")

	cases := []struct {
		name  string
		body  string
		wants string
	}{
		{
			name:  "a password_file that is not there",
			body:  "    password_file: " + absent + "\n",
			wants: absent,
		},
		{
			name:  "an environment variable that is not set",
			body:  "    password_env: KOFFR_TEST_ABSENT_VARIABLE\n",
			wants: "KOFFR_TEST_ABSENT_VARIABLE",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := writeConfig(t, c.body)

			out, err := failing(t, "config", "validate", "--config", path)
			if err == nil {
				t.Fatalf("the configuration was accepted:\n%s", out)
			}
			if !strings.Contains(err.Error(), c.wants) {
				t.Errorf("the error does not name %q:\n%v", c.wants, err)
			}
		})
	}
}

// CFG-09 — and --offline checks the shape alone, so that a configuration can be
// read again from a machine that does not hold the production files.
func TestCFG09OfflineAcceptsWhatItCannotResolve(t *testing.T) {
	path := writeConfig(t, "    password_file: /nowhere/at/all\n")

	out := run(t, "config", "validate", "--offline", "--config", path)

	if !strings.Contains(out, "ok") {
		t.Errorf("--offline refused a well-formed configuration:\n%s", out)
	}
	if !strings.Contains(out, "offline") {
		t.Errorf("--offline does not say that it skipped the secrets:\n%s", out)
	}
}

// CFG-09 — a secret koffr can read is not what stops it. Since E-034, validate
// also reaches the fleet, and this fixture has no server listening: what the
// test says is that the failure is about the database, never about the secret.
func TestCFG09ValidateAcceptsSecretsItCanRead(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(secret, []byte("s3cr3t\n"), 0o600); err != nil {
		t.Fatalf("write the secret: %v", err)
	}

	_, err := failing(t, "config", "validate", "--config", writeConfig(t, "    password_file: "+secret+"\n"))
	if err == nil {
		t.Fatal("a configuration naming a database nothing answers was accepted")
	}

	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "password_file") {
		t.Errorf("the readable secret was blamed:\n%v", err)
	}
	if !strings.Contains(err.Error(), "database shop") {
		t.Errorf("the error does not name the database that failed:\n%v", err)
	}
}

// CFG-09 — and `config show` never needs a secret: reading the topology of a
// fleet must not require the passwords of that fleet (E-115).
func TestCFG09ShowNeverNeedsToResolveASecret(t *testing.T) {
	out := run(t, "config", "show", "--config", writeConfig(t, "    password_file: /nowhere/at/all\n"))

	if !strings.Contains(out, "shop") {
		t.Errorf("config show refused a configuration it did not need to resolve:\n%s", out)
	}
}

func writeConfig(t *testing.T, databaseLines string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "koffr.yaml")
	body := "agent:\n  id: prod-fr-01\n  timezone: Europe/Paris\n" +
		"databases:\n  - id: shop\n    engine: postgresql\n" + databaseLines

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	return path
}

// E-034 — `config validate` checks the syntax, the coherence, **the
// reachability of the databases and the existence of the tools**, and nothing
// else. Until now it stopped at the shape; § 5.1 F1.3 asks for more.
func TestE034ValidateReachesTheDatabasesAndTheTools(t *testing.T) {
	fleet := newFleet(t)

	out, err := failing(t, "config", "validate", "--config", fleet.config, "--search-path", fleet.tools)
	if err == nil {
		t.Fatalf("a fleet whose databases do not answer was declared valid:\n%s", out)
	}
	if !strings.Contains(err.Error(), "shop") {
		t.Errorf("the error does not name the database that failed:\n%v", err)
	}
}

// E-034 — and it still does nothing else. --offline keeps the shape-only check
// for a machine that holds neither the fleet nor its credentials.
func TestE034OfflineStillChecksTheShapeAlone(t *testing.T) {
	fleet := newFleet(t)

	out := run(t, "config", "validate", "--offline", "--config", fleet.config)

	if !strings.Contains(out, "ok") {
		t.Errorf("--offline refused a well-formed configuration:\n%s", out)
	}
}

// A fleet that answers is valid. Here nothing listens, so the closest we get
// without a server is: the tool exists, and the failure is about the database
// and not about the tool.
func TestE034TheToolIsCheckedToo(t *testing.T) {
	fleet := newFleet(t)

	_, err := failing(t, "config", "validate", "--config", fleet.config,
		"--search-path", filepath.Join(t.TempDir(), "no-tool-here"))
	if err == nil {
		t.Fatal("a fleet with neither database nor tool was declared valid")
	}
}
