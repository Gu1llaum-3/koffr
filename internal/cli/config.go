package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
	"github.com/Gu1llaum-3/koffr/internal/engine"
)

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect the configuration file",
	}
	cmd.AddCommand(newConfigValidateCommand(), newConfigShowCommand())

	return cmd
}

func newConfigValidateCommand() *cobra.Command {
	var (
		offline    bool
		searchPath []string
	)

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Check the configuration file and say what is wrong with it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := configPath(cmd)

			parsed, err := parse(path)
			if err != nil {
				return err
			}

			// Resolving reads a file and an environment variable, and nothing
			// else: a password koffr cannot read is found here rather than in
			// the middle of the night (CFG-09). Reaching the databases and the
			// tools is E-034, at lot 1.
			if !offline {
				if err := parsed.Resolve(); err != nil {
					return fmt.Errorf("invalid configuration: %s: %w", path, err)
				}
			}

			// E-034 — the shape is not the whole question: a configuration
			// that names a database nothing answers, or a tool nobody has, is
			// coherent and useless. § 5.1 F1.3 asks to check both, and to do
			// nothing else: no dump, no write, no schema read.
			if !offline {
				if err := reachEverything(cmd, parsed, searchPath); err != nil {
					return err
				}
			}

			cmd.Printf("ok %s: %d databases, %d destinations, %d alert channels, timezone %s%s\n",
				path,
				len(parsed.Databases),
				len(parsed.Destinations),
				len(parsed.Alerts.Channels),
				parsed.Agent.Timezone,
				offlineNote(offline),
			)

			return nil
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false,
		"check the shape only, without reading the secrets or reaching the fleet")
	cmd.Flags().StringSliceVar(&searchPath, "search-path", nil,
		"directories to look for tools in instead of the ones koffr knows about")

	return cmd
}

// reachEverything reports the first database that cannot be backed up right
// now. validate answers "is this configuration usable?", so one failure is
// enough; doctor is the command that walks the whole fleet.
func reachEverything(cmd *cobra.Command, parsed *config.Config, searchPath []string) error {
	targets, err := targetsOf(parsed, "")
	if err != nil {
		return err
	}

	for _, diagnosis := range resolve.Diagnose(cmd.Context(), engine.New(), finderFor(cmd, searchPath), targets) {
		switch {
		case diagnosis.Unreachable != nil:
			return fmt.Errorf("database %s: %w", diagnosis.ID, diagnosis.Unreachable)

		case diagnosis.NoTool != nil:
			return fmt.Errorf("database %s: %w", diagnosis.ID, diagnosis.NoTool)
		}
	}

	return nil
}

func offlineNote(offline bool) string {
	if offline {
		return " (offline: the secrets were not read)"
	}

	return ""
}

func newConfigShowCommand() *cobra.Command {
	var redact bool

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the configuration with every secret replaced by a marker",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// There is no code path that prints a secret. The flag is here so
			// that the command reads as it should, and refusing to turn it off
			// is what E-115 asks for (N-15).
			if !redact {
				return errors.New("koffr never prints secrets: --redact cannot be turned off")
			}

			parsed, err := parse(configPath(cmd))
			if err != nil {
				return err
			}

			shown, err := parsed.Redacted()
			if err != nil {
				return err //nolint:wrapcheck // already says what failed
			}
			cmd.Print(shown)

			return nil
		},
	}
	cmd.Flags().BoolVar(&redact, "redact", true, "replace every secret by a marker; cannot be turned off")

	return cmd
}

// parse reads the file and checks its shape. It resolves no secret: reading the
// topology of a fleet must not require the passwords of that fleet (E-115), and
// checking a configuration on a laptop must not need the production files.
func parse(path string) (*config.Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the configuration: %w", err)
	}

	parsed, err := config.Parse(raw, path)
	if err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return parsed, nil
}

func configPath(cmd *cobra.Command) string {
	path, err := cmd.Flags().GetString("config")
	if err != nil || path == "" {
		return config.DefaultConfigFile
	}

	return path
}
