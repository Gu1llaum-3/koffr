package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// Parse reads a configuration document and checks its shape: strict field
// names, a declared timezone, and one form per sensitive field. It reaches for
// nothing outside the document — no environment variable, no file, no server —
// which is what makes it usable on a machine that is not the one koffr runs on.
//
// name is what errors are prefixed with, usually the path the bytes came from.
func Parse(raw []byte, name string) (*Config, error) {
	var config Config

	// KnownFields is the whole of E-032: a key koffr does not know is an error
	// naming the key and its line, never a warning (CFG-01).
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)

	if err := decoder.Decode(&config); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	if err := config.resolveTimezone(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	if err := config.checkSecretForms(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	return &config, nil
}

// Load parses the file at path and then resolves its secrets, so that a
// password koffr cannot read stops it now rather than in the middle of the
// night's backup (CFG-03).
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the configuration: %w", err)
	}

	config, err := Parse(raw, path)
	if err != nil {
		return nil, err
	}

	if err := config.resolveSecrets(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return config, nil
}
