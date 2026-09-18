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
	root.AddCommand(newVersionCommand())

	return root
}
