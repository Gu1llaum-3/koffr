package testutil_test

import (
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

// A leak detector that misses a leak is worse than none at all: it converts an
// untested property into a false guarantee. So it gets tested itself, including
// the encodings a secret can hide behind by the time it reaches a log line.

func TestFindSecretLeaks_Clean(t *testing.T) {
	leaks := testutil.FindSecretLeaks("connecting to db1", "backup completed", "")
	if len(leaks) != 0 {
		t.Fatalf("clean input reported leaks: %v", leaks)
	}
}

func TestFindSecretLeaks_Plain(t *testing.T) {
	leaks := testutil.FindSecretLeaks("dsn=postgres://u:" + testutil.SecretSentinel + "@h/db")
	if len(leaks) != 1 {
		t.Fatalf("want 1 leak, got %d: %v", len(leaks), leaks)
	}
	if leaks[0].Encoding != "plain" {
		t.Errorf("Encoding = %q, want %q", leaks[0].Encoding, "plain")
	}
	if leaks[0].Index != 0 {
		t.Errorf("Index = %d, want 0", leaks[0].Index)
	}
}

// A secret does not always reach a log verbatim. It can arrive URL-escaped from
// a connection string, base64-encoded from a header, or JSON-escaped from a
// structured log. Checking only the plain form would pass while leaking.
func TestFindSecretLeaks_Encoded(t *testing.T) {
	for _, tc := range []struct {
		name     string
		haystack string
		encoding string
	}{
		{"url", "u=" + testutil.SecretSentinelURLEncoded, "url"},
		{"base64", "auth " + testutil.SecretSentinelBase64, "base64"},
		{"hex", "0x" + testutil.SecretSentinelHex, "hex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leaks := testutil.FindSecretLeaks(tc.haystack)
			if len(leaks) != 1 {
				t.Fatalf("want 1 leak, got %d: %v", len(leaks), leaks)
			}
			if leaks[0].Encoding != tc.encoding {
				t.Errorf("Encoding = %q, want %q", leaks[0].Encoding, tc.encoding)
			}
		})
	}
}

// The sentinel must not be something a real log could produce by accident,
// otherwise every run reports a phantom leak.
func TestSentinel_IsDistinctive(t *testing.T) {
	if len(testutil.SecretSentinel) < 24 {
		t.Errorf("sentinel is %d chars, too short to be unmistakable", len(testutil.SecretSentinel))
	}
	leaks := testutil.FindSecretLeaks("password secret token credential 12345")
	if len(leaks) != 0 {
		t.Errorf("common words matched the sentinel: %v", leaks)
	}
}

func TestFindSecretLeaks_ReportsEveryInput(t *testing.T) {
	leaks := testutil.FindSecretLeaks(
		"clean",
		"leaked "+testutil.SecretSentinel,
		"clean too",
		"also "+testutil.SecretSentinelBase64,
	)
	if len(leaks) != 2 {
		t.Fatalf("want 2 leaks, got %d: %v", len(leaks), leaks)
	}
	if leaks[0].Index != 1 || leaks[1].Index != 3 {
		t.Errorf("indexes = %d,%d; want 1,3", leaks[0].Index, leaks[1].Index)
	}
}
