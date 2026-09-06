package notify_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/notify"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

// capture is a webhook receiver that remembers what arrived.
type capture struct {
	mu      sync.Mutex
	bodies  []string
	headers []http.Header
	status  int
}

func (c *capture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	c.bodies = append(c.bodies, string(body))
	c.headers = append(c.headers, r.Header.Clone())
	status := c.status
	c.mu.Unlock()

	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
}

func (c *capture) last() (string, http.Header) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) == 0 {
		return "", nil
	}
	return c.bodies[len(c.bodies)-1], c.headers[len(c.headers)-1]
}

func TestWebhook_PostsTheEventAsJSON(t *testing.T) {
	rec := &capture{}
	srv := httptest.NewServer(rec)
	defer srv.Close()

	w, err := notify.NewWebhook(notify.WebhookConfig{
		URL:     srv.URL,
		Headers: map[string]string{"X-Koffr-Token": "opaque"},
	})
	require.NoError(t, err)

	require.NoError(t, w.Notify(t.Context(), notify.Event{
		Kind: notify.KindBackupFailed, Severity: notify.SeverityError,
		SourceID: "prod", Message: "pg_dump exited with status 1",
		OccurredAt: time.Date(2026, 3, 1, 2, 0, 0, 0, time.UTC),
		Details:    map[string]string{"destination": "main"},
	}))

	body, headers := rec.last()
	assert.Equal(t, "opaque", headers.Get("X-Koffr-Token"))
	assert.Equal(t, "application/json", headers.Get("Content-Type"))

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	assert.Equal(t, "backup.failed", got["kind"])
	assert.Equal(t, "error", got["severity"])
	assert.Equal(t, "prod", got["source_id"])
	assert.Equal(t, "2026-03-01T02:00:00Z", got["occurred_at"])
}

// EF-130 asks for a configurable template, because every chat platform wants a
// different shape and nobody should need a bridge to post a message.
func TestWebhook_UsesAConfiguredTemplate(t *testing.T) {
	rec := &capture{}
	srv := httptest.NewServer(rec)
	defer srv.Close()

	w, err := notify.NewWebhook(notify.WebhookConfig{
		URL:      srv.URL,
		Template: `{"text":"{{.Severity}}: {{.SourceID}} — {{.Message}}"}`,
	})
	require.NoError(t, err)
	require.NoError(t, w.Notify(t.Context(), notify.Event{
		Kind: notify.KindBackupCaughtUp, Severity: notify.SeverityWarning,
		SourceID: "prod", Message: "a missed window is being made good",
	}))

	body, _ := rec.last()
	assert.JSONEq(t, `{"text":"warning: prod — a missed window is being made good"}`, body)
}

// A template that does not parse is refused when the configuration loads, not
// at three in the morning when the alert it was meant to send goes nowhere.
func TestNewWebhook_RefusesABadTemplate(t *testing.T) {
	_, err := notify.NewWebhook(notify.WebhookConfig{URL: "http://x", Template: "{{.Severity"})
	require.Error(t, err)
}

func TestNewWebhook_RefusesAURLThatIsNotOne(t *testing.T) {
	for _, url := range []string{"", "not a url", "ftp://example.com/hook"} {
		_, err := notify.NewWebhook(notify.WebhookConfig{URL: url})
		assert.Error(t, err, "url %q", url)
	}
}

// A receiver answering 500 is a delivery that did not happen, and saying so is
// what lets an operator find out their alerting is broken before they need it.
func TestWebhook_ReportsAnUnhappyReceiver(t *testing.T) {
	rec := &capture{status: http.StatusInternalServerError}
	srv := httptest.NewServer(rec)
	defer srv.Close()

	w, err := notify.NewWebhook(notify.WebhookConfig{URL: srv.URL})
	require.NoError(t, err)

	err = w.Notify(t.Context(), notify.Event{Kind: "backup.failed"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

// The webhook is the one place Koffr deliberately sends data outward, which
// makes it the place a leak would actually leave the machine (ENF-021).
func TestWebhook_SendsNoSecret(t *testing.T) {
	rec := &capture{}
	srv := httptest.NewServer(rec)
	defer srv.Close()

	w, err := notify.NewWebhook(notify.WebhookConfig{
		URL:      srv.URL,
		Headers:  map[string]string{"Authorization": "Bearer " + testutil.SecretSentinel},
		Template: `{"text":"{{.Message}}"}`,
	})
	require.NoError(t, err)
	require.NoError(t, w.Notify(t.Context(), notify.Event{
		Kind: "backup.failed", SourceID: "prod",
		Message: "backup failed: connection refused",
	}))

	body, headers := rec.last()
	testutil.AssertNoSecretLeak(t, body)
	// The token in the header is the exception, and it is deliberate: it is the
	// receiver's own credential, sent to the receiver, over the channel the
	// operator configured for exactly that.
	assert.Contains(t, headers.Get("Authorization"), testutil.SecretSentinel)
}
