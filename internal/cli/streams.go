package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// say writes a **result**: what the caller asked for, on the standard output,
// where a pipe or a redirection will find it (ADR-0012).
//
// It exists because cobra's cmd.Print reads as "print" and means "print to the
// **error** stream": Command.Print writes to OutOrStderr(), which falls back to
// os.Stderr whenever no writer was set — never in a test, always in the binary.
// `koffr config show > copie.yaml` produced an empty file for two lots because
// of it (A-12), and streams_test.go now refuses cmd.Print* in this package.
func say(cmd *cobra.Command, format string, args ...any) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), format, args...)
}

// warn writes what the caller did not ask for but has to know: an escrow
// warning, a degraded mode. On the error stream, so that a redirected result
// stays clean.
func warn(cmd *cobra.Command, format string, args ...any) {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), format, args...)
}

// moment is how koffr writes an instant on screen: in the zone the operator
// declared, with its offset, and **in one format everywhere**.
//
// ADR-0006 keeps one representation in the base — UTC — and converts at the
// display. `A-23`: everything was rendered in UTC although `agent.timezone` was
// declared, and `koffr list` and `koffr verify` disagreed on the shape besides.
func moment(at time.Time, in *time.Location) string {
	if at.IsZero() {
		return ""
	}

	if in == nil {
		in = time.UTC
	}

	return at.In(in).Format("2006-01-02 15:04 -07:00")
}
