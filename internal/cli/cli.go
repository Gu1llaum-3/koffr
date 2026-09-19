// Package cli wires the command surface of koffr. Commands are thin: they parse
// flags, call a use case and render the result.
package cli

import (
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/Gu1llaum-3/koffr/internal/build"
	"github.com/Gu1llaum-3/koffr/internal/config"
)

// NewRoot builds the root command. Callers set the output streams and the
// arguments, which is what makes the surface testable.
func NewRoot() *cobra.Command {
	var releaseLogger func() error

	root := &cobra.Command{
		Use:           build.Name,
		Short:         "Autonomous backup agent for PostgreSQL, MySQL and MariaDB",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			release, err := setUpLogging(cmd)
			if err != nil {
				return err
			}
			releaseLogger = release

			loggerOf(cmd).Info("command started",
				slog.String("command", cmd.Name()),
				slog.String("version", build.Info().Version),
			)

			return nil
		},
		PersistentPostRunE: func(*cobra.Command, []string) error {
			if releaseLogger == nil {
				return nil
			}

			return releaseLogger()
		},
	}
	// --config and --state-dir override the production paths of E-026, which
	// are the defaults. Development happens on a machine where /etc/koffr is
	// not writable (N-11).
	root.PersistentFlags().String("config", config.DefaultConfigFile, "path of koffr.yaml")
	root.PersistentFlags().String("state-dir", config.DefaultStateDir, "directory holding the local state")
	root.PersistentFlags().String("log-dir", config.DefaultLogDir, "directory holding the log file")
	root.PersistentFlags().String("log-level", "info", "debug, info, warn or error")
	root.PersistentFlags().String("tools-dir", config.DefaultPaths().ToolsDir(),
		"directory holding the tools koffr installed")

	root.AddCommand(newVersionCommand(), newConfigCommand(), newToolsCommand())

	return root
}
