package pipeline_test

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/Gu1llaum-3/koffr/internal/domain/crypto"
	"github.com/Gu1llaum-3/koffr/internal/pipeline"
)

// interopDir is where this test lays out what an external tool needs to open an
// archive koffr wrote. A flag rather than an environment variable: AR-05
// reserves reading the environment to internal/config.
var interopDir = flag.String("interop-dir", "",
	"write an archive, its plaintext and a private key here, for scripts/check-age-interop.sh")

// E-075 — the archive must be readable by the standard `age` tool, without
// koffr. Proving that means running the real binary, and AR-07 reserves
// os/exec to internal/engine — so this test writes the pieces and
// scripts/check-age-interop.sh runs age over them (N-8).
//
// Without -interop-dir it does nothing: the script is what drives it.
func TestWriteTheInteropFixture(t *testing.T) {
	if *interopDir == "" {
		t.Skip("no -interop-dir: run scripts/check-age-interop.sh to check age interoperability")
	}

	const plaintext = "koffr wrote this, and the standard age tool has to be able to read it.\n"

	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	archive, err := os.Create(filepath.Join(*interopDir, "archive.age"))
	if err != nil {
		t.Fatalf("create the archive: %v", err)
	}

	encrypt, err := pipelineEncrypt(archive, key.Recipient())
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := io.WriteString(encrypt, plaintext); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := encrypt.Close(); err != nil {
		t.Fatalf("close the encryption: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close the archive: %v", err)
	}

	write(t, "identity.txt", key.String())
	write(t, "expected.txt", plaintext)
}

func write(t *testing.T, name, contents string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(*interopDir, name), []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func pipelineEncrypt(into io.Writer, to *age.X25519Recipient) (io.WriteCloser, error) {
	return pipeline.Encrypt(into, crypto.Recipients{Keys: []*age.X25519Recipient{to}})
}
