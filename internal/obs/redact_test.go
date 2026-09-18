package obs

import (
	"bytes"
	"strings"
	"testing"
)

const leaked = "hunter2-do-not-log-me"

// E-115 — a value that looks like a secret does not reach a log line, even when
// the caller passed it as a plain string. config.Secret masks itself; this is
// the net underneath, for the day somebody logs a password they read from
// somewhere else.
func TestASensitiveAttributeIsMasked(t *testing.T) {
	sensitive := []string{
		"password",
		"password_env",
		"smtp_password",
		"token",
		"uplink_token",
		"secret",
		"secret_access_key",
		"access_key_id",
		"private_key",
		"credentials",
		"authorization",
	}

	for _, key := range sensitive {
		t.Run(key, func(t *testing.T) {
			var out bytes.Buffer

			logger, closeLogger := newTestLogger(t, &out, Options{})
			logger.Info("connecting", key, leaked)
			closeLogger()

			if strings.Contains(out.String(), leaked) {
				t.Errorf("the attribute %q reached the log:\n%s", key, out.String())
			}
			if !strings.Contains(out.String(), redacted) {
				t.Errorf("the attribute %q was dropped instead of being marked:\n%s", key, out.String())
			}
		})
	}
}

// It masks under a group too, which is where an attribute usually ends up.
func TestASensitiveAttributeIsMaskedInsideAGroup(t *testing.T) {
	var out bytes.Buffer

	logger, closeLogger := newTestLogger(t, &out, Options{})
	logger.With("component", "uplink").WithGroup("server").Info("pushing", "token", leaked)
	closeLogger()

	if strings.Contains(out.String(), leaked) {
		t.Errorf("a grouped attribute reached the log:\n%s", out.String())
	}
}

// And it leaves everything else alone: a log nobody can read is no better than
// a log that leaks.
func TestAnOrdinaryAttributeIsUntouched(t *testing.T) {
	var out bytes.Buffer

	logger, closeLogger := newTestLogger(t, &out, Options{})
	logger.Info("backup finished",
		"database", "shop",
		"destination", "s3-ovh",
		"keyspace", "public",
		"size_bytes", 41231,
	)
	closeLogger()

	for _, want := range []string{"shop", "s3-ovh", "public", "41231"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the log lost %q:\n%s", want, out.String())
		}
	}
}
