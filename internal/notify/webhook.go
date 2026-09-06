package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"text/template"
	"time"
)

// WebhookConfig is EF-130's generic webhook.
type WebhookConfig struct {
	URL     string
	Headers map[string]string

	// Template renders the body. Empty means the event as JSON, which is what a
	// script wants; a chat platform wants its own shape, and needing a bridge
	// to post a message would be a poor answer.
	Template string

	// Client is injected by tests. Nil means a client with a timeout of its
	// own, because the default http.Client has none and would hang for ever.
	Client *http.Client
}

type webhook struct {
	cfg  WebhookConfig
	tmpl *template.Template
	cli  *http.Client
}

// NewWebhook builds the notifier, refusing anything that cannot work.
//
// The URL and the template are checked here so a broken alerting channel is a
// configuration error rather than a discovery made at three in the morning,
// when the alert it was meant to carry goes nowhere (PD-006).
func NewWebhook(cfg WebhookConfig) (Notifier, error) {
	parsed, err := url.Parse(cfg.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf(
			"notify: %q is not a webhook URL; it has to be http or https with a host", cfg.URL)
	}

	w := &webhook{cfg: cfg, cli: cfg.Client}
	if cfg.Template != "" {
		tmpl, err := template.New("webhook").Parse(cfg.Template)
		if err != nil {
			return nil, fmt.Errorf("notify: the webhook template does not parse: %w", err)
		}
		w.tmpl = tmpl
	}
	if w.cli == nil {
		w.cli = &http.Client{Timeout: 15 * time.Second}
	}
	return w, nil
}

func (w *webhook) Name() string { return "webhook " + redactURL(w.cfg.URL) }

func (w *webhook) Notify(ctx context.Context, ev Event) error {
	body, err := w.render(ev)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify: build the webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "koffr")
	for k, v := range w.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := w.cli.Do(req)
	if err != nil {
		// The URL may carry a token in its path -- Healthchecks.io and Slack
		// both do -- so it is redacted even here.
		return fmt.Errorf("notify: post to %s: %w", redactURL(w.cfg.URL), err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Drained so the connection can be reused rather than reopened for every
	// event.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("notify: %s answered %d", redactURL(w.cfg.URL), resp.StatusCode)
	}
	return nil
}

func (w *webhook) render(ev Event) ([]byte, error) {
	if w.tmpl == nil {
		body, err := json.Marshal(payloadOf(ev))
		if err != nil {
			return nil, fmt.Errorf("notify: encode the event: %w", err)
		}
		return body, nil
	}
	var out bytes.Buffer
	if err := w.tmpl.Execute(&out, ev); err != nil {
		return nil, fmt.Errorf("notify: render the webhook template: %w", err)
	}
	return out.Bytes(), nil
}

// payload is the JSON a receiver parses. Its field names are a contract: they
// may be added to, never renamed.
type payload struct {
	Kind       string            `json:"kind"`
	Severity   string            `json:"severity"`
	SourceID   string            `json:"source_id,omitempty"`
	BackupID   string            `json:"backup_id,omitempty"`
	Message    string            `json:"message"`
	OccurredAt string            `json:"occurred_at"`
	Details    map[string]string `json:"details,omitempty"`
}

func payloadOf(ev Event) payload {
	return payload{
		Kind: ev.Kind, Severity: string(ev.Severity),
		SourceID: ev.SourceID, BackupID: ev.BackupID,
		Message:    ev.Message,
		OccurredAt: ev.OccurredAt.UTC().Format(time.RFC3339),
		Details:    ev.Details,
	}
}

// redactURL keeps the host and drops the rest.
//
// A webhook URL is frequently a credential in itself: Slack, Discord and
// Healthchecks.io all put a secret in the path, and a log line naming the full
// URL hands it to whoever reads the logs (ENF-021).
func redactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "the configured URL"
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return parsed.Scheme + "://" + parsed.Host + "/" + strings.Repeat("x", 6)
	}
	return parsed.Scheme + "://" + parsed.Host
}
