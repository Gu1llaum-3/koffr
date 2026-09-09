package binlog

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func n(t *testing.T, s string) Name {
	t.Helper()
	v, err := Parse(s)
	require.NoError(t, err)
	return v
}

func TestParse(t *testing.T) {
	v := n(t, "mariadb-bin.000042")
	assert.Equal(t, "mariadb-bin", v.Base)
	assert.Equal(t, uint64(42), v.Seq)
	assert.Equal(t, "mariadb-bin.000042", v.String(), "spelled the way the server spells it")
	assert.Equal(t, "mariadb-bin.000043", v.Next().String())

	// An operator can name the log anything, dots included.
	dotted := n(t, "db01.prod.bin.000007")
	assert.Equal(t, "db01.prod.bin", dotted.Base)

	// Wider numbers happen after a million rotations; the width is kept.
	wide := n(t, "binlog.1000000")
	assert.Equal(t, "binlog.1000001", wide.Next().String())

	for _, bad := range []string{"", "nodot", "bin.", ".000001", "bin.abc", "bin.42", "binlog.index"} {
		_, err := Parse(bad)
		assert.Error(t, err, "%q is not a binary log name", bad)
	}
}

// Derived from the names, never stored: a stored "chain is intact" can be
// wrong, and a hole between 000041 and 000043 cannot.
func TestGaps(t *testing.T) {
	gaps, err := Gaps([]Name{n(t, "b.000040"), n(t, "b.000041"), n(t, "b.000042")})
	require.NoError(t, err)
	assert.Empty(t, gaps)

	gaps, err = Gaps([]Name{n(t, "b.000043"), n(t, "b.000040"), n(t, "b.000041")})
	require.NoError(t, err)
	require.Len(t, gaps, 1, "order of input must not matter")
	assert.Equal(t, "b.000042", gaps[0].String())

	gaps, err = Gaps([]Name{n(t, "b.000040"), n(t, "b.000045")})
	require.NoError(t, err)
	require.Len(t, gaps, 1)
	assert.Equal(t, "b.000041 to b.000044", gaps[0].String())

	// Two bases in one archive means log_bin changed under a running archive.
	// Sorting them into one sequence would hide it.
	_, err = Gaps([]Name{n(t, "old.000001"), n(t, "new.000001")})
	require.Error(t, err)
}

// A file is closed when the one after it exists -- never by size. The newest
// file is the one being written, whatever its size.
func TestClosed(t *testing.T) {
	closed, err := Closed([]Name{n(t, "b.000041"), n(t, "b.000042"), n(t, "b.000043")})
	require.NoError(t, err)
	assert.Equal(t, []string{"b.000041", "b.000042"}, names(closed))

	closed, err = Closed([]Name{n(t, "b.000041")})
	require.NoError(t, err)
	assert.Empty(t, closed, "a lone file is the one being written")

	// A hole means the file before it cannot be trusted as closed: the receiver
	// did not move on from it, something else happened.
	closed, err = Closed([]Name{n(t, "b.000041"), n(t, "b.000043")})
	require.NoError(t, err)
	assert.Empty(t, closed)

	closed, err = Closed(nil)
	require.NoError(t, err)
	assert.Empty(t, closed)
}

// The successor of the highest archived file. A partial file in the spool is
// discarded and asked for again; the server still has the closed original.
func TestResumeFrom(t *testing.T) {
	from, ok := ResumeFrom([]Name{n(t, "b.000040"), n(t, "b.000042"), n(t, "b.000041")})
	require.True(t, ok)
	assert.Equal(t, "b.000043", from.String())

	_, ok = ResumeFrom(nil)
	assert.False(t, ok, "with nothing archived the caller asks the server for its oldest")
}

func names(ns []Name) []string {
	out := make([]string, 0, len(ns))
	for _, v := range ns {
		out = append(out, v.String())
	}
	return out
}
