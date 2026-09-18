// Package cli wires the command surface of koffr. Commands are thin: they parse
// flags, call a use case and render the result.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/Gu1llaum-3/koffr/internal/build"
)

// NewRoot builds the root command. Callers set the output streams and the
// arguments, which is what makes the surface testable.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           build.Name,
		Short:         "Autonomous backup agent for PostgreSQL, MySQL and MariaDB",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// --config and --state-dir override the production paths of E-026, which
	// are the defaults. Development happens on a machine where /etc/koffr is
	// not writable (N-11).
	root.PersistentFlags().String("config", defaultConfigPath, "path of koffr.yaml")
	root.PersistentFlags().String("state-dir", defaultStateDir, "directory holding the local state")

	root.AddCommand(newVersionCommand(), newConfigCommand())

	return root
}
