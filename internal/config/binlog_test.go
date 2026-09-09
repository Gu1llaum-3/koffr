package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

// mariaWithBinlog is `valid` turned into a MariaDB source that archives its
// binary log, with the spool declared.
func mariaWithBinlog(t *testing.T, sourceExtra, topExtra string) string {
	t.Helper()
	body := strings.Replace(valid, "engine: postgresql",
		"engine: mariadb\n    binlog:\n      enabled: true"+sourceExtra, 1)
	return strings.Replace(body, "catalog:", "binlog:\n  spool_dir: /var/lib/koffr/spool"+topExtra+"\ncatalog:", 1)
}

func TestBinlog_DefaultsAndBounds(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)

	cfg, err := config.Load(write(t, mariaWithBinlog(t, "", "")))
	require.NoError(t, err)
	// Two gigabytes of spool, resuming at a fifth: the hysteresis that measured
	// well elsewhere, and a default nobody has to read about to be safe.
	assert.Equal(t, config.DefaultSpoolHigh, cfg.Binlog.High)
	assert.Equal(t, config.DefaultSpoolHigh/5, cfg.Binlog.Low)
	require.NotNil(t, cfg.Sources["prod-pg-main"].Binlog)
	assert.True(t, cfg.Sources["prod-pg-main"].Binlog.Enabled)
	assert.Zero(t, cfg.Sources["prod-pg-main"].Binlog.RotateEvery, "rotation is off unless asked for")
}

func TestBinlog_SizesAreWrittenTheWayOperatorsWriteThem(t *testing.T) {
	for in, want := range map[string]uint64{
		"2G": 2 << 30, "400M": 400 << 20, "64K": 64 << 10, "1024": 1024,
		"2g": 2 << 30, "2GiB": 2 << 30, "2GB": 2 << 30, "1T": 1 << 40,
	} {
		got, err := config.ParseSize(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "lots", "2X", "-1G"} {
		_, err := config.ParseSize(bad)
		assert.Error(t, err, bad)
	}
}

// Refused rather than ignored: an operator who turned it on believes they can
// recover to a point in time, and on PostgreSQL they cannot this way (PD-006).
func TestBinlog_RefusedOnAPostgreSQLSource(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)
	body := strings.Replace(valid, "engine: postgresql",
		"engine: postgresql\n    binlog:\n      enabled: true", 1)
	body = strings.Replace(body, "catalog:", "binlog:\n  spool_dir: /var/lib/koffr/spool\ncatalog:", 1)
	_, err := config.Load(write(t, body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binlog.enabled")
	assert.Contains(t, err.Error(), "EF-016", "the PostgreSQL equivalent is named")
}

func TestBinlog_EveryProblemIsNamed(t *testing.T) {
	setIdentity(t)
	t.Setenv("PGPASSWORD", testutil.SecretSentinel)

	t.Run("destination not among the source's", func(t *testing.T) {
		_, err := config.Load(write(t, mariaWithBinlog(t, "\n      destination: offsite", "")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "binlog.destination")
	})
	t.Run("rotating too often", func(t *testing.T) {
		_, err := config.Load(write(t, mariaWithBinlog(t, "\n      rotate_every: 10s", "")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rotate_every")
		assert.Contains(t, err.Error(), "1m0s")
	})
	t.Run("a rotation that is allowed", func(t *testing.T) {
		cfg, err := config.Load(write(t, mariaWithBinlog(t, "\n      rotate_every: 5m", "")))
		require.NoError(t, err)
		assert.Equal(t, 5*time.Minute, cfg.Sources["prod-pg-main"].Binlog.RotateEvery)
	})
	t.Run("a relative spool", func(t *testing.T) {
		body := strings.Replace(mariaWithBinlog(t, "", ""), "spool_dir: /var/lib/koffr/spool", "spool_dir: spool", 1)
		_, err := config.Load(write(t, body))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not absolute")
	})
	t.Run("bounds that flap", func(t *testing.T) {
		_, err := config.Load(write(t, mariaWithBinlog(t, "", "\n  spool_high: 1G\n  spool_low: 1G")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "spool_low")
	})
	t.Run("no spool when nothing archives is fine", func(t *testing.T) {
		_, err := config.Load(write(t, valid))
		require.NoError(t, err, "a configuration with no binlog archiving owes no spool")
	})
}
