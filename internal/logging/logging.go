// Package logging builds the logger (EF-136).
//
// slog from the standard library, because a backup tool that must be a single
// static binary has no business taking a dependency for something the standard
// library does. The only thing written here is the file half: a rotating writer
// small enough to read in one sitting, which matters because a daemon that
// fills a disk with its own logs takes the backups down with it.
package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config is what an operator can set.
type Config struct {
	// Format is "text" or "json". Empty means text, because the first person to
	// run Koffr is a person.
	Format string

	// Level is debug, info, warn or error. Empty means info.
	Level string

	// Path is the log file. Empty means no file, which is the right default for
	// a command run by hand.
	Path string

	// MaxSizeBytes is when the file rotates. Zero means 10 MiB.
	MaxSizeBytes int64

	// MaxFiles is how many are kept, the live one included. Zero means 5.
	MaxFiles int

	// Writer is the stream half. Nil means standard error.
	//
	// Standard error and not standard output, despite EF-136 naming stdout:
	// stdout carries a command's answer, and `koffr ls --output json | jq`
	// must not have log lines mixed into the document it is parsing. systemd
	// and every container runtime capture the two identically, so nothing is
	// lost by the choice.
	Writer io.Writer
}

// New returns the logger and a function that closes the file behind it.
func New(cfg Config) (*slog.Logger, func() error, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, nil, err
	}

	writers := []io.Writer{}
	if cfg.Writer != nil {
		writers = append(writers, cfg.Writer)
	} else if cfg.Path == "" {
		writers = append(writers, os.Stderr)
	}

	closeFile := func() error { return nil }
	if cfg.Path != "" {
		f, err := newRotator(cfg.Path, cfg.MaxSizeBytes, cfg.MaxFiles)
		if err != nil {
			return nil, nil, err
		}
		writers = append(writers, f)
		closeFile = f.Close
	}

	out := writers[0]
	if len(writers) > 1 {
		out = io.MultiWriter(writers...)
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch strings.ToLower(cfg.Format) {
	case "", "text":
		handler = slog.NewTextHandler(out, opts)
	case "json":
		handler = slog.NewJSONHandler(out, opts)
	default:
		return nil, nil, fmt.Errorf(
			"logging: %q is not a log format; use \"text\" or \"json\"", cfg.Format)
	}
	return slog.New(handler), closeFile, nil
}

func parseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(name) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf(
			"logging: %q is not a level; use debug, info, warn or error", name)
	}
}

// ValidateConfig reports whether a configuration would build, without building
// it. The configuration loader uses it so a bad level is found while someone is
// still looking at the file rather than when the daemon starts (PD-006).
func ValidateConfig(cfg Config) error {
	if _, err := parseLevel(cfg.Level); err != nil {
		return err
	}
	switch strings.ToLower(cfg.Format) {
	case "", "text", "json":
		return nil
	default:
		return fmt.Errorf("logging: %q is not a log format; use \"text\" or \"json\"", cfg.Format)
	}
}

var errNoPath = errors.New("logging: no log file path")
