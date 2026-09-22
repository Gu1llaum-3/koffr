package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A-12 — every command wrote its result to the **error** stream in production,
// and nobody saw it: cobra's cmd.Print* writes to OutOrStderr(), which falls
// back to os.Stderr when no writer is set. The tests always set one, so they
// were blind to the one wiring that ships.
//
// These tests therefore drive a root built like the real one — no SetOut, no
// SetErr — and capture the process's own descriptors.
func TestTheResultOfACommandGoesToStandardOutput(t *testing.T) {
	out, errs := captureDefaultWiring(t, "version")

	if out == "" {
		t.Errorf("koffr version wrote nothing to standard output; the error stream got:\n%s", errs)
	}
	if !strings.Contains(out, "koffr") {
		t.Errorf("standard output does not carry the answer:\n%s", out)
	}
	if strings.Contains(errs, "koffr dev") {
		t.Errorf("the answer leaked onto the error stream:\n%s", errs)
	}
}

// ADR-0012 — a caller piping `koffr config show` into a file must find the
// configuration in it. An empty file satisfies "no log lines", which is how the
// lot 0 acceptance run passed while this was broken.
func TestAConfigurationCanBeRedirectedIntoAFile(t *testing.T) {
	out, _ := captureDefaultWiring(t, "config", "show", "--config", referenceConfig(t))

	if len(out) < 200 {
		t.Errorf("config show produced %d bytes on standard output, want the whole configuration:\n%s",
			len(out), out)
	}
	for _, want := range []string{"agent:", "databases:", "timezone:"} {
		if !strings.Contains(out, want) {
			t.Errorf("standard output does not carry %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"level":`) {
		t.Errorf("a log line landed in the redirected output:\n%s", out)
	}
}

// And the guard that keeps it fixed. cmd.Print* reads as "print", and means
// "print to the error stream" — a trap nothing else in this package can see.
// Results go through say(), warnings through warn().
func TestNoCommandUsesTheMisleadingPrintHelpers(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	forbidden := map[string]string{
		"Print":   "say(cmd, …) for a result, warn(cmd, …) for a warning",
		"Printf":  "say(cmd, …)",
		"Println": "say(cmd, …)",
	}

	fileSet := token.NewFileSet()

	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}

		parsed, err := parser.ParseFile(fileSet, source, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", source, err)
		}

		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			receiver, ok := call.X.(*ast.Ident)
			if !ok || receiver.Name != "cmd" {
				return true
			}

			if instead, banned := forbidden[call.Sel.Name]; banned {
				t.Errorf("%s:%d calls cmd.%s, which writes to the **error** stream (A-12).\n"+
					"  Use %s.",
					source, fileSet.Position(call.Pos()).Line, call.Sel.Name, instead)
			}

			return true
		})
	}
}

// captureDefaultWiring runs a command through a root built exactly as the
// binary builds it, and returns what landed on each of the process's streams.
func captureDefaultWiring(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()

	outRead, outWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	errRead, errWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	realOut, realErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outWrite, errWrite

	// One channel per stream: a harness whose two readers share a channel can
	// hand back the streams swapped, and then it proves nothing.
	fromOut, fromErr := make(chan string, 1), make(chan string, 1)

	go func() {
		read, _ := io.ReadAll(outRead)
		fromOut <- string(read)
	}()

	go func() {
		read, _ := io.ReadAll(errRead)
		fromErr <- string(read)
	}()

	root := NewRoot()
	root.SetArgs(append(args, "--log-dir", t.TempDir()))
	runErr := root.Execute()

	_ = outWrite.Close()
	_ = errWrite.Close()

	os.Stdout, os.Stderr = realOut, realErr

	stdout, stderr = <-fromOut, <-fromErr

	if runErr != nil {
		t.Fatalf("koffr %s: %v\n%s", strings.Join(args, " "), runErr, stderr)
	}

	return stdout, stderr
}

func referenceConfig(t *testing.T) string {
	t.Helper()

	path, err := filepath.Abs(filepath.Join("..", "..", "examples", "koffr.yaml"))
	if err != nil {
		t.Fatalf("locate the example configuration: %v", err)
	}

	return path
}

// ADR-0007, décision de recette du 2026-09-22 — `koffr keygen >> recipients.txt`
// must append the **public** key and nothing else. The private key and the
// warning go to the error stream, so that a distracted redirection cannot write
// a secret to disk.
func TestKeygenPutsThePublicKeyWhereARedirectionCatchesIt(t *testing.T) {
	out, errs := captureDefaultWiring(t, "keygen")

	if !strings.HasPrefix(strings.TrimSpace(out), "age1") {
		t.Errorf("standard output does not start with the public key:\n%q", out)
	}
	if lines := strings.Count(strings.TrimSpace(out), "\n"); lines != 0 {
		t.Errorf("standard output carries %d extra lines; a redirection should append one key:\n%s",
			lines, out)
	}
	if strings.Contains(out, "AGE-SECRET-KEY") {
		t.Fatalf("the private key landed on standard output, where a redirection writes it to disk:\n%s", out)
	}

	if !strings.Contains(errs, "AGE-SECRET-KEY") {
		t.Errorf("the private key was never shown:\n%s", errs)
	}
	for _, want := range []string{"never", "escrow"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the warning does not say %q:\n%s", want, errs)
		}
	}
}
