// Package tunnel exposes a database that is only reachable through an executor
// as a local address.
//
// This is what lets a client binary reach a database it has no route to.
// pg_dump takes a host and a port; it knows nothing about SSH. So a listener is
// bound on loopback, every connection to it is forwarded through the executor,
// and pg_dump is pointed at that address.
//
// The port is not ours to choose. P-004 made that a hard ordering constraint:
// libpq matches .pgpass on host AND port, so the credentials file can only be
// written once the listener is bound and Addr can be read. Writing it earlier
// fails with a misleading "no password supplied".
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/Gu1llaum-3/koffr/internal/executor"
)

// Forwarder is a local listener whose connections are carried by an executor.
type Forwarder struct {
	listener net.Listener
	target   string

	closeOnce sync.Once
	closeErr  error

	// done is closed when the accept loop has stopped, and wg tracks the
	// per-connection goroutines. Close waits for both: a forwarder that
	// outlived its job would hold an SSH channel open on the database host.
	done chan struct{}
	wg   sync.WaitGroup

	// live holds both ends of every connection being carried, so Close can
	// cut them. A carry ends when both sides have closed; a far side that
	// streams forever -- a binary log dump thread in --stop-never -- never
	// does, and Close would wait on it until the server rebooted.
	mu     sync.Mutex
	closed bool
	live   map[net.Conn]struct{}
}

// Forward binds a loopback listener and carries its connections to target.
//
// target is an address as the executor sees it, so "127.0.0.1:5432" means the
// database host's own loopback, not ours.
func Forward(ctx context.Context, ex executor.Executor, target string) (*Forwarder, error) {
	if !ex.Capabilities().CanDial {
		// PD-006: refuse now rather than at the first connection, in the middle
		// of a backup.
		return nil, fmt.Errorf(
			"tunnel: executor %s cannot dial, so it cannot reach %s",
			ex.Capabilities().Target, target)
	}

	var lc net.ListenConfig
	// Loopback only. Binding anywhere else would publish the database to the
	// network Koffr sits on, which is the opposite of the point.
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("tunnel: bind local listener: %w", err)
	}

	f := &Forwarder{listener: ln, target: target, done: make(chan struct{}), live: map[net.Conn]struct{}{}}
	go f.accept(ctx, ex)
	return f, nil
}

// Addr is the local address to point a client binary at. It is stable for the
// life of the Forwarder.
func (f *Forwarder) Addr() string { return f.listener.Addr().String() }

func (f *Forwarder) accept(ctx context.Context, ex executor.Executor) {
	defer close(f.done)
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return // closed
		}
		// The connection is registered here, synchronously, before the carrier
		// goroutine runs. Registering inside carry raced Close: Close could
		// snapshot an empty set and then wait forever on a carrier that
		// registered a moment later. If Close has already run, refuse the
		// connection outright.
		if !f.track(conn) {
			_ = conn.Close()
			continue
		}
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			f.carry(ctx, ex, conn)
		}()
	}
}

// carry joins one local connection to a connection opened by the executor.
func (f *Forwarder) carry(ctx context.Context, ex executor.Executor, local net.Conn) {
	defer f.release(local)

	remote, err := ex.Dial(ctx, "tcp", f.target)
	if err != nil {
		// Closing the local side is the only way to report this: the client is
		// speaking a database protocol and has nowhere to put an error message.
		// It sees a connection that ends, which is what an unreachable database
		// looks like anyway.
		return
	}
	// If Close ran between accepting local and dialing remote, remote is not
	// registered and would be leaked; refuse it and let local's deferred
	// release end the carry.
	if !f.track(remote) {
		_ = remote.Close()
		return
	}
	defer f.release(remote)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(remote, local)
		// Half-close so the far side sees the end of the request rather than
		// waiting for data that is not coming.
		if cw, ok := remote.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	_, _ = io.Copy(local, remote)
	if cw, ok := local.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
	wg.Wait()
}

// track registers a connection so Close can cut it, and reports whether it
// did. Once Close has run it refuses, so a connection accepted or dialed in
// the race with Close is handed back to be closed rather than leaked.
func (f *Forwarder) track(c net.Conn) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return false
	}
	f.live[c] = struct{}{}
	return true
}

func (f *Forwarder) release(c net.Conn) {
	f.mu.Lock()
	delete(f.live, c)
	f.mu.Unlock()
	_ = c.Close()
}

// Close stops accepting, cuts the connections in flight, and waits for their
// carriers to finish.
//
// Cutting rather than draining is deliberate. A Forwarder is closed when the
// session that owns it is done, which is after the client process has exited
// or been killed; whatever is still open at that point is a far side that
// has not noticed. The first receiver stopped through a tunnel hung the
// daemon's shutdown for as long as the server kept its dump thread, which is
// forever.
func (f *Forwarder) Close() error {
	f.closeOnce.Do(func() {
		f.closeErr = f.listener.Close()
		if errors.Is(f.closeErr, net.ErrClosed) {
			f.closeErr = nil
		}
		<-f.done
		f.mu.Lock()
		f.closed = true
		for c := range f.live {
			_ = c.Close()
		}
		f.mu.Unlock()
		f.wg.Wait()
	})
	return f.closeErr
}
