package cli

import (
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
	"github.com/Gu1llaum-3/koffr/internal/engine"
)

func newToolsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "Inspect the dump and restore tools koffr can reach",
		// A command with subcommands and no RunE prints its help and succeeds,
		// even for a subcommand it does not have. `koffr tools install` would
		// then look like it worked.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("koffr tools has no %q subcommand: koffr installs no tool, "+
					"the dump and restore clients are a prerequisite (see the README)", args[0])
			}

			return cmd.Help() //nolint:wrapcheck // cobra's own error
		},
	}
	cmd.AddCommand(newToolsListCommand())

	return cmd
}

func newToolsListCommand() *cobra.Command {
	var searchPath []string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every tool koffr found, with its version and where it came from",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			options := engine.FinderOptions{ManagedDir: toolsDir(cmd)}
			if len(searchPath) > 0 {
				// "instead of", as the flag says: an operator who names the
				// directories wants to see those, not whatever their shell
				// happens to have on PATH.
				options.SystemPaths = searchPath
				options.LookPath = engine.NoPath
			}

			finder := engine.NewFinder(options)

			found, err := everyCandidate(cmd, finder)
			if err != nil {
				return err
			}

			if len(found) == 0 {
				cmd.Println("no tool found. koffr looked on the host, on PATH and in " + toolsDir(cmd))

				return nil
			}

			render(cmd, found)

			return nil
		},
	}
	cmd.Flags().StringSliceVar(&searchPath, "search-path", nil,
		"directories to look in instead of the ones koffr knows about")

	return cmd
}

// everyCandidate enumerates the tools of every family and every operation.
// `tools list` shows what discovery sees, unfiltered: choosing is the
// resolver's job, and seeing what it chose from is the point of the command.
func everyCandidate(cmd *cobra.Command, finder resolve.ToolFinder) ([]resolve.Candidate, error) {
	var found []resolve.Candidate

	for _, family := range []resolve.Family{resolve.PostgreSQL, resolve.MySQL, resolve.MariaDB} {
		for _, tool := range []resolve.Tool{resolve.Dump, resolve.Restore} {
			candidates, err := finder.Find(cmd.Context(), family, tool)
			if err != nil {
				return nil, fmt.Errorf("look for %s %s tools: %w", family, tool, err)
			}
			found = append(found, candidates...)
		}
	}

	// Sorted so that two runs can be compared: an operator reads this twice,
	// before and after installing something.
	slices.SortFunc(found, func(a, b resolve.Candidate) int {
		return strings.Compare(sortKey(a), sortKey(b))
	})

	return found, nil
}

func sortKey(c resolve.Candidate) string {
	return fmt.Sprintf("%s|%s|%03d.%03d|%s", c.Family, c.Tool, c.Version.Major, c.Version.Minor, c.Path)
}

func render(cmd *cobra.Command, found []resolve.Candidate) {
	table := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)

	_, _ = fmt.Fprintln(table, "ENGINE\tTOOL\tVERSION\tSOURCE\tPATH")
	for _, candidate := range found {
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n",
			candidate.Family, candidate.Tool, candidate.Version, candidate.Source, candidate.Path)
	}

	_ = table.Flush()
}

// toolsDir is where managed tools live. E-026 fixes it; --tools-dir moves it,
// which is what a test and a machine without /var/lib need.
func toolsDir(cmd *cobra.Command) string {
	if dir, err := cmd.Flags().GetString("tools-dir"); err == nil && dir != "" {
		return dir
	}

	return config.DefaultPaths().ToolsDir()
}
