package obs

import (
	"log/slog"
	"strings"
)

// redacted is what a sensitive attribute shows instead of its value.
const redacted = "[redacted]"

// sensitiveWords are the words that make an attribute a secret. A key is
// masked when it contains one of them, so that smtp_password and
// secret_access_key are caught as well as password and token.
var sensitiveWords = []string{
	"password",
	"passphrase",
	"secret",
	"token",
	"credential",
	"authorization",
	"access_key",
	"private_key",
	"api_key",
}

// redactSensitive masks a value whose key names a secret. config.Secret already
// masks itself (CFG-06); this catches the password somebody read from a file
// and logged as a plain string, which is the slip no type can prevent.
//
// The value is replaced, never dropped: a log that hides that a field existed
// is harder to debug than one that says a secret was there.
func redactSensitive(_ []string, attr slog.Attr) slog.Attr {
	if isSensitive(attr.Key) {
		return slog.String(attr.Key, redacted)
	}

	return attr
}

func isSensitive(key string) bool {
	lowered := strings.ToLower(key)

	for _, word := range sensitiveWords {
		if strings.Contains(lowered, word) {
			return true
		}
	}

	return false
}
