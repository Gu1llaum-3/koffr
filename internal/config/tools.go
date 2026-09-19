package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ExecStrategy dumps through the container of the database. It is the only
// strategy § 5.2 F2.9 defines, and it is declared per database — never
// globally, because it hands koffr the Docker socket (E-046).
const ExecStrategy = "exec"

// Tools says where the dump tool of a database comes from. The specification
// § 5.1 writes it two ways: the word "auto", or a mapping that names an
// explicit strategy.
type Tools struct {
	Auto      bool
	Strategy  string
	Container string
}

// UnmarshalYAML accepts both writings of tools: the scalar "auto", and a
// mapping naming a strategy and its container.
func (t *Tools) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		if node.Value != "auto" {
			return fmt.Errorf("line %d: tools: %q is not known; expected \"auto\" or a mapping of strategy and container", node.Line, node.Value)
		}
		t.Auto = true

		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: tools: expected \"auto\" or a mapping of strategy and container", node.Line)
	}

	// Decoded by hand rather than by node.Decode: a nested decoder loses the
	// strictness of E-032, and an unknown key under tools must fail like any
	// other (CFG-01).
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]

		switch key.Value {
		case "strategy":
			t.Strategy = value.Value
		case "container":
			t.Container = value.Value
		default:
			return unknownKeyIn("tools", key)
		}
	}

	return nil
}
