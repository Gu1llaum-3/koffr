package notify_test

import (
	"context"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/notify"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

func TestEmail_ComposesAReadableMessage(t *testing.T) {
	var sent []byte
	var toAddrs []string

	cfg := notify.EmailConfig{
		Host: "smtp.example.com", Port: 587,
		From: "koffr@example.com", To: []string{"ops@example.com"},
		Username: "koffr", Password: testutil.SecretSentinel,
	}
	notify.SetSendForTest(&cfg, func(_ string, _ smtp.Auth, _ string, to []string, msg []byte) error {
		toAddrs, sent = to, msg
		return nil
	})

	n, err := notify.NewEmail(cfg)
	require.NoError(t, err)
	require.NoError(t, n.Notify(t.Context(), notify.Event{
		Kind: notify.KindBackupFailed, Severity: notify.SeverityError,
		SourceID: "prod", Message: "pg_dump exited with status 1",
		OccurredAt: time.Date(2026, 3, 1, 2, 0, 0, 0, time.UTC),
	}))

	body := string(sent)
	assert.Equal(t, []string{"ops@example.com"}, toAddrs)
	assert.Contains(t, body, "Subject: [koffr] ERROR: backup.failed on prod",
		"the subject has to be readable in a phone preview at three in the morning")
	assert.Contains(t, body, "pg_dump exited with status 1")
	assert.Contains(t, body, "text/plain")

	// The SMTP password is a credential for the mail server, and it has no
	// business in the message it authenticates the sending of.
	testutil.AssertNoSecretLeak(t, body)
}

func TestNewEmail_RefusesWhatCannotDeliver(t *testing.T) {
	base := notify.EmailConfig{
		Host: "smtp.example.com", From: "koffr@example.com", To: []string{"ops@example.com"},
	}
	for name, mutate := range map[string]func(*notify.EmailConfig){
		"no host":                     func(c *notify.EmailConfig) { c.Host = "" },
		"no from":                     func(c *notify.EmailConfig) { c.From = "" },
		"no recipient":                func(c *notify.EmailConfig) { c.To = nil },
		"from is not an address":      func(c *notify.EmailConfig) { c.From = "koffr" },
		"recipient is not an address": func(c *notify.EmailConfig) { c.To = []string{"ops"} },
		// The shape a missing environment variable leaves behind, which
		// otherwise fails at the server with an unhelpful message.
		"username with no password": func(c *notify.EmailConfig) { c.Username = "koffr" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			_, err := notify.NewEmail(cfg)
			assert.Error(t, err)
		})
	}
}

func TestEmail_HonoursACancelledContext(t *testing.T) {
	cfg := notify.EmailConfig{
		Host: "smtp.example.com", From: "koffr@example.com", To: []string{"ops@example.com"},
	}
	block := make(chan struct{})
	defer close(block)
	notify.SetSendForTest(&cfg, func(string, smtp.Auth, string, []string, []byte) error {
		<-block
		return nil
	})

	n, err := notify.NewEmail(cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	err = n.Notify(ctx, notify.Event{Kind: "backup.failed"})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "did not finish"),
		"a mail server that never answers must not hold the scheduler")
}
