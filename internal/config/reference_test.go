package config

import (
	"os"
	"testing"
)

// CFG-05 — the target form of § 5.1, copied from the specification, is
// accepted, and every key lands where the rest of koffr will look for it
// (E-037). testdata/reference.yaml is that copy: only the product name was
// changed, per ADR-0001. Parse checks the shape; it resolves no secret, since
// the paths of the specification exist on a production host and not here.
func TestCFG05TheTargetFormOfTheSpecificationIsAccepted(t *testing.T) {
	config := parseReference(t)

	t.Run("agent", func(t *testing.T) {
		equal(t, "id", config.Agent.ID, "prod-fr-01")
		equal(t, "timezone", config.Agent.Timezone, "Europe/Paris")
		equal(t, "max_parallel_jobs", config.Agent.MaxParallelJobs, 2)
		equal(t, "location", config.Location().String(), "Europe/Paris")
	})

	t.Run("encryption", func(t *testing.T) {
		equal(t, "recipients_file", config.Encryption.RecipientsFile, "/etc/koffr/recipients.txt")
	})

	t.Run("databases", func(t *testing.T) {
		if len(config.Databases) != 2 {
			t.Fatalf("got %d databases, want 2", len(config.Databases))
		}

		shop := config.Databases[0]
		equal(t, "id", shop.ID, "boutique-prod")
		equal(t, "engine", shop.Engine, "postgresql")
		equal(t, "host", shop.Host, "10.0.3.12")
		equal(t, "port", shop.Port, 5432)
		equal(t, "database", shop.Database, "boutique")
		equal(t, "user", shop.User, "koffr_backup")
		equal(t, "password_file", shop.PasswordFile, "/run/credentials/koffr.service/boutique")
		equal(t, "tools auto", shop.Tools.Auto, true)
		equal(t, "staging", shop.Staging, "auto")
		equal(t, "schedule", shop.Schedule, "0 2 * * *")
		equal(t, "min_interval", shop.MinInterval, "24h")
		equal(t, "allow_remote_schedule", shop.AllowRemoteSchedule, true)
		equal(t, "allow_restore", shop.AllowRestore, false)
		equal(t, "destinations", len(shop.Destinations), 2)
		equal(t, "retention.last", shop.Retention.Last, 7)
		equal(t, "retention.daily", shop.Retention.Daily, 14)
		equal(t, "retention.weekly", shop.Retention.Weekly, 8)
		equal(t, "retention.monthly", shop.Retention.Monthly, 12)

		erp := config.Databases[1]
		equal(t, "engine", erp.Engine, "mariadb")
		equal(t, "password_env", erp.PasswordEnv, "ERP_BACKUP_PASSWORD")
		equal(t, "tools auto", erp.Tools.Auto, false)
		equal(t, "tools.strategy", erp.Tools.Strategy, "exec")
		equal(t, "tools.container", erp.Tools.Container, "erp-mariadb")
	})

	t.Run("destinations", func(t *testing.T) {
		if len(config.Destinations) != 3 {
			t.Fatalf("got %d destinations, want 3", len(config.Destinations))
		}

		disk, s3, sftp := config.Destinations[0], config.Destinations[1], config.Destinations[2]

		equal(t, "type", disk.Type, "filesystem")
		equal(t, "path", disk.Path, "/srv/backups")

		equal(t, "type", s3.Type, "s3")
		equal(t, "endpoint", s3.Endpoint, "https://s3.gra.io.cloud.ovh.net")
		equal(t, "region", s3.Region, "gra")
		equal(t, "bucket", s3.Bucket, "koffr-prod")
		equal(t, "access_key_id_env", s3.AccessKeyIDEnv, "OVH_ACCESS_KEY")
		equal(t, "secret_access_key_env", s3.SecretAccessKeyEnv, "OVH_SECRET_KEY")

		equal(t, "type", sftp.Type, "sftp")
		equal(t, "host", sftp.Host, "nas.interne.exemple.fr")
		equal(t, "port", sftp.Port, 22)
		equal(t, "user", sftp.User, "koffr")
		equal(t, "private_key_file", sftp.PrivateKeyFile, "/etc/koffr/sftp_ed25519")
		equal(t, "path", sftp.Path, "/volume1/backups")
	})

	t.Run("alerts", func(t *testing.T) {
		if len(config.Alerts.Channels) != 2 {
			t.Fatalf("got %d channels, want 2", len(config.Alerts.Channels))
		}

		mail, hook := config.Alerts.Channels[0], config.Alerts.Channels[1]

		equal(t, "type", mail.Type, "email")
		equal(t, "to", len(mail.To), 1)
		equal(t, "smtp.host", mail.SMTP.Host, "smtp.exemple.fr")
		equal(t, "smtp.port", mail.SMTP.Port, 587)
		equal(t, "smtp.user", mail.SMTP.User, "koffr")
		equal(t, "smtp.password_env", mail.SMTP.PasswordEnv, "SMTP_PASSWORD")

		equal(t, "type", hook.Type, "webhook")
		equal(t, "url", hook.URL, "https://hooks.exemple.fr/koffr")

		if len(config.Alerts.Rules) != 1 {
			t.Fatalf("got %d rules, want 1", len(config.Alerts.Rules))
		}
		equal(t, "rules[0].on", len(config.Alerts.Rules[0].On), 5)
		equal(t, "rules[0].channels", len(config.Alerts.Rules[0].Channels), 2)
	})

	t.Run("server", func(t *testing.T) {
		equal(t, "enabled", config.Server.Enabled, false)
		equal(t, "url", config.Server.URL, "https://koffr.interne.exemple.fr")
		equal(t, "token_file", config.Server.TokenFile, "/etc/koffr/uplink.token")
		equal(t, "delegate_alerts", config.Server.DelegateAlerts, true)
		equal(t, "fallback_after", config.Server.FallbackAfter, "15m")
	})
}

func parseReference(t *testing.T) *Config {
	t.Helper()

	const path = "testdata/reference.yaml"

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	config, err := Parse(raw, path)
	if err != nil {
		t.Fatalf("the target form of § 5.1 was refused: %v", err)
	}

	return config
}

func equal[T comparable](t *testing.T, field string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}
