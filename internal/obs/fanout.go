package obs

import (
	"context"
	"errors"
	"log/slog"
)

// fanout hands one event to several handlers, each with its own level. The
// standard library has no such handler, and ADR-0012 needs one: the file keeps
// everything, while the console of a command keeps what a human is waiting for.
type fanout struct {
	handlers []slog.Handler
}

func (f fanout) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range f.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}

	return false
}

func (f fanout) Handle(ctx context.Context, record slog.Record) error {
	var failures []error

	for _, handler := range f.handlers {
		if !handler.Enabled(ctx, record.Level) {
			continue
		}
		// Each handler gets its own copy: a handler is free to consume the
		// attributes of the record it is given.
		if err := handler.Handle(ctx, record.Clone()); err != nil {
			failures = append(failures, err)
		}
	}

	return errors.Join(failures...)
}

func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	return f.derive(func(handler slog.Handler) slog.Handler { return handler.WithAttrs(attrs) })
}

func (f fanout) WithGroup(name string) slog.Handler {
	return f.derive(func(handler slog.Handler) slog.Handler { return handler.WithGroup(name) })
}

func (f fanout) derive(with func(slog.Handler) slog.Handler) slog.Handler {
	derived := make([]slog.Handler, 0, len(f.handlers))
	for _, handler := range f.handlers {
		derived = append(derived, with(handler))
	}

	return fanout{handlers: derived}
}
