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
	// Level is the lowest level the file keeps. The zero value is
	// slog.LevelInfo. The file keeps everything koffr does, including the
	// trace of a command somebody ran by hand (ADR-0012).
	Level slog.Level

	// ConsoleLevel is the lowest level the writer given to New receives. The
	// zero value is slog.LevelInfo, which is what a daemon wants; a command
	// typed by a human asks for slog.LevelWarn, so that its output is its
	// answer and nothing else (ADR-0012).
	ConsoleLevel slog.Level

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
	release := func() error { return nil }

	handlers := []slog.Handler{jsonHandler(out, options.ConsoleLevel)}

	if options.File != "" {
		file := rotatingFile(options)
		handlers = append(handlers, jsonHandler(file, options.Level))
		release = file.Close
	}

	return slog.New(fanout{handlers: handlers}), release
}

// jsonHandler builds one destination. Masking lives here rather than in a
// wrapper, so that it applies to every destination and cannot be lost by
// adding a third one (E-115).
func jsonHandler(out io.Writer, level slog.Level) slog.Handler {
	return slog.NewJSONHandler(out, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: redactSensitive,
	})
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
