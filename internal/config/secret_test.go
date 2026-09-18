package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const header = `
agent:
  id: "prod-fr-01"
  timezone: "Europe/Paris"
`

// Every sensitive field of § 5.1, and the document that carries it.
var sensitive = []struct {
	name string
	// body renders the section with the field written as form (literal, _env
	// or _file) and the given right-hand side.
	body func(form, value string) string
	// read returns the resolved secret.
	read func(*Config) Secret
}{
	{
		name: "databases[0].password",
		body: func(form, value string) string {
			return header + `
databases:
  - id: shop
    engine: postgresql
    ` + form + `: ` + value + `
`
		},
		read: func(c *Config) Secret { return c.Databases[0].Password },
	},
	{
		name: "destinations[0].access_key_id",
		body: func(form, value string) string {
			return header + `
destinations:
  - id: s3-ovh
    type: s3
    ` + form + `: ` + value + `
`
		},
		read: func(c *Config) Secret { return c.Destinations[0].AccessKeyID },
	},
	{
		name: "destinations[0].secret_access_key",
		body: func(form, value string) string {
			return header + `
destinations:
  - id: s3-ovh
    type: s3
    ` + form + `: ` + value + `
`
		},
		read: func(c *Config) Secret { return c.Destinations[0].SecretAccessKey },
	},
	{
		name: "alerts.channels[0].smtp.password",
		body: func(form, value string) string {
			return header + `
alerts:
  channels:
    - id: ops-mail
      type: email
      smtp:
        host: smtp.example.org
        ` + form + `: ` + value + `
`
		},
		read: func(c *Config) Secret { return c.Alerts.Channels[0].SMTP.Password },
	},
}

// CFG-02 — every sensitive field accepts its three forms: a literal value,
// *_env and *_file (E-033, § 5.1 F1.2).
func TestCFG02EverySensitiveFieldAcceptsItsThreeForms(t *testing.T) {
	const want = "s3cr3t"

	for _, field := range sensitive {
		base := field.name[strings.LastIndex(field.name, ".")+1:]

		t.Run(base+"/literal", func(t *testing.T) {
			config := load(t, field.body(base, want))
			if got := field.read(config).Expose(); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})

		t.Run(base+"/env", func(t *testing.T) {
			t.Setenv("KOFFR_TEST_SECRET", want)

			config := load(t, field.body(base+"_env", "KOFFR_TEST_SECRET"))
			if got := field.read(config).Expose(); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})

		t.Run(base+"/file", func(t *testing.T) {
			// A trailing newline is what an editor and systemd both produce.
			path := writeFile(t, "secret", want+"\n")

			config := load(t, field.body(base+"_file", path))
			if got := field.read(config).Expose(); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// CFG-03 — two forms of the same field at once is an error: koffr never picks
// one silently.
func TestCFG03TwoFormsOfTheSameFieldIsAnError(t *testing.T) {
	_, err := Load(write(t, header+`
databases:
  - id: shop
    engine: postgresql
    password: literal
    password_env: KOFFR_TEST_SECRET
`))
	if err == nil {
		t.Fatal("password and password_env were accepted together")
	}
	for _, want := range []string{"password", "password_env", "shop"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q:\n%v", want, err)
		}
	}
}

// CFG-03 — a *_file that is not there fails at start-up, not at the first
// backup three hours later.
func TestCFG03AMissingSecretFileFailsAtStartUp(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "never-written")

	_, err := Load(write(t, header+`
databases:
  - id: shop
    engine: postgresql
    password_file: `+absent+`
`))
	if err == nil {
		t.Fatal("a missing password_file was accepted")
	}
	if !strings.Contains(err.Error(), absent) {
		t.Errorf("the error does not name the file:\n%v", err)
	}
}

// CFG-03 — an *_env pointing at a variable that is not set fails the same way.
func TestCFG03AnUnsetEnvironmentVariableFailsAtStartUp(t *testing.T) {
	_, err := Load(write(t, header+`
databases:
  - id: shop
    engine: postgresql
    password_env: KOFFR_TEST_ABSENT_VARIABLE
`))
	if err == nil {
		t.Fatal("an unset password_env was accepted")
	}
	if !strings.Contains(err.Error(), "KOFFR_TEST_ABSENT_VARIABLE") {
		t.Errorf("the error does not name the variable:\n%v", err)
	}
}

func load(t *testing.T, body string) *Config {
	t.Helper()

	config, err := Load(write(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	return config
}

func writeFile(t *testing.T, name, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}

	return path
}
