package ssh

import (
	"net"
	"sync"
	"time"
)

// tunnelConn gives an SSH channel the deadline support a database driver needs.
//
// An SSH channel has no deadline mechanism at all -- SetDeadline on one returns
// "deadline not supported" -- and pgx sets read deadlines to bound a query. A
// tunnel that could not carry them would work in a smoke test and hang forever
// on a database that stopped answering, which is the failure a backup tool can
// least afford.
//
// Deadlines are implemented the only way a stream without them allows: a timer
// that closes the connection when it fires. That is coarser than a real
// deadline, since the connection does not survive one, but a caller whose
// deadline expired is not going to reuse it either.
type tunnelConn struct {
	net.Conn

	mu         sync.Mutex
	readTimer  *time.Timer
	writeTimer *time.Timer
}

func newTunnelConn(c net.Conn) net.Conn { return &tunnelConn{Conn: c} }

func (c *tunnelConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

func (c *tunnelConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readTimer = c.arm(c.readTimer, t)
	return nil
}

func (c *tunnelConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeTimer = c.arm(c.writeTimer, t)
	return nil
}

// arm replaces a timer. A zero time clears the deadline, which is how a caller
// says the operation finished in time.
func (c *tunnelConn) arm(existing *time.Timer, at time.Time) *time.Timer {
	if existing != nil {
		existing.Stop()
	}
	if at.IsZero() {
		return nil
	}
	d := time.Until(at)
	if d <= 0 {
		_ = c.Conn.Close()
		return nil
	}
	return time.AfterFunc(d, func() { _ = c.Conn.Close() })
}

func (c *tunnelConn) Close() error {
	c.mu.Lock()
	if c.readTimer != nil {
		c.readTimer.Stop()
	}
	if c.writeTimer != nil {
		c.writeTimer.Stop()
	}
	c.mu.Unlock()
	return c.Conn.Close()
}
