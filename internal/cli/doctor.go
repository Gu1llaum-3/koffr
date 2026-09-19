package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
	"github.com/Gu1llaum-3/koffr/internal/engine"
)

// errFleetInTrouble — at least one database could not be backed up right now.
// doctor is run before an incident, and from a script: its exit code has to
// tell the two cases apart.
var errFleetInTrouble = errors.New("at least one database is not ready to be backed up")

func newDoctorCommand() *cobra.Command {
	var (
		only       string
		searchPath []string
	)

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Report, for each database, whether it answers and which tool would dump it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			loaded, err := loadResolved(cmd)
			if err != nil {
				return err
			}

			targets, err := targetsOf(loaded, only)
			if err != nil {
				return err
			}

			// In the order of the configuration: an operator reads this next
			// to the file they wrote.
			diagnoses := resolve.Diagnose(cmd.Context(), engine.New(), finderFor(cmd, searchPath), targets)

			renderDiagnoses(cmd, diagnoses)

			for _, diagnosis := range diagnoses {
				if !diagnosis.Healthy() {
					return errFleetInTrouble
				}
			}

			return nil
		},
	}
	cmd.Flags().StringVar(&only, "database", "", "report on this database alone")
	cmd.Flags().StringSliceVar(&searchPath, "search-path", nil,
		"directories to look for tools in instead of the ones koffr knows about")

	return cmd
}

// targetsOf turns the configuration into what the domain reasons about. An
// identifier nobody declared names the ones that exist: a typo is the likeliest
// reason to be here.
func targetsOf(loaded *config.Config, only string) ([]resolve.Subject, error) {
	var targets []resolve.Subject

	known := make([]string, 0, len(loaded.Databases))

	for _, database := range loaded.Databases {
		known = append(known, database.ID)

		if only != "" && database.ID != only {
			continue
		}

		targets = append(targets, resolve.Subject{
			ID: database.ID,
			Target: resolve.Target{
				Engine:   resolve.Family(database.Engine),
				Host:     database.Host,
				Port:     database.Port,
				Database: database.Database,
				User:     database.User,
				Password: database.Password.Expose(),
			},
		})
	}

	if only != "" && len(targets) == 0 {
		slices.Sort(known)

		return nil, fmt.Errorf("no database is called %q; the configuration declares: %s",
			only, strings.Join(known, ", "))
	}

	return targets, nil
}

func renderDiagnoses(cmd *cobra.Command, diagnoses []resolve.Diagnosis) {
	table := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)

	_, _ = fmt.Fprintln(table, "DATABASE\tREACHABLE\tSERVER\tTOOL\tVERSION\tSOURCE")
	for _, diagnosis := range diagnoses {
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n",
			diagnosis.ID,
			reachability(diagnosis),
			serverOf(diagnosis),
			toolOf(diagnosis),
			versionOf(diagnosis),
			sourceOf(diagnosis),
		)
	}
	_ = table.Flush()

	// The table says what is wrong; underneath, koffr says what to do about it.
	for _, diagnosis := range diagnoses {
		switch {
		case diagnosis.Unreachable != nil:
			cmd.Printf("\n%s: %v\n", diagnosis.ID, diagnosis.Unreachable)

		case diagnosis.NoTool != nil:
			cmd.Printf("\n%s: %v\n", diagnosis.ID, diagnosis.NoTool)
		}
	}
}

func reachability(d resolve.Diagnosis) string {
	if d.Unreachable != nil {
		return "unreachable"
	}

	return "yes"
}

func serverOf(d resolve.Diagnosis) string {
	if d.Unreachable != nil {
		return "-"
	}

	return fmt.Sprintf("%s %s", d.Server.Family, d.Server.Version)
}

func toolOf(d resolve.Diagnosis) string {
	switch {
	case d.Unreachable != nil:
		return "-"
	case d.NoTool != nil:
		return "none"
	default:
		return d.Tool.Path
	}
}

func versionOf(d resolve.Diagnosis) string {
	if d.Unreachable != nil || d.NoTool != nil {
		return "-"
	}

	return d.Tool.Version.String()
}

func sourceOf(d resolve.Diagnosis) string {
	if d.Unreachable != nil || d.NoTool != nil {
		return "-"
	}

	return string(d.Tool.Source)
}

// finderFor builds tool discovery for a command. --search-path means "instead
// of", so naming directories also means not consulting PATH.
func finderFor(cmd *cobra.Command, searchPath []string) resolve.ToolFinder {
	options := engine.FinderOptions{ManagedDir: toolsDir(cmd)}
	if len(searchPath) > 0 {
		options.SystemPaths = searchPath
		options.LookPath = engine.NoPath
	}

	return engine.NewFinder(options)
}

// loadResolved reads the configuration and resolves its secrets: doctor is
// about to connect, so a password it cannot read is a problem to report now.
func loadResolved(cmd *cobra.Command) (*config.Config, error) {
	path := configPath(cmd)

	loaded, err := config.Load(path)
	if err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return loaded, nil
}
