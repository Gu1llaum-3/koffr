// Package testutil holds helpers shared by Koffr's tests.
//
// It is compiled into the normal build rather than kept behind a build tag,
// because contract suites in other packages import it. It must therefore never
// pull in anything heavy and never touch the network.
package testutil

import (
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
)

// SecretSentinel is the password every test configuration uses.
//
// ENF-021 forbids a credential from reaching a log, an error message or a
// process argument list. That is a promise until something checks it, so tests
// feed this value in and then assert it never comes back out.
//
// The shape is deliberate: long enough that no real log line produces it by
// accident, and containing a '/' so that the URL-encoded form is genuinely
// different from the plain one.
const SecretSentinel = "koffr-D0-N0T-L0G-thisvalue/9c1f"

// Encoded forms of the sentinel.
//
// A secret rarely reaches a log verbatim. It arrives percent-escaped from a
// connection string, base64 from an authorization header, hex from a dump of
// raw bytes. Checking only the plain form is the kind of test that passes while
// the thing it guards is broken.
var (
	SecretSentinelURLEncoded = url.QueryEscape(SecretSentinel)
	SecretSentinelBase64     = base64.StdEncoding.EncodeToString([]byte(SecretSentinel))
	SecretSentinelHex        = hex.EncodeToString([]byte(SecretSentinel))
)

// Leak locates one occurrence of the sentinel.
type Leak struct {
	// Index is the position of the offending string in the arguments given to
	// FindSecretLeaks, so a caller can tell stdout from stderr from a manifest.
	Index int
	// Encoding is how the secret was hiding: plain, url, base64 or hex.
	Encoding string
	// Excerpt is the surrounding text with the secret itself replaced, so a
	// failure message shows where the leak is without reprinting it.
	Excerpt string
}

// encodings is ordered plain-first: when a value could match several forms, the
// plain one is the most useful thing to report.
var encodings = []struct {
	name  string
	value string
}{
	{"plain", SecretSentinel},
	{"url", SecretSentinelURLEncoded},
	{"base64", SecretSentinelBase64},
	{"hex", SecretSentinelHex},
}

const excerptRadius = 40

// FindSecretLeaks reports, for each input containing the sentinel in any known
// encoding, where it was found. An input that leaks in several encodings is
// reported once, under the first encoding matched.
func FindSecretLeaks(inputs ...string) []Leak {
	var leaks []Leak
	for i, in := range inputs {
		for _, enc := range encodings {
			at := strings.Index(in, enc.value)
			if at < 0 {
				continue
			}
			leaks = append(leaks, Leak{
				Index:    i,
				Encoding: enc.name,
				Excerpt:  excerpt(in, at, len(enc.value)),
			})
			break
		}
	}
	return leaks
}

// AssertNoSecretLeak fails the test if the sentinel appears in any input.
//
// Pass everything a real operator could read: stdout, stderr, captured log
// output, the rendered process argument list, the manifest, and the bytes
// actually written to storage.
func AssertNoSecretLeak(t testing.TB, inputs ...string) {
	t.Helper()
	for _, leak := range FindSecretLeaks(inputs...) {
		t.Errorf("secret leaked in input %d (%s encoding): %s",
			leak.Index, leak.Encoding, leak.Excerpt)
	}
}

// excerpt returns the text around a match with the match itself replaced.
func excerpt(s string, at, width int) string {
	start := max(at-excerptRadius, 0)
	end := min(at+width+excerptRadius, len(s))

	var b strings.Builder
	if start > 0 {
		b.WriteString("...")
	}
	b.WriteString(s[start:at])
	b.WriteString("«SENTINEL»")
	b.WriteString(s[at+width : end])
	if end < len(s) {
		b.WriteString("...")
	}
	return b.String()
}
