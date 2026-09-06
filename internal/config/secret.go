package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Secret is a credential written as a reference, never as a literal.
//
// EF-103 wants the configuration file safe to commit, and a reference is what
// makes that true by construction rather than by discipline. A literal is
// refused rather than warned about: a warning about a file that is already in
// Git is advice arriving too late.
//
// The resolved value never appears in YAML output, so `koffr config show` and
// every error message stay safe to paste into a ticket (ENF-021).
type Secret struct {
	// raw is what the file said: "env:NAME" or "file:/path".
	raw string
	// value is what it resolved to. Unexported and never marshalled.
	value string
}

// Value returns the resolved secret.
func (s Secret) Value() string { return s.value }

// Reference is how the secret is written in the file.
func (s Secret) Reference() string { return s.raw }

// IsZero reports an unset secret, so omitempty works.
func (s Secret) IsZero() bool { return s.raw == "" }

// UnmarshalYAML reads the reference and resolves it immediately.
//
// Resolving at load rather than at use means a missing environment variable is
// a configuration error reported by `koffr config validate`, not a surprise at
// 3 AM.
func (s *Secret) UnmarshalYAML(node *yaml.Node) error {
	var raw string
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("a secret must be a string like env:NAME or file:/path: %w", err)
	}
	s.raw = strings.TrimSpace(raw)
	return nil
}

// MarshalYAML writes the reference back, never the value.
func (s Secret) MarshalYAML() (any, error) { return s.raw, nil }

// resolve turns the reference into a value, or records why it could not.
func (s *Secret) validate(v *validator, path string, required bool) {
	if s.raw == "" {
		if required {
			v.add(path, "no value", "use env:NAME or file:/path")
		}
		return
	}

	kind, target, found := strings.Cut(s.raw, ":")
	if !found {
		v.add(path, "a secret must be a reference, not a literal",
			"write env:NAME to read it from the environment, or file:/path to read it "+
				"from a file, so this configuration stays safe to commit")
		return
	}

	switch kind {
	case "env":
		value, ok := os.LookupEnv(target)
		if !ok {
			v.add(path, fmt.Sprintf("the environment variable %s is not set", target),
				"export it before running Koffr, or point at a file instead")
			return
		}
		// Set to nothing is not a value. LookupEnv reports it as present, so
		// `export PGPASSWORD=` would produce an empty credential and a
		// connection refused in the middle of the night rather than a message
		// at load time (PD-006).
		if value == "" {
			v.add(path, fmt.Sprintf("the environment variable %s is set but empty", target),
				"give it a value, or remove the export so the problem is obvious")
			return
		}
		s.value = value
	case "file":
		info, err := os.Stat(target)
		if err != nil {
			v.add(path, fmt.Sprintf("cannot read %s: %v", target, err),
				"check the path and that Koffr may read it")
			return
		}
		// A secret file anyone can read is not a secret, and refusing is the
		// only way the check means anything.
		if info.Mode().Perm()&0o077 != 0 {
			v.add(path, fmt.Sprintf("%s is mode %o and readable by others", target, info.Mode().Perm()),
				fmt.Sprintf("chmod 0600 %s", target))
			return
		}
		content, err := os.ReadFile(target) //nolint:gosec // the path is the operator's own
		if err != nil {
			v.add(path, fmt.Sprintf("cannot read %s: %v", target, err), "")
			return
		}
		// A trailing newline is what an editor adds, not part of the secret.
		s.value = strings.TrimRight(string(content), "\r\n")
	default:
		v.add(path, fmt.Sprintf("%q is not a way to reference a secret", kind),
			"use env:NAME or file:/path")
	}
}
