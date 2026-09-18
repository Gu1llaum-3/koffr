// Package obs sets up what koffr says about itself: structured logs on
// standard output, and the same lines in a file it rotates on its own.
package obs

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Logger is what the rest of koffr logs through.
type Logger = slog.Logger

// Options configures the logger. The zero value is usable: JSON at info level
// on the writer it is given, no file.
type Options struct {
	// Level is the lowest level written. The zero value is slog.LevelInfo.
	Level slog.Level

	// File, when set, receives the same lines as the writer, and is rotated by
	// koffr itself — E-119 forbids depending on logrotate, which would be a
	// service koffr claims not to need (N-5).
	File string

	// MaxSizeMB is the size a log file reaches before it is rotated.
	MaxSizeMB int

	// MaxBackups is how many rotated files are kept.
	MaxBackups int

	// MaxAgeDays is how long a rotated file is kept. Zero keeps them until
	// MaxBackups pushes them out.
	MaxAgeDays int
}

// Default rotation, used when Options leaves it at zero.
const (
	defaultMaxSizeMB  = 50
	defaultMaxBackups = 7
)

// New builds the logger and returns the function that releases what it holds.
// out is standard output in production; E-121 wants the JSON there, so that
// journald picks it up with no configuration at all, and the same lines in a
// file for the machines where nobody reads journald.
func New(out io.Writer, options Options) (*Logger, func() error) {
	writer := out
	release := func() error { return nil }

	if options.File != "" {
		file := rotatingFile(options)
		writer = io.MultiWriter(out, file)
		release = file.Close
	}

	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level:       options.Level,
		ReplaceAttr: redactSensitive,
	})

	return slog.New(handler), release
}

// rotatingFile is the log file and the rotation koffr performs itself. The
// directory is created here: a first start on a bare machine must not fail
// because nobody made /var/log/koffr.
func rotatingFile(options Options) *lumberjack.Logger {
	// An error here is not worth failing a start-up for: lumberjack creates the
	// directory again on its first write, and reports the real problem then.
	_ = os.MkdirAll(filepath.Dir(options.File), 0o750)

	maxSize := options.MaxSizeMB
	if maxSize <= 0 {
		maxSize = defaultMaxSizeMB
	}

	maxBackups := options.MaxBackups
	if maxBackups <= 0 {
		maxBackups = defaultMaxBackups
	}

	return &lumberjack.Logger{
		Filename:   options.File,
		MaxSize:    maxSize,
		MaxBackups: maxBackups,
		MaxAge:     options.MaxAgeDays,
		Compress:   true,
	}
}
