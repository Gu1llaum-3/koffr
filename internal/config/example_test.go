package config

import (
	"os"
	"testing"
)

const (
	examplePath   = "../../examples/koffr.yaml"
	referencePath = "testdata/reference.yaml"
)

// CFG-10 — examples/koffr.yaml is shipped for an operator to start from, and it
// is the same document the tests check. An example that drifts from what koffr
// accepts is worse than no example at all (A-03, N-4).
func TestCFG10TheShippedExampleIsAccepted(t *testing.T) {
	raw, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("read %s: %v", examplePath, err)
	}

	if _, err := Parse(raw, examplePath); err != nil {
		t.Fatalf("the example koffr ships is refused by koffr: %v", err)
	}
}

// CFG-10 — and the copy the tests read is byte for byte the one operators get.
func TestCFG10TheExampleAndTheTestReferenceAreTheSameDocument(t *testing.T) {
	example, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("read %s: %v", examplePath, err)
	}

	reference, err := os.ReadFile(referencePath)
	if err != nil {
		t.Fatalf("read %s: %v", referencePath, err)
	}

	if string(example) != string(reference) {
		t.Errorf("%s and %s have drifted apart; one of them is now a lie", examplePath, referencePath)
	}
}
