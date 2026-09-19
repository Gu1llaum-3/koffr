package config

import "fmt"

// checkTools refuses a tools section koffr could not act on. § 5.2 F2.9 makes
// the exec strategy a per-database decision that hands koffr the Docker socket:
// an operator who asks for it without saying which container has written
// something koffr cannot do, and finding that out at two in the morning is the
// failure this check exists to prevent (E-046, RSV-10).
func (c *Config) checkTools() error {
	for i := range c.Databases {
		database := &c.Databases[i]

		switch {
		case database.Tools.Auto, database.Tools.Strategy == "":
			continue

		case database.Tools.Strategy != ExecStrategy:
			return fmt.Errorf("databases[%d] %s: tools.strategy %q is not known; the only strategy is %q",
				i, database.ID, database.Tools.Strategy, ExecStrategy)

		case database.Tools.Container == "":
			return fmt.Errorf("databases[%d] %s: tools.strategy %q needs the container it should dump through; add tools.container",
				i, database.ID, ExecStrategy)
		}
	}

	return nil
}
