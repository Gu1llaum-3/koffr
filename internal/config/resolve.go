package config

import (
	"fmt"
	"os"
	"strings"
)

// secretField is one sensitive field of § 5.1 in its three accepted forms
// (E-033): a literal value, the name of an environment variable, and the path
// of a file. At most one may be given.
type secretField struct {
	// where locates the field for the operator: "databases[0] shop".
	where string
	// key is the name of the field as § 5.1 writes it, without a suffix.
	key string
	// target receives the resolved value.
	target *Secret
	env    string
	file   string
}

// secretFields lists every sensitive field of the document. Adding a secret to
// the schema means adding it here, or it is neither resolved nor checked.
func (c *Config) secretFields() []secretField {
	var fields []secretField

	for i := range c.Databases {
		database := &c.Databases[i]
		fields = append(fields, secretField{
			where:  fmt.Sprintf("databases[%d] %s", i, database.ID),
			key:    "password",
			target: &database.Password,
			env:    database.PasswordEnv,
			file:   database.PasswordFile,
		})
	}

	for i := range c.Destinations {
		destination := &c.Destinations[i]
		fields = append(fields,
			secretField{
				where:  fmt.Sprintf("destinations[%d] %s", i, destination.ID),
				key:    "access_key_id",
				target: &destination.AccessKeyID,
				env:    destination.AccessKeyIDEnv,
				file:   destination.AccessKeyIDFile,
			},
			secretField{
				where:  fmt.Sprintf("destinations[%d] %s", i, destination.ID),
				key:    "secret_access_key",
				target: &destination.SecretAccessKey,
				env:    destination.SecretAccessKeyEnv,
				file:   destination.SecretAccessKeyFile,
			},
		)
	}

	for i := range c.Alerts.Channels {
		channel := &c.Alerts.Channels[i]
		fields = append(fields, secretField{
			where:  fmt.Sprintf("alerts.channels[%d] %s: smtp", i, channel.ID),
			key:    "password",
			target: &channel.SMTP.Password,
			env:    channel.SMTP.PasswordEnv,
			file:   channel.SMTP.PasswordFile,
		})
	}

	return fields
}

// checkSecretForms refuses a field written in two forms at once. koffr never
// picks one silently: the operator says where the secret comes from (CFG-03).
func (c *Config) checkSecretForms() error {
	for _, field := range c.secretFields() {
		forms := make([]string, 0, 3)
		if field.target.IsSet() {
			forms = append(forms, field.key)
		}
		if field.env != "" {
			forms = append(forms, field.key+"_env")
		}
		if field.file != "" {
			forms = append(forms, field.key+"_file")
		}

		if len(forms) > 1 {
			return fmt.Errorf("%s: %s are given together; keep one", field.where, strings.Join(forms, " and "))
		}
	}

	return nil
}

// resolveSecrets turns every *_env and *_file into a value. It runs at start-up
// on purpose: a password that is not there must stop koffr now, not three hours
// later in the middle of a backup (CFG-03).
func (c *Config) resolveSecrets() error {
	for _, field := range c.secretFields() {
		switch {
		case field.env != "":
			// internal/config is the one package allowed to read the
			// environment (ADR-0010, AR-05).
			value, found := os.LookupEnv(field.env)
			if !found {
				return fmt.Errorf("%s: %s_env reads %s, which is not set", field.where, field.key, field.env)
			}
			*field.target = NewSecret(value)

		case field.file != "":
			raw, err := os.ReadFile(field.file)
			if err != nil {
				// The path is already in the error os returns; repeating it
				// makes the line twice as long and no clearer.
				return fmt.Errorf("%s: %s_file: %w", field.where, field.key, err)
			}
			*field.target = NewSecret(strings.TrimRight(string(raw), "\r\n"))
		}
	}

	return nil
}
