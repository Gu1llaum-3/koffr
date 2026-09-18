package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Redacted renders the configuration as YAML with every secret replaced by a
// marker. It is what `koffr config show` prints: the topology of the fleet —
// which databases, which destinations, which channels — and nothing an
// operator would have to think twice about before pasting into a ticket
// (E-115).
func (c *Config) Redacted() (string, error) {
	out, err := yaml.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("render the configuration: %w", err)
	}

	return string(out), nil
}
