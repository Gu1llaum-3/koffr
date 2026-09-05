package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"syscall"
	"text/tabwriter"
)

// printer renders text output and remembers the first write that failed.
//
// It exists because checking every Fprintf at the call site turns rendering
// code into noise, and ignoring them all is how `koffr ls | head` becomes a
// command that silently reports success on a broken pipe. The error is latched
// and reported once, by whoever finishes the rendering.
type printer struct {
	w   io.Writer
	err error
}

func newPrinter(w io.Writer) *printer { return &printer{w: w} }

func (p *printer) printf(format string, args ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, format, args...)
}

// table renders aligned columns and folds any flush error into the same latch.
func (p *printer) table(rows func(*printer)) {
	if p.err != nil {
		return
	}
	tw := tabwriter.NewWriter(p.w, 0, 0, 2, ' ', 0)
	inner := &printer{w: tw}
	rows(inner)
	if err := tw.Flush(); inner.err == nil {
		inner.err = err
	}
	p.err = inner.err
}

// Err reports the first failed write, if any.
//
// A closed pipe is not an error worth a message: `koffr ls | head -5` does
// exactly that, on purpose, and complaining about it would make the tool
// annoying to use in the one place it is most used.
func (p *printer) Err() error {
	if p.err == nil || errors.Is(p.err, fs.ErrClosed) || errors.Is(p.err, syscall.EPIPE) {
		return nil
	}
	return p.err
}
