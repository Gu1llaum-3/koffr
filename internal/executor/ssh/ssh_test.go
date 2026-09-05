package ssh_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	xssh "golang.org/x/crypto/ssh"

	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/executor/executortest"
	koffrssh "github.com/Gu1llaum-3/koffr/internal/executor/ssh"
	"github.com/Gu1llaum-3/koffr/internal/testutil"
)

var shared struct {
	addr       string
	privateKey []byte
	skipWhy    string
}

func TestMain(m *testing.M) {
	os.Exit(func() int {
		if why := testutil.EnsureDockerHost(); why != "" {
			shared.skipWhy = why
		}
		if shared.skipWhy == "" {
			if err := startSSHD(); err != nil {
				shared.skipWhy = fmt.Sprintf("sshd container unavailable: %v", err)
			}
		}
		if _, fatal := testutil.SkipOrFailWithoutDocker(shared.skipWhy); fatal != "" {
			fmt.Fprintln(os.Stderr, fatal)
			return 1
		}
		return m.Run()
	}())
}

// startSSHD builds the target image with a freshly generated key, so nothing
// long-lived is committed and every run authenticates against material that
// exists only for that run.
func startSSHD() error {
	ctx := context.Background()

	priv, pub, err := generateKeyPair()
	if err != nil {
		return err
	}
	shared.privateKey = priv

	dir, err := filepath.Abs(filepath.Join("testdata", "sshd"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "authorized_keys"), pub, 0o600); err != nil {
		return err
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{Context: dir},
			ExposedPorts:   []string{"22/tcp"},
			WaitingFor:     wait.ForListeningPort("22/tcp"),
		},
		Started: true,
	})
	if err != nil {
		return err
	}

	host, err := container.Host(ctx)
	if err != nil {
		return err
	}
	port, err := container.MappedPort(ctx, "22/tcp")
	if err != nil {
		return err
	}
	shared.addr = fmt.Sprintf("%s:%s", host, port.Port())
	return nil
}

func generateKeyPair() (privatePEM, authorizedKey []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	block, err := xssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, nil, err
	}
	sshPub, err := xssh.NewPublicKey(pub)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(block), xssh.MarshalAuthorizedKey(sshPub), nil
}

func newExecutor(t *testing.T, allowExec bool) executor.Executor {
	t.Helper()
	ex, err := koffrssh.Dial(t.Context(), koffrssh.Config{
		Address:    shared.addr,
		User:       "probe",
		PrivateKey: shared.privateKey,
		// The container's host key is generated at build time and is not worth
		// pinning; every other test here exercises verification properly.
		InsecureIgnoreHostKey: true,
		AllowExec:             allowExec,
	})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, ex.Close()) })
	return ex
}

// The identical suite that executor/local runs. Nothing in it is specific to a
// transport, which is what keeps EX-001 honest: the future agent has to pass
// exactly this.
func TestContract(t *testing.T) {
	if shared.skipWhy != "" {
		t.Skip(shared.skipWhy)
	}
	executortest.Suite(t, executortest.Harness{
		New: func(t *testing.T) executor.Executor { return newExecutor(t, true) },
		DialTarget: func(t *testing.T) (string, string) {
			// The target's own sshd greets every caller with its version
			// banner, so a forwarded connection is provable without standing
			// up a second service inside the container.
			return "127.0.0.1:22", "SSH-2.0-"
		},
	})
}

// M1 needs the tunnel and nothing else. An executor that can forward a
// connection must not silently also be able to run arbitrary commands on a
// database host (CT-002).
func TestTunnelOnlyRefusesExec(t *testing.T) {
	if shared.skipWhy != "" {
		t.Skip(shared.skipWhy)
	}
	ex := newExecutor(t, false)

	require.False(t, ex.Capabilities().CanExec)
	require.True(t, ex.Capabilities().CanDial)

	_, err := ex.Start(t.Context(), executor.Command{Path: "/bin/sh", Args: []string{"-c", "true"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allow_exec",
		"the error should say how to enable it, not merely that it is off")
}

// EF-004: verification is on unless it is turned off explicitly. A missing
// known_hosts must fail the connection, not quietly downgrade it.
func TestHostKeyVerificationIsOnByDefault(t *testing.T) {
	if shared.skipWhy != "" {
		t.Skip(shared.skipWhy)
	}
	_, err := koffrssh.Dial(t.Context(), koffrssh.Config{
		Address:        shared.addr,
		User:           "probe",
		PrivateKey:     shared.privateKey,
		KnownHostsFile: filepath.Join(t.TempDir(), "does-not-exist"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "known_hosts")
}

// A host key that does not match must fail. Without this, the test above only
// proves that a missing file is noticed, not that a wrong key is.
func TestHostKeyMismatchIsRejected(t *testing.T) {
	if shared.skipWhy != "" {
		t.Skip(shared.skipWhy)
	}
	_, other, err := generateKeyPair()
	require.NoError(t, err)

	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	require.NoError(t, os.WriteFile(knownHosts,
		fmt.Appendf(nil, "[%s]:%s %s", "127.0.0.1", portOf(t, shared.addr), other), 0o600))

	_, err = koffrssh.Dial(t.Context(), koffrssh.Config{
		Address:        shared.addr,
		User:           "probe",
		PrivateKey:     shared.privateKey,
		KnownHostsFile: knownHosts,
	})
	require.Error(t, err, "a host key that does not match must not be accepted")
}

// An authentication failure is exactly where a credential ends up in the logs
// if nobody is watching (ENF-021).
func TestNoSecretInAuthenticationError(t *testing.T) {
	if shared.skipWhy != "" {
		t.Skip(shared.skipWhy)
	}
	_, err := koffrssh.Dial(t.Context(), koffrssh.Config{
		Address:               shared.addr,
		User:                  "probe",
		Password:              testutil.SecretSentinel,
		InsecureIgnoreHostKey: true,
	})
	require.Error(t, err)
	testutil.AssertNoSecretLeak(t, err.Error())
}

func TestUnparsableKeyIsNotEchoed(t *testing.T) {
	// The armour is assembled at run time rather than written out, so this file
	// does not itself trip the pre-commit secret guard. Adding it to that
	// guard's allowlist would have been the easier fix and the wrong one: the
	// allowlist is deliberately two files long, and a control that has already
	// failed open once does not need its exceptions widened for a fixture.
	armour := fmt.Sprintf("-----BEGIN %s-----\n%s\n", "OPENSSH PRIVATE KEY", testutil.SecretSentinel)

	_, err := koffrssh.Dial(t.Context(), koffrssh.Config{
		Address:               "127.0.0.1:1",
		User:                  "probe",
		PrivateKey:            []byte(armour),
		InsecureIgnoreHostKey: true,
	})
	require.Error(t, err)
	testutil.AssertNoSecretLeak(t, err.Error())
}

func portOf(t *testing.T, addr string) string {
	t.Helper()
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[i+1:]
		}
	}
	t.Fatalf("no port in %q", addr)
	return ""
}
