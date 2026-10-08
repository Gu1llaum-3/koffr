package cli

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Gu1llaum-3/koffr/internal/domain/catalog"
)

func newListCommand() *cobra.Command {
	var destination string

	cmd := &cobra.Command{
		Use:   "list [<database>]",
		Short: "List the archives of the catalogue, most recent first",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			book, closer, err := catalogFor(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = closer() }()

			filter := catalog.Filter{Destination: destination}
			if len(args) == 1 {
				filter.Database = args[0]
			}

			lister, in, err := listerFor(cmd, book)
			if err != nil {
				return err
			}

			found, err := lister.List(cmd.Context(), filter)
			if err != nil {
				return fmt.Errorf("list the archives: %w", err)
			}

			renderArchives(cmd, found, in)

			return nil
		},
	}
	cmd.Flags().StringVar(&destination, "destination", "", "list only the archives held by this destination")

	return cmd
}

// verificationLabel is how E-064 is met: the states read differently, and none
// of them reads like a success it is not.
//
// « Absence de vérification n'est jamais assimilée à un succès » — so an
// archive nobody looked at says **no**, in as many letters, rather than leaving
// a blank that a tired eye reads as fine. An archive koffr has no record of at
// all says that, which is a third thing again (`A-19`).
func verificationLabel(line catalog.Listed) string {
	if !line.Catalogued {
		return "not in the catalogue"
	}

	switch line.Verified {
	case catalog.Structure:
		return "yes, in full"

	case catalog.Checksum:
		return "yes, checksum only"

	case catalog.Failed:
		return "FAILED"

	default:
		return "no — never checked"
	}
}

func renderArchives(cmd *cobra.Command, found []catalog.Listed, in *time.Location) {
	if len(found) == 0 {
		say(cmd, "no archive, neither in the catalogue nor on the destinations\n")

		return
	}

	table := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)

	_, _ = fmt.Fprintln(table, "ARCHIVE\tDATABASE\tTAKEN\tSTORED\tVERIFIED\tWHERE")

	for _, archive := range found {
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n",
			archive.ID,
			archive.Database,
			moment(archive.StartedAt, in),
			humanBytes(archive.StoredBytes),
			verificationLabel(archive),
			destinationsOfArchive(archive),
		)
	}

	_ = table.Flush()
}

// destinationsOfArchive names where the copies are — and says when the
// destination no longer holds what the catalogue says it does. A file that
// disappears quietly is the failure retention exists to avoid.
func destinationsOfArchive(archive catalog.Listed) string {
	if len(archive.Locations) == 0 {
		return "nowhere"
	}

	names := make([]string, 0, len(archive.Locations))
	for _, location := range archive.Locations {
		names = append(names, location.Destination)
	}

	joined := joinWith(names, ", ")
	if archive.Catalogued && !archive.Present {
		joined += " (gone)"
	}

	return joined
}

// humanBytes is what an operator reads at a glance. Binary units, because a
// disk is measured that way and a backup lives on a disk.
func humanBytes(count int64) string {
	const unit = 1024

	if count < unit {
		return fmt.Sprintf("%d B", count)
	}

	value, exponent := float64(count), 0
	for value >= unit && exponent < 4 {
		value /= unit
		exponent++
	}

	return fmt.Sprintf("%.1f %ciB", value, "KMGT"[exponent-1])
}

func joinWith(values []string, separator string) string {
	joined := ""

	for index, value := range values {
		if index > 0 {
			joined += separator
		}

		joined += value
	}

	return joined
}
