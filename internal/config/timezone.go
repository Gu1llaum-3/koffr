package config

import (
	"fmt"
	"time"

	// The zone database travels inside the binary. koffr ships as a static
	// binary and runs on hosts that carry no zoneinfo — a scratch container,
	// a minimal image. Without this, time.LoadLocation would fail there and
	// E-036 would be a promise the product cannot keep (N-6).
	_ "time/tzdata"
)

// Location is the zone schedules are read in. It is the one the operator
// declared, never the one the machine happens to be set to (E-036).
func (c *Config) Location() *time.Location {
	return c.location
}

// resolveTimezone turns agent.timezone into a location, and refuses a document
// that does not carry one.
func (c *Config) resolveTimezone() error {
	if c.Agent.Timezone == "" {
		return fmt.Errorf("agent.timezone is required: koffr reads schedules in a zone you declare, never in the one of the host")
	}

	location, err := time.LoadLocation(c.Agent.Timezone)
	if err != nil {
		return fmt.Errorf("agent.timezone %q is not a known zone: %w", c.Agent.Timezone, err)
	}
	c.location = location

	return nil
}
