package main

import (
	"bufio"
	"io"
)

// bufWriter is a bufio.Writer that also satisfies io.Closer, so it can sit in
// a closer chain and flush at the right moment.
type bufWriter struct{ *bufio.Writer }

func newBufWriter(w io.Writer, size int) bufWriter {
	return bufWriter{bufio.NewWriterSize(w, size)}
}

func (b bufWriter) Close() error { return b.Flush() }
