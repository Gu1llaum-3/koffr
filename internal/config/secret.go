package config

import (
	"log/slog"

	"gopkg.in/yaml.v3"
)

// redacted is what a secret shows instead of itself.
const redacted = "[redacted]"

// Secret is a sensitive value. It never prints itself, whatever the caller
// reaches for — %v, %+v, %#v, slog or a YAML round trip — because E-115 wants
// reading the configuration to expose the topology of the fleet and nothing
// else. The value comes out only through Expose, which is greppable.
type Secret struct {
	value string
}

// NewSecret wraps a value that was resolved from the environment or a file.
func NewSecret(value string) Secret {
	return Secret{value: value}
}

// Expose returns the secret itself. Every call site is a place to look at
// during a security review.
func (s Secret) Expose() string {
	return s.value
}

// IsSet reports whether a value was given at all.
func (s Secret) IsSet() bool {
	return s.value != ""
}

// UnmarshalYAML reads the literal form of a secret written in the document.
func (s *Secret) UnmarshalYAML(node *yaml.Node) error {
	if err := node.Decode(&s.value); err != nil {
		return err //nolint:wrapcheck // the decoder already names the line
	}

	return nil
}

// IsZero tells yaml.v3 whether `omitempty` should drop this field. Without it
// the encoder reflects over the struct, finds only an unexported field, skips
// it as private, and concludes every Secret is empty — so a secret that is set
// would silently vanish from `config show` instead of being marked.
func (s Secret) IsZero() bool {
	return !s.IsSet()
}

// MarshalYAML renders the marker, never the value: this is what `config show`
// prints.
func (s Secret) MarshalYAML() (any, error) {
	if !s.IsSet() {
		return nil, nil
	}

	return redacted, nil
}

// String renders the marker, so that any accidental %v or %s is harmless.
func (s Secret) String() string {
	if !s.IsSet() {
		return ""
	}

	return redacted
}

// GoString keeps %#v from printing the value through the struct.
func (s Secret) GoString() string {
	return s.String()
}

// LogValue keeps slog from writing the value into a log line.
func (s Secret) LogValue() slog.Value {
	return slog.StringValue(s.String())
}
