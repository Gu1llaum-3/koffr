package tunnel_test

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/executor/local"
	"github.com/Gu1llaum-3/koffr/internal/executor/tunnel"
)

func TestMain(m *testing.M) {
	// A forwarder owns goroutines per connection. One left behind per backup
	// job is a slow leak in a process meant to run for months.
	goleak.VerifyTestMain(m)
}

// echoServer stands in for the database: it greets, then echoes.
func echoServer(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	// Cleanups run last-registered-first, so the wait is registered before the
	// close that unblocks it. Registering them the other way round deadlocks
	// the test in Accept, which is exactly what happened the first time.
	done := make(chan struct{})
	t.Cleanup(func() { <-done })
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		defer close(done)
		var wg []chan struct{}
		for {
			conn, err := ln.Accept()
			if err != nil {
				for _, c := range wg {
					<-c
				}
				return
			}
			finished := make(chan struct{})
			wg = append(wg, finished)
			go func() {
				defer close(finished)
				defer func() { _ = conn.Close() }()
				if _, err := io.WriteString(conn, "READY\n"); err != nil {
					return
				}
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return ln.Addr().String()
}

// dial opens a connection bound to the test's context, so a hung tunnel fails
// the test instead of the suite.
func dial(t *testing.T, addr string) (net.Conn, error) {
	t.Helper()
	var d net.Dialer
	return d.DialContext(t.Context(), "tcp", addr)
}

func TestForward_CarriesTraffic(t *testing.T) {
	target := echoServer(t)

	f, err := tunnel.Forward(t.Context(), local.New(), target)
	require.NoError(t, err)
	defer func() { assert.NoError(t, f.Close()) }()

	assert.True(t, strings.HasPrefix(f.Addr(), "127.0.0.1:"),
		"a tunnel must listen on loopback only: anything else exposes the database to the network")
	assert.NotEqual(t, target, f.Addr())

	conn, err := dial(t, f.Addr())
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	banner := make([]byte, len("READY\n"))
	_, err = io.ReadFull(conn, banner)
	require.NoError(t, err)
	assert.Equal(t, "READY\n", string(banner))

	_, err = io.WriteString(conn, "SELECT 1\n")
	require.NoError(t, err)
	echoed := make([]byte, len("SELECT 1\n"))
	_, err = io.ReadFull(conn, echoed)
	require.NoError(t, err)
	assert.Equal(t, "SELECT 1\n", string(echoed))
}

// The port is chosen by the kernel and is not known until the listener is
// bound. P-004 turns that into a hard ordering constraint: libpq matches
// .pgpass on host AND port, so the credentials file can only be written once
// Addr() can be read.
func TestForward_PortIsKnownOnlyAfterBinding(t *testing.T) {
	f, err := tunnel.Forward(t.Context(), local.New(), echoServer(t))
	require.NoError(t, err)
	defer func() { assert.NoError(t, f.Close()) }()

	_, port, err := net.SplitHostPort(f.Addr())
	require.NoError(t, err)
	assert.NotEqual(t, "0", port, "Forward must return a bound port, not the placeholder it asked for")
	assert.Equal(t, f.Addr(), f.Addr(), "the address must not change under the caller's feet")
}

func TestForward_ConcurrentConnections(t *testing.T) {
	target := echoServer(t)
	f, err := tunnel.Forward(t.Context(), local.New(), target)
	require.NoError(t, err)
	defer func() { assert.NoError(t, f.Close()) }()

	// pg_dump opens more than one connection when dumping large objects, and
	// the privilege pre-check runs on its own.
	for range 5 {
		conn, err := dial(t, f.Addr())
		require.NoError(t, err)
		require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
		banner := make([]byte, 6)
		_, err = io.ReadFull(conn, banner)
		require.NoError(t, err)
		assert.Equal(t, "READY\n", string(banner))
		require.NoError(t, conn.Close())
	}
}

// A tunnel that outlives its job holds an SSH channel open on the database
// host. Close must stop accepting and release everything.
func TestForward_CloseStopsListening(t *testing.T) {
	f, err := tunnel.Forward(t.Context(), local.New(), echoServer(t))
	require.NoError(t, err)
	addr := f.Addr()

	require.NoError(t, f.Close())
	assert.NoError(t, f.Close(), "Close runs from deferred teardown and may run twice")

	_, err = dial(t, addr)
	assert.Error(t, err, "the listener is still accepting after Close")
}

// An executor that cannot dial must be refused when the tunnel is built, not
// when the first connection is attempted in the middle of a backup (PD-006).
func TestForward_RefusesExecutorThatCannotDial(t *testing.T) {
	_, err := tunnel.Forward(t.Context(), noDial{}, "127.0.0.1:5432")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dial")
}

// A target that refuses connections must surface as a failed connection through
// the tunnel, not as a hang.
func TestForward_UnreachableTargetFailsTheConnection(t *testing.T) {
	f, err := tunnel.Forward(t.Context(), local.New(), "127.0.0.1:1")
	require.NoError(t, err, "the tunnel binds even if the target is down; that is discovered per connection")
	defer func() { assert.NoError(t, f.Close()) }()

	conn, err := dial(t, f.Addr())
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	_, err = io.ReadFull(conn, make([]byte, 1))
	require.Error(t, err, "a connection to an unreachable target must end, not hang")
}

// noDial is an executor that can run commands but cannot forward a connection,
// which is what a future agent restricted to exec would look like.
type noDial struct{}

func (noDial) Dial(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("not supported")
}
func (noDial) Start(context.Context, executor.Command) (executor.Process, error) {
	return nil, errors.New("not supported")
}
func (noDial) Capabilities() executor.Capabilities {
	return executor.Capabilities{CanDial: false, CanExec: true, Target: "no-dial"}
}
func (noDial) Close() error { return nil }

var _ executor.Executor = noDial{}
