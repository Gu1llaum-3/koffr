package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/Gu1llaum-3/koffr/internal/build"
)

func newVersionCommand() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version of this binary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := build.Info()

			if !asJSON {
				say(cmd, "%v\n", info)
				return nil
			}

			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")

			return encoder.Encode(info)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the version as a JSON object")

	return cmd
}
