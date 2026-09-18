package config

import (
	"fmt"
	"regexp"
	"strconv"

	"gopkg.in/yaml.v3"
)

// yaml.v3 reports an unknown key as "line 8: field timezon not found in type
// config.Agent". The line and the key are exactly what E-032 asks for; the type
// name is not — an operator reads this message, and config.Agent tells them
// nothing (A-05).
var unknownKeyMessage = regexp.MustCompile(`^line (\d+): field (\S+) not found in type `)

// unknownKeyError is a key koffr does not know, told the way the person who
// wrote the file will recognise it.
type unknownKeyError struct {
	Line    int
	Key     string
	Section string
}

func (e unknownKeyError) Error() string {
	return fmt.Sprintf("line %d: unknown key %q in %s", e.Line, e.Key, e.Section)
}

// translateUnknownKeys turns what yaml.v3 says into what an operator needs. It
// keeps the first problem only: a file with a typo is a file to fix and read
// again, and a wall of messages helps nobody.
func translateUnknownKeys(err error, document *yaml.Node) error {
	var typeError *yaml.TypeError
	if !asTypeError(err, &typeError) {
		return err
	}

	for _, message := range typeError.Errors {
		match := unknownKeyMessage.FindStringSubmatch(message)
		if match == nil {
			continue
		}

		line, convErr := strconv.Atoi(match[1])
		if convErr != nil {
			continue
		}
		key := match[2]

		return unknownKeyError{Line: line, Key: key, Section: sectionOf(document, line, key)}
	}

	// Something else went wrong — a wrong type, a malformed list. Pass it on
	// rather than dress it up as something it is not.
	return err
}

func asTypeError(err error, target **yaml.TypeError) bool {
	typeError, ok := err.(*yaml.TypeError) //nolint:errorlint // yaml.v3 never wraps it
	if !ok {
		return false
	}
	*target = typeError

	return true
}

// sectionOf walks the document to find where the offending key sits, so that
// the message can say "databases[0]" rather than a Go type name. Falls back to
// "the root" when the key sits there, and to "this file" when the walk finds
// nothing — a message without a section is still better than one with a type.
func sectionOf(document *yaml.Node, line int, key string) string {
	if document == nil {
		return "this file"
	}

	root := document
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}

	if path, found := findKey(root, "", line, key); found {
		if path == "" {
			return "the root"
		}

		return path
	}

	return "this file"
}

// findKey returns the path of the mapping that holds key at line.
func findKey(node *yaml.Node, path string, line int, key string) (string, bool) {
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			name, value := node.Content[i], node.Content[i+1]

			if name.Value == key && name.Line == line {
				return path, true
			}
			if found, ok := findKey(value, join(path, name.Value), line, key); ok {
				return found, true
			}
		}

	case yaml.SequenceNode:
		for i, item := range node.Content {
			if found, ok := findKey(item, fmt.Sprintf("%s[%d]", path, i), line, key); ok {
				return found, true
			}
		}
	}

	return "", false
}

func join(path, name string) string {
	if path == "" {
		return name
	}

	return path + "." + name
}

// unknownKeyIn is used by the decoders koffr writes by hand, where yaml.v3's
// strictness does not reach.
func unknownKeyIn(section string, node *yaml.Node) error {
	return unknownKeyError{Line: node.Line, Key: node.Value, Section: section}
}

// trimDocument is a small helper for callers that hold the raw bytes.
func parseDocument(raw []byte) *yaml.Node {
	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil
	}

	return &document
}
