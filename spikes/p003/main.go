// P-003: is streaming age encryption a throughput bottleneck?
//
// Measures each stage of the real pipeline in isolation and then combined, on
// synthetic data whose compressibility matches the reference dataset (60%
// incompressible, 40% repetitive).
//
// The hypothesis under test is the buffering one: age's STREAM construction
// works in 64 KiB chunks, so writes smaller than a chunk could multiply calls
// for nothing. Each encrypting case therefore runs twice, once writing
// directly and once behind a 1 MiB bufio.Writer.
//
// Throwaway probe code.
package main

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"
)

const (
	sourceSize = 128 << 20 // repeating window, larger than any zstd window here
	totalSize  = 4 << 30   // bytes pushed through each case
	bufSize    = 1 << 20
)

// buildSource returns a buffer mixing incompressible and highly compressible
// bytes in the same proportion as the reference dataset.
func buildSource() []byte {
	buf := make([]byte, sourceSize)
	cut := sourceSize * 60 / 100
	if _, err := rand.Read(buf[:cut]); err != nil {
		panic(err)
	}
	pattern := []byte("the quick brown fox jumps over the lazy dog ")
	for i := cut; i < sourceSize; i += len(pattern) {
		copy(buf[i:], pattern)
	}
	return buf
}

// repeatReader streams a fixed buffer until it has produced total bytes.
type repeatReader struct {
	buf  []byte
	off  int
	left int64
}

func (r *repeatReader) Read(p []byte) (int, error) {
	if r.left <= 0 {
		return 0, io.EOF
	}
	if r.off == len(r.buf) {
		r.off = 0
	}
	n := copy(p, r.buf[r.off:])
	if int64(n) > r.left {
		n = int(r.left)
	}
	r.off += n
	r.left -= int64(n)
	return n, nil
}

type result struct {
	name     string
	seconds  float64
	inBytes  int64
	outBytes int64
	peakRSS  uint64
}

// countingWriter is the sink: it measures output volume without storing it.
type countingWriter struct{ n int64 }

func (w *countingWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

func run(name string, src []byte, build func(io.Writer) (io.WriteCloser, error)) result {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	sink := &countingWriter{}
	r := &repeatReader{buf: src, left: totalSize}

	start := time.Now()
	var w io.Writer = sink
	var closers []io.Closer
	if build != nil {
		wc, err := build(sink)
		if err != nil {
			panic(err)
		}
		w = wc
		closers = append(closers, wc)
	}
	if _, err := io.Copy(w, r); err != nil {
		panic(err)
	}
	for i := len(closers) - 1; i >= 0; i-- {
		if err := closers[i].Close(); err != nil {
			panic(err)
		}
	}
	elapsed := time.Since(start).Seconds()

	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	return result{name: name, seconds: elapsed, inBytes: totalSize, outBytes: sink.n, peakRSS: after.Sys - before.Sys}
}

// multiCloser closes a chain outermost-first.
type multiCloser struct {
	io.Writer
	closers []io.Closer
}

func (m multiCloser) Close() error {
	for i := 0; i < len(m.closers); i++ {
		if err := m.closers[i].Close(); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	fmt.Fprintln(os.Stderr, "building source buffer...")
	src := buildSource()

	id, err := age.GenerateX25519Identity()
	if err != nil {
		panic(err)
	}
	rec := id.Recipient()
	// Two recipients, as the real configuration always has (EF-051).
	id2, err := age.GenerateX25519Identity()
	if err != nil {
		panic(err)
	}
	rec2 := id2.Recipient()

	zenc := func(level zstd.EncoderLevel) func(io.Writer) (io.WriteCloser, error) {
		return func(w io.Writer) (io.WriteCloser, error) {
			return zstd.NewWriter(w, zstd.WithEncoderLevel(level))
		}
	}
	agenc := func(w io.Writer) (io.WriteCloser, error) {
		return age.Encrypt(w, rec, rec2)
	}
	agencBuf := func(w io.Writer) (io.WriteCloser, error) {
		enc, err := age.Encrypt(w, rec, rec2)
		if err != nil {
			return nil, err
		}
		return enc, nil
	}
	_ = agencBuf

	cases := []struct {
		name  string
		build func(io.Writer) (io.WriteCloser, error)
	}{
		{"raw copy (ceiling)", nil},
		{"zstd level 1", zenc(zstd.SpeedFastest)},
		{"zstd level 3", zenc(zstd.SpeedDefault)},
		{"zstd level 9", zenc(zstd.SpeedBetterCompression)},
		{"age only", agenc},
		{"age only + 1MiB bufio", func(w io.Writer) (io.WriteCloser, error) {
			enc, err := age.Encrypt(w, rec, rec2)
			if err != nil {
				return nil, err
			}
			bw := newBufWriter(enc, bufSize)
			return multiCloser{Writer: bw, closers: []io.Closer{bw, enc}}, nil
		}},
		{"zstd3 -> age (real chain)", func(w io.Writer) (io.WriteCloser, error) {
			enc, err := age.Encrypt(w, rec, rec2)
			if err != nil {
				return nil, err
			}
			z, err := zstd.NewWriter(enc, zstd.WithEncoderLevel(zstd.SpeedDefault))
			if err != nil {
				return nil, err
			}
			return multiCloser{Writer: z, closers: []io.Closer{z, enc}}, nil
		}},
		{"zstd3 -> 1MiB bufio -> age", func(w io.Writer) (io.WriteCloser, error) {
			enc, err := age.Encrypt(w, rec, rec2)
			if err != nil {
				return nil, err
			}
			bw := newBufWriter(enc, bufSize)
			z, err := zstd.NewWriter(bw, zstd.WithEncoderLevel(zstd.SpeedDefault))
			if err != nil {
				return nil, err
			}
			return multiCloser{Writer: z, closers: []io.Closer{z, bw, enc}}, nil
		}},
	}

	fmt.Printf("%-28s %10s %12s %10s %10s\n", "case", "MiB/s", "out/in", "seconds", "GiB in")
	for _, c := range cases {
		r := run(c.name, src, c.build)
		mibs := float64(r.inBytes) / r.seconds / 1048576
		ratio := float64(r.outBytes) / float64(r.inBytes)
		fmt.Printf("%-28s %10.0f %12.3f %10.1f %10.1f\n",
			r.name, mibs, ratio, r.seconds, float64(r.inBytes)/(1<<30))
	}
}
