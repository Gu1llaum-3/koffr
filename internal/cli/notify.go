package cli

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/notify"
)

// buildHub turns the configuration into notification channels.
//
// Every failure here is a configuration error, refused before the scheduler
// starts. Alerting that is broken is worse than alerting that is absent,
// because absent is obvious and broken looks like quiet (PD-006).
func (a *app) buildHub(cfg config.Config) (*notify.Hub, error) {
	hub := &notify.Hub{
		// Reported, never returned: see notify.Hub. A channel that failed is
		// worth a line in the log and nothing more, because the backup it was
		// reporting on already happened.
		OnError: func(name string, err error) {
			a.warnf("koffr: notification via %s failed: %v", name, err)
		},
	}

	for i, w := range cfg.Notify.Webhooks {
		headers := make(map[string]string, len(w.Headers))
		for name, value := range w.Headers {
			headers[name] = value.Value()
		}
		n, err := notify.NewWebhook(notify.WebhookConfig{
			URL: w.URL.Value(), Headers: headers, Template: w.Template,
		})
		if err != nil {
			return nil, &Fault{Code: ExitConfig,
				Err: fmt.Errorf("notify.webhooks[%d]: %w", i, err)}
		}
		hub.Channels = append(hub.Channels, notify.Channel{
			Notifier: n, MinSeverity: notify.Severity(w.MinSeverity), Kinds: w.Kinds,
		})
	}

	if e := cfg.Notify.Email; e != nil {
		n, err := notify.NewEmail(notify.EmailConfig{
			Host: e.Host, Port: e.Port, From: e.From, To: e.To,
			Username: e.Username, Password: e.Password.Value(),
		})
		if err != nil {
			return nil, &Fault{Code: ExitConfig, Err: err}
		}
		hub.Channels = append(hub.Channels, notify.Channel{
			Notifier: n, MinSeverity: notify.Severity(e.MinSeverity), Kinds: e.Kinds,
		})
	}
	return hub, nil
}

// buildDeadMansSwitch is EF-131.
func (a *app) buildDeadMansSwitch(cfg config.Config) (notify.DeadMansSwitch, error) {
	urls := make(map[string]string, len(cfg.Notify.DeadMansSwitch))
	// The cross-reference against the sources happens in config validation,
	// where a mistake is found while someone is still looking at the file.
	for source, url := range cfg.Notify.DeadMansSwitch {
		urls[source] = url.Value()
	}
	dms, err := notify.NewDeadMansSwitch(notify.DeadMansSwitchConfig{
		URLs: urls, Client: &http.Client{Timeout: 10 * time.Second},
	})
	if err != nil {
		return nil, &Fault{Code: ExitConfig, Err: err}
	}
	return dms, nil
}

// reportSuccess publishes a completed backup and pings the switch.
//
// Called where the backup id is known, which is inside the work itself: the
// scheduler's result carries an attempt and an error, not an identifier, and
// inventing a blank one for the payload would make the event useless to anyone
// correlating it with `koffr show`.
//
// The ping goes out only on success. A dead man's switch that fired on an
// attempt would report health for a backup that failed, which is the one lie
// the whole mechanism exists to prevent.
//
// It may reach the monitor before the webhook reaches its receiver: hub
// delivery is queued and this call is not. That ordering does not matter --
// they are different systems answering different questions -- and serialising
// them would put a slow webhook in front of the health signal.
func (a *app) reportSuccess(
	ctx context.Context, hub *notify.Hub, dms notify.DeadMansSwitch,
	sourceID, backupID string, bytes int64,
) {
	hub.Publish(ctx, notify.Event{
		Kind: notify.KindBackupCompleted, Severity: notify.SeverityInfo,
		SourceID: sourceID, BackupID: backupID,
		Message: "backup completed",
		Details: map[string]string{"bytes": fmt.Sprint(bytes)},
	})
	if err := dms.Ping(ctx, sourceID); err != nil {
		a.warnf("koffr: %v", err)
	}
}

// reportFailure publishes an attempt that did not work.
//
// Retrying and failed are separate events on purpose. One says "wait, I am
// handling it" and the other says "come and look", and a channel that could not
// tell them apart would either wake someone for a blip or stay quiet through a
// real outage.
func (a *app) reportFailure(
	ctx context.Context, hub *notify.Hub, sourceID string, attempt int, willRetry bool, err error,
) {
	if willRetry {
		hub.Publish(ctx, notify.Event{
			Kind: notify.KindBackupRetrying, Severity: notify.SeverityWarning,
			SourceID: sourceID,
			Message:  fmt.Sprintf("attempt %d failed, another will follow: %v", attempt, err),
		})
		return
	}
	hub.Publish(ctx, notify.Event{
		Kind: notify.KindBackupFailed, Severity: notify.SeverityError,
		SourceID: sourceID,
		Message:  fmt.Sprintf("backup failed after %d attempt(s): %v", attempt, err),
	})
}
