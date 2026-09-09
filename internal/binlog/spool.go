package binlog

import "fmt"

// Bounds is the back-pressure on the spool.
//
// mariadb-binlog --raw writes files; it has no kernel pipe to push back on, so
// the spool directory is the buffer. Above High the receiver is stopped, and it
// is not restarted until the archiver has drained the spool below Low. The
// distance between the two is what stops the receiver flapping on the boundary
// -- a lesson Databasus records as a 5x hysteresis, and one the first version
// of anything like this gets wrong.
type Bounds struct {
	High uint64
	Low  uint64
}

// Validate refuses bounds that cannot work.
func (b Bounds) Validate() error {
	if b.High == 0 {
		return fmt.Errorf("binlog: spool_high must be set")
	}
	if b.Low == 0 {
		return fmt.Errorf("binlog: spool_low must be set")
	}
	if b.Low >= b.High {
		return fmt.Errorf("binlog: spool_low (%d) must be below spool_high (%d), or the receiver flaps on the boundary",
			b.Low, b.High)
	}
	return nil
}

// WithDefaultLow fills Low from High when it was not given: a fifth, the ratio
// that measured well elsewhere.
func (b Bounds) WithDefaultLow() Bounds {
	if b.Low == 0 && b.High > 0 {
		b.Low = b.High / 5
		if b.Low == 0 {
			b.Low = 1
		}
	}
	return b
}

// Gate decides whether the receiver may run, given how much the spool holds.
//
// It has memory: once stopped it stays stopped until the spool is under Low,
// not merely under High. That memory is the hysteresis.
type Gate struct {
	bounds  Bounds
	stopped bool
}

func NewGate(b Bounds) *Gate { return &Gate{bounds: b} }

// Allow reports whether the receiver should be running when the spool holds
// this many bytes, and records the decision.
func (g *Gate) Allow(spoolBytes uint64) bool {
	switch {
	case g.stopped && spoolBytes < g.bounds.Low:
		g.stopped = false
	case !g.stopped && spoolBytes >= g.bounds.High:
		g.stopped = true
	}
	return !g.stopped
}

// Stopped reports the gate's current state without changing it.
func (g *Gate) Stopped() bool { return g.stopped }
