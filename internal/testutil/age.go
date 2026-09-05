package testutil

import (
	"testing"

	"filippo.io/age"
)

// AgeIdentity generates a real age identity for a test.
//
// Generated rather than written down, for two reasons. A literal key in a
// source file trips the pre-commit secret guard, and widening that guard's
// allowlist for a fixture would weaken a control that has already failed open
// once. And a generated identity is the real thing, so a test using one is
// exercising what production does rather than a string that looks like it.
func AgeIdentity(tb testing.TB) (identity, recipient string) {
	tb.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		tb.Fatalf("generate age identity: %v", err)
	}
	return id.String(), id.Recipient().String()
}
