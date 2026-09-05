// Package executortest is the contract every executor.Executor must satisfy.
//
// It is written before the first implementation and reused unchanged by each
// one, exactly as storagetest is. That matters more here than anywhere else:
// EX-001 promises that adding an agent is a purely additive change, and the
// only way to keep that promise honest is for the agent to have to pass the
// same suite as local and SSH.
package executortest

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

	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

// shell is present on every target Koffr supports, and is how the SSH transport
// runs anything at all: an exec channel takes a command line, not an argv.
const shell = "/bin/sh"

// Harness is what a backend must provide to be put through the contract.
type Harness struct {
	// New returns a fresh Executor. It is called once per test.
	New func(t *testing.T) executor.Executor

	// DialTarget gives an address the executor can reach, together with a
	// prefix the service there sends unprompted. An unprompted banner is what
	// makes a successful dial provable without speaking a protocol.
	//
	// Nil if the backend does not advertise CanDial.
	DialTarget func(t *testing.T) (addr, wantPrefix string)
}

// Suite runs the whole contract.
func Suite(t *testing.T, h Harness) {
	t.Helper()

	t.Run("Stdout", func(t *testing.T) { testStdout(t, h) })
	t.Run("StdoutAndStderrAreSeparate", func(t *testing.T) { testStreamsAreSeparate(t, h) })
	t.Run("ExitCode", func(t *testing.T) { testExitCode(t, h) })
	t.Run("StartFailsForMissingBinary", func(t *testing.T) { testMissingBinary(t, h) })
	t.Run("Stdin", func(t *testing.T) { testStdin(t, h) })
	t.Run("Env", func(t *testing.T) { testEnv(t, h) })
	t.Run("ArgumentsArriveIntact", func(t *testing.T) { testArgumentsArriveIntact(t, h) })
	t.Run("LargeStdout", func(t *testing.T) { testLargeStdout(t, h) })
	t.Run("WaitAfterAbandoningStdout", func(t *testing.T) { testWaitAfterAbandoningStdout(t, h) })
	t.Run("CancellationKillsProcess", func(t *testing.T) { testCancellationKills(t, h) })
	t.Run("WaitIsIdempotent", func(t *testing.T) { testWaitIsIdempotent(t, h) })
	t.Run("NoSecretInStartError", func(t *testing.T) { testNoSecretInStartError(t, h) })
	t.Run("Capabilities", func(t *testing.T) { testCapabilities(t, h) })
	t.Run("Dial", func(t *testing.T) { testDial(t, h) })
	t.Run("CloseIsIdempotent", func(t *testing.T) { testCloseIsIdempotent(t, h) })
}

// sh builds a command that runs a shell snippet on the target.
func sh(script string) executor.Command {
	return executor.Command{Path: shell, Args: []string{"-c", script}}
}

func start(t *testing.T, ex executor.Executor, cmd executor.Command) executor.Process {
	t.Helper()
	p, err := ex.Start(t.Context(), cmd)
	require.NoError(t, err)
	return p
}

func testStdout(t *testing.T, h Harness) {
	ex := h.New(t)
	p := start(t, ex, sh("echo hello"))

	out, err := io.ReadAll(p.Stdout())
	require.NoError(t, err)
	require.NoError(t, p.Wait())
	assert.Equal(t, "hello\n", string(out))
}

// The two streams must not be merged. The pipeline reads the backup from
// stdout; a diagnostic line mixed into it would corrupt the archive, and
// pg_basebackup writes its LSNs to stderr precisely so both can be used.
func testStreamsAreSeparate(t *testing.T, h Harness) {
	ex := h.New(t)
	p := start(t, ex, sh("echo to-stdout; echo to-stderr >&2"))

	// stderr is read concurrently: a backend that buffers neither would
	// deadlock if we drained them one after the other.
	stderrCh := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(p.Stderr())
		stderrCh <- string(b)
	}()

	out, err := io.ReadAll(p.Stdout())
	require.NoError(t, err)
	require.NoError(t, p.Wait())

	assert.Equal(t, "to-stdout\n", string(out))
	assert.Equal(t, "to-stderr\n", <-stderrCh)
}

// The exit status is how a source failure is told apart from a storage failure,
// which is what decides whether the job is retried (ENF-011).
func testExitCode(t *testing.T, h Harness) {
	ex := h.New(t)
	p := start(t, ex, sh("echo partial; exit 42"))

	_, err := io.ReadAll(p.Stdout())
	require.NoError(t, err)

	err = p.Wait()
	require.Error(t, err)

	var exitErr *executor.ExitError
	require.ErrorAs(t, err, &exitErr, "a non-zero exit must be reported as an ExitError")
	assert.Equal(t, 42, exitErr.Code)
}

// PD-006: a missing client binary is a configuration problem, and it must
// surface as a named failure rather than a mysterious empty stream.
//
// Where it surfaces depends on the transport, and the contract says so rather
// than pretending otherwise: a local executor knows at Start, while SSH hands
// the command to a remote shell and only learns from exit status 127. Demanding
// one or the other would force one backend to lie or to pay a round trip on
// every command.
func testMissingBinary(t *testing.T, h Harness) {
	ex := h.New(t)
	const missing = "koffr-not-a-binary"

	p, err := ex.Start(t.Context(), executor.Command{Path: "/nonexistent/" + missing})
	if err != nil {
		assert.Contains(t, err.Error(), missing, "the error must name what could not be run")
		return
	}

	stderr, readErr := io.ReadAll(p.Stderr())
	require.NoError(t, readErr)
	err = p.Wait()
	require.Error(t, err, "a command that could not be run must not report success")
	assert.Contains(t, string(stderr)+err.Error(), missing,
		"the failure must name what could not be run")
}

func testStdin(t *testing.T, h Harness) {
	ex := h.New(t)
	cmd := sh("cat")
	cmd.Stdin = strings.NewReader("fed through stdin")

	p := start(t, ex, cmd)
	out, err := io.ReadAll(p.Stdout())
	require.NoError(t, err)
	require.NoError(t, p.Wait())
	assert.Equal(t, "fed through stdin", string(out))
}

// Credentials travel through the environment or a temporary file, never through
// the argument list (ENF-021), so the environment has to work.
func testEnv(t *testing.T, h Harness) {
	ex := h.New(t)
	cmd := sh("printf '%s' \"$KOFFR_PROBE\"")
	cmd.Env = []string{"KOFFR_PROBE=" + testutil.SecretSentinel}

	p := start(t, ex, cmd)
	out, err := io.ReadAll(p.Stdout())
	require.NoError(t, err)
	require.NoError(t, p.Wait())
	assert.Equal(t, testutil.SecretSentinel, string(out))
}

// The SSH transport turns argv into a command line for a remote shell, so
// quoting is not a detail: pg_dump takes --exclude-table='some table', and an
// argument that loses its quoting either dumps the wrong thing or runs
// something else entirely.
func testArgumentsArriveIntact(t *testing.T, h Harness) {
	ex := h.New(t)

	awkward := []string{
		"plain",
		"with space",
		"with'single",
		`with"double`,
		"with$dollar",
		"with;semicolon",
		"with`backtick",
		"with\\backslash",
		"with*glob?",
		"with\ttab",
		"--exclude-table=some table",
	}

	// printf '%s\n' repeats its format for every argument, so each one comes
	// back on its own line whatever it contains.
	cmd := executor.Command{
		Path: shell,
		Args: append([]string{"-c", `printf '%s\n' "$@"`, "sh"}, awkward...),
	}
	p := start(t, ex, cmd)
	out, err := io.ReadAll(p.Stdout())
	require.NoError(t, err)
	require.NoError(t, p.Wait())

	assert.Equal(t, strings.Join(awkward, "\n")+"\n", string(out))
}

// The backup stream is the whole point, and it is far larger than any pipe
// buffer. A backend that only ever moved a few kilobytes would pass every test
// above and deadlock on the first real job.
func testLargeStdout(t *testing.T, h Harness) {
	ex := h.New(t)
	const lines = 200000
	p := start(t, ex, sh("i=0; while [ $i -lt 200000 ]; do echo 0123456789012345678901234567890123456789; i=$((i+1)); done"))

	n, err := io.Copy(io.Discard, p.Stdout())
	require.NoError(t, err)
	require.NoError(t, p.Wait())
	assert.Equal(t, int64(lines*41), n)
}

// The interface invariant that keeps teardown from deadlocking.
//
// When the storage branch fails, the pipeline stops reading stdout and tears
// down. If Wait blocked until a process nobody is reading from finished
// writing, the job would hang instead of reporting the storage failure.
func testWaitAfterAbandoningStdout(t *testing.T, h Harness) {
	ex := h.New(t)
	p := start(t, ex, sh("i=0; while [ $i -lt 200000 ]; do echo 0123456789012345678901234567890123456789; i=$((i+1)); done"))

	// Read a little, then walk away, exactly as a failing pipeline would.
	buf := make([]byte, 64)
	_, _ = io.ReadFull(p.Stdout(), buf)

	done := make(chan error, 1)
	go func() { done <- p.Wait() }()

	select {
	case <-done:
		// Any outcome is acceptable; not returning is not.
	case <-time.After(10 * time.Second):
		t.Fatal("Wait blocked on an abandoned stdout: this is the deadlock the invariant exists to prevent")
	}
}

// Cancelling must actually kill the process, not merely stop waiting for it. An
// orphaned pg_basebackup holds a replication slot, and a held slot makes the
// source retain WAL until the disk fills.
func testCancellationKills(t *testing.T, h Harness) {
	ex := h.New(t)
	ctx, cancel := context.WithCancel(t.Context())

	p, err := ex.Start(ctx, sh("sleep 300"))
	require.NoError(t, err)

	cancel()

	done := make(chan error, 1)
	go func() { done <- p.Wait() }()

	select {
	case err := <-done:
		require.Error(t, err, "a killed process did not exit cleanly, so Wait must say so")
	case <-time.After(15 * time.Second):
		t.Fatal("cancelling the context did not stop the process")
	}
}

// Teardown paths call Wait more than once. Doing so must not panic, hang, or
// report a different outcome the second time.
func testWaitIsIdempotent(t *testing.T, h Harness) {
	ex := h.New(t)
	p := start(t, ex, sh("exit 7"))
	_, _ = io.ReadAll(p.Stdout())

	first := p.Wait()
	second := p.Wait()

	var a, b *executor.ExitError
	require.ErrorAs(t, first, &a)
	require.ErrorAs(t, second, &b)
	assert.Equal(t, a.Code, b.Code)
}

// A failure must not echo the command line or the environment. Credentials are
// not supposed to be in argv (ENF-021), but an error message is exactly where a
// mistake elsewhere turns into a leak in the logs.
//
// As with testMissingBinary, the failure surfaces at Start or at Wait depending
// on the transport, so both are checked.
func testNoSecretInStartError(t *testing.T, h Harness) {
	ex := h.New(t)
	cmd := executor.Command{
		Path: "/nonexistent/koffr-not-a-binary",
		Args: []string{"--password=" + testutil.SecretSentinel},
		Env:  []string{"PGPASSWORD=" + testutil.SecretSentinel},
	}

	p, err := ex.Start(t.Context(), cmd)
	if err != nil {
		testutil.AssertNoSecretLeak(t, err.Error())
		return
	}
	stderr, readErr := io.ReadAll(p.Stderr())
	require.NoError(t, readErr)
	err = p.Wait()
	require.Error(t, err)
	testutil.AssertNoSecretLeak(t, err.Error(), string(stderr))
}

// A backend that claims a capability it does not have makes EF-005 useless:
// the configuration check would accept a source it cannot actually back up.
func testCapabilities(t *testing.T, h Harness) {
	ex := h.New(t)
	caps := ex.Capabilities()

	assert.NotEmpty(t, caps.Target, "Target identifies the executor in diagnostics")
	testutil.AssertNoSecretLeak(t, caps.Target)

	if !caps.CanExec {
		_, err := ex.Start(t.Context(), sh("true"))
		assert.Error(t, err, "CanExec is false but Start was accepted")
	}
	if !caps.CanDial {
		_, err := ex.Dial(t.Context(), "tcp", "127.0.0.1:1")
		assert.Error(t, err, "CanDial is false but Dial was accepted")
	}
}

func testDial(t *testing.T, h Harness) {
	ex := h.New(t)
	if !ex.Capabilities().CanDial || h.DialTarget == nil {
		t.Skip("backend does not advertise dialling")
	}
	addr, wantPrefix := h.DialTarget(t)

	conn, err := ex.Dial(t.Context(), "tcp", addr)
	require.NoError(t, err)
	defer func() { assert.NoError(t, conn.Close()) }()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
	buf := make([]byte, len(wantPrefix))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, wantPrefix, string(buf))

	// Dialling somewhere with nothing listening must fail rather than hand back
	// a connection that only errors later, in the middle of a backup.
	_, err = ex.Dial(t.Context(), "tcp", "127.0.0.1:1")
	assert.Error(t, err)
}

// Close runs from deferred teardown, sometimes twice on an error path.
func testCloseIsIdempotent(t *testing.T, h Harness) {
	ex := h.New(t)
	require.NoError(t, ex.Close())
	assert.NoError(t, ex.Close())
}

// Listener starts a TCP service that greets every caller, for use as a
// DialTarget. It is here rather than in each backend's test so that local and
// SSH prove the same thing.
func Listener(t *testing.T, banner string) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed by cleanup
			}
			go func() {
				defer func() { _ = conn.Close() }()
				if _, err := io.WriteString(conn, banner); err != nil && !errors.Is(err, net.ErrClosed) {
					return
				}
			}()
		}
	}()
	return ln.Addr().String()
}
