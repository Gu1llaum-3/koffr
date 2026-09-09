// Package binlog archives a MariaDB binary log continuously and replays it to a
// point in time (EF-032, EF-082).
//
// The shape is borrowed from how Databasus streams PostgreSQL WAL, recorded in
// ADR-0007: only closed files are archived, continuity is checked by sequence
// rather than trusted from state, the spool is bounded, and the receiver is
// supervised rather than merely restarted.
package binlog

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Name is one binary log file name, as the server writes it.
//
// MariaDB names them <base>.<six digits>: mariadb-bin.000042. The base comes
// from log_bin and can be anything an operator chose; the number is what
// orders them and what the continuity check reads.
type Name struct {
	Base string
	Seq  uint64
	// width is how many digits the number was written with, kept so the next
	// name is spelled the way the server will spell it.
	width int
}

// Parse splits a binary log file name.
func Parse(s string) (Name, error) {
	i := strings.LastIndexByte(s, '.')
	if i <= 0 || i == len(s)-1 {
		return Name{}, fmt.Errorf("binlog: %q is not a binary log name (want <base>.<number>)", s)
	}
	digits := s[i+1:]
	seq, err := strconv.ParseUint(digits, 10, 64)
	if err != nil || len(digits) < 6 {
		return Name{}, fmt.Errorf("binlog: %q is not a binary log name (want at least six digits after the dot)", s)
	}
	return Name{Base: s[:i], Seq: seq, width: len(digits)}, nil
}

// String spells the name the way the server does.
func (n Name) String() string {
	w := n.width
	if w < 6 {
		w = 6
	}
	return fmt.Sprintf("%s.%0*d", n.Base, w, n.Seq)
}

// Next is the file the server writes after this one.
func (n Name) Next() Name { return Name{Base: n.Base, Seq: n.Seq + 1, width: n.width} }

// Prev is the file before this one. The first file has no predecessor and
// returns itself, so a caller printing "through Prev()" of a resume point that
// is the very first file says something true.
func (n Name) Prev() Name {
	if n.Seq == 0 {
		return n
	}
	return Name{Base: n.Base, Seq: n.Seq - 1, width: n.width}
}

// Sort orders names by sequence. Two bases in one list is a configuration that
// changed log_bin under a running archive, and is reported rather than sorted
// into nonsense.
func Sort(names []Name) error {
	for i := 1; i < len(names); i++ {
		if names[i].Base != names[0].Base {
			return fmt.Errorf("binlog: two different log bases, %q and %q: log_bin changed while archiving",
				names[0].Base, names[i].Base)
		}
	}
	sort.Slice(names, func(i, j int) bool { return names[i].Seq < names[j].Seq })
	return nil
}

// Gap is a hole in a sequence: the files from Start to End exclusive are
// missing.
type Gap struct{ Start, End Name }

// String names the missing files, as a single name or a range.
func (g Gap) String() string {
	last := Name{Base: g.Start.Base, Seq: g.End.Seq - 1, width: g.Start.width}
	if last.Seq == g.Start.Seq {
		return g.Start.String()
	}
	return g.Start.String() + " to " + last.String()
}

// Gaps finds every hole in an archived sequence.
//
// Derived from the names each time, never stored: a stored "chain is intact"
// can be wrong, and a hole between 000041 and 000043 cannot. This is the
// integrity check of the whole archive and it is a pure function on purpose,
// so a test can hand it any sequence and the answer is the same in production.
func Gaps(names []Name) ([]Gap, error) {
	if len(names) < 2 {
		return nil, nil
	}
	sorted := append([]Name(nil), names...)
	if err := Sort(sorted); err != nil {
		return nil, err
	}
	var gaps []Gap
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Seq > sorted[i-1].Seq+1 {
			gaps = append(gaps, Gap{Start: sorted[i-1].Next(), End: sorted[i]})
		}
	}
	return gaps, nil
}

// Closed reports which of the files present in a spool are finished.
//
// A file is closed when the one after it exists: the receiver writes them in
// order and moves on when the server rotates. Never by size -- max_binlog_size
// is a ceiling the server may rotate under, and the last file is always short.
// The newest file is therefore never returned, whatever its size: it is the
// one being written.
func Closed(present []Name) ([]Name, error) {
	if len(present) == 0 {
		return nil, nil
	}
	sorted := append([]Name(nil), present...)
	if err := Sort(sorted); err != nil {
		return nil, err
	}
	var closed []Name
	for i := 0; i+1 < len(sorted); i++ {
		if sorted[i+1].Seq == sorted[i].Seq+1 {
			closed = append(closed, sorted[i])
		}
	}
	return closed, nil
}

// ResumeFrom is the first file an archive does not yet hold.
//
// The successor of the highest archived file, which is the rule Databasus
// arrived at for WAL: a partial file left in the spool is discarded and asked
// for again, because the server still has the closed original and a partial
// file's contents are not to be trusted. With nothing archived yet, the caller
// starts from the oldest file the server still has.
func ResumeFrom(archived []Name) (Name, bool) {
	if len(archived) == 0 {
		return Name{}, false
	}
	highest := archived[0]
	for _, n := range archived[1:] {
		if n.Seq > highest.Seq {
			highest = n
		}
	}
	return highest.Next(), true
}
