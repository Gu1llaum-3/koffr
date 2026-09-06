package notify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// DeadMansSwitchConfig maps a source to the monitor that watches it (EF-131).
type DeadMansSwitchConfig struct {
	// URLs is per source, because that is how Healthchecks.io and Uptime Kuma
	// work: one check, one URL, one schedule to compare against. A single
	// global URL would tell you *something* backed up, which is the question
	// nobody asks.
	URLs map[string]string

	Client *http.Client
}

type deadMansSwitch struct {
	urls map[string]string
	cli  *http.Client
}

// NewDeadMansSwitch builds the pinger.
//
// This is the inverse of every other alert in Koffr, and the only one that
// catches a job which never ran at all. Ordinary alerting needs Koffr to be
// alive enough to complain; here the alarm comes from the ping *not* arriving,
// so it survives Koffr being crashed, stopped, or uninstalled by someone
// tidying up.
func NewDeadMansSwitch(cfg DeadMansSwitchConfig) (DeadMansSwitch, error) {
	for source, raw := range cfg.URLs {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, fmt.Errorf(
				"notify: the dead man's switch URL for source %s is not an http or https URL", source)
		}
	}
	cli := cfg.Client
	if cli == nil {
		cli = &http.Client{Timeout: 10 * time.Second}
	}
	return &deadMansSwitch{urls: cfg.URLs, cli: cli}, nil
}

// Ping tells the monitor this source succeeded.
//
// A source with no URL is not an error. The switch is opt-in per source, and
// refusing to back up an unmonitored one would be the wrong trade entirely.
func (d *deadMansSwitch) Ping(ctx context.Context, sourceID string) error {
	raw, watched := d.urls[sourceID]
	if !watched {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return fmt.Errorf("notify: build the ping for %s: %w", sourceID, err)
	}
	req.Header.Set("User-Agent", "koffr")

	resp, err := d.cli.Do(req)
	if err != nil {
		return fmt.Errorf("notify: ping the monitor for %s at %s: %w",
			sourceID, redactURL(raw), err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Worth reporting even though the supervisor will raise its own alarm
		// in a few minutes: this message says *why*, and the supervisor's will
		// only say that nothing arrived.
		return fmt.Errorf("notify: the monitor for %s at %s answered %d",
			sourceID, redactURL(raw), resp.StatusCode)
	}
	return nil
}
