package cli

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/obs"
)

// loggerKey carries the logger from the root command to the one that runs.
type loggerKey struct{}

// setUpLogging builds the logger for this run and hands it to the command
// through the context. It runs before every command, and it never stops one:
// a log file koffr cannot open is worth a line on standard error, not a
// failure of `koffr version` (N-3).
func setUpLogging(cmd *cobra.Command) (func() error, error) {
	level, err := logLevel(cmd)
	if err != nil {
		return nil, err
	}

	paths := config.DefaultPaths()
	if dir, dirErr := cmd.Flags().GetString("log-dir"); dirErr == nil && dir != "" {
		paths.LogDir = dir
	}

	logger, release := obs.New(cmd.ErrOrStderr(), obs.Options{
		Level: level,
		File:  paths.LogFile(),
	})

	cmd.SetContext(context.WithValue(cmd.Context(), loggerKey{}, logger))

	return release, nil
}

// loggerOf returns the logger of this run. A command that runs outside the root
// still gets a working logger rather than a nil pointer.
func loggerOf(cmd *cobra.Command) *slog.Logger {
	if logger, ok := cmd.Context().Value(loggerKey{}).(*slog.Logger); ok {
		return logger
	}

	return slog.New(slog.DiscardHandler)
}

// logLevel reads --log-level. An unknown value is refused: a typo must not
// silence an agent whose whole job is to say when a backup did not happen.
func logLevel(cmd *cobra.Command) (slog.Level, error) {
	name, err := cmd.Flags().GetString("log-level")
	if err != nil || name == "" {
		return slog.LevelInfo, nil //nolint:nilerr // the flag is optional
	}

	var level slog.Level
	if unmarshalErr := level.UnmarshalText([]byte(strings.ToLower(name))); unmarshalErr != nil {
		return 0, fmt.Errorf("unknown log level %q: use debug, info, warn or error", name)
	}

	return level, nil
}
