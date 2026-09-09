package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Gu1llaum-3/koffr/internal/config"
)

// EF-067: retention is automatic and on by default. The one field decides how:
// empty runs it after each backup, a cron pins a fixed cadence, off disables.
func TestSchedulerPruneModes(t *testing.T) {
	cases := []struct {
		prune       string
		afterBackup bool
		cadence     string
		disabled    bool
	}{
		{"", true, "", false}, // default: after each backup
		{"@daily", false, "@daily", false},
		{"0 3 * * *", false, "0 3 * * *", false},
		{"off", false, "", true},
		{"never", false, "", true},
		{"none", false, "", true},
		{"  Off  ", false, "", true}, // trimmed and case-insensitive
	}
	for _, c := range cases {
		s := config.Scheduler{Prune: c.prune}
		assert.Equal(t, c.afterBackup, s.PruneAfterBackup(), "PruneAfterBackup for %q", c.prune)
		assert.Equal(t, c.cadence, s.PruneCadence(), "PruneCadence for %q", c.prune)
		assert.Equal(t, c.disabled, s.PruneDisabled(), "PruneDisabled for %q", c.prune)
	}
}

// The three modes are mutually exclusive: exactly one applies at a time.
func TestSchedulerPruneModesAreExclusive(t *testing.T) {
	for _, prune := range []string{"", "@daily", "off"} {
		s := config.Scheduler{Prune: prune}
		n := 0
		if s.PruneAfterBackup() {
			n++
		}
		if s.PruneCadence() != "" {
			n++
		}
		if s.PruneDisabled() {
			n++
		}
		assert.Equal(t, 1, n, "exactly one mode for %q", prune)
	}
}

// A disabling word must never be parsed as a cadence, which would fail
// validation with a confusing "not a schedule" for a deliberate choice.
func TestSchedulerOffIsNotACadence(t *testing.T) {
	assert.Empty(t, config.Scheduler{Prune: "off"}.PruneCadence())
}

// EF-066: maintenance is on by default (daily when unset), never deletes a
// backup, and can be turned off.
func TestSchedulerMaintenanceModes(t *testing.T) {
	cases := []struct {
		maintenance string
		cadence     string
		disabled    bool
	}{
		{"", "@daily", false}, // default on, daily
		{"@hourly", "@hourly", false},
		{"0 4 * * *", "0 4 * * *", false},
		{"off", "", true},
		{"never", "", true},
		{" OFF ", "", true},
	}
	for _, c := range cases {
		s := config.Scheduler{Maintenance: c.maintenance}
		assert.Equal(t, c.cadence, s.MaintenanceCadence(), "MaintenanceCadence for %q", c.maintenance)
		assert.Equal(t, c.disabled, s.MaintenanceDisabled(), "MaintenanceDisabled for %q", c.maintenance)
	}
}
