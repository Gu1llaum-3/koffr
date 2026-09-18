package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/build"
)

// `koffr version` prints one human line naming the product and its version.
func TestVersionPrintsOneLine(t *testing.T) {
	out := run(t, "version")

	if lines := strings.Count(strings.TrimSpace(out), "\n"); lines != 0 {
		t.Errorf("got %d extra lines, want a single line:\n%s", lines, out)
	}
	for _, want := range []string{build.Name, build.Info().Version, build.Info().Platform} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
}

// `koffr version --json` prints one JSON object carrying the build details.
func TestVersionJSONPrintsTheBuildDetails(t *testing.T) {
	out := run(t, "version", "--json")

	var got build.Details
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	if got != build.Info() {
		t.Errorf("got %+v, want %+v", got, build.Info())
	}
}

// An unknown command fails rather than doing nothing.
func TestUnknownCommandFails(t *testing.T) {
	root := NewRoot()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"backup-everything-now"})

	if err := root.Execute(); err == nil {
		t.Fatal("an unknown command returned no error")
	}
}

// run executes the root command with args and returns what it wrote to standard
// output. The two streams are kept apart on purpose: results go out, logs go to
// standard error, and a caller piping `koffr config show` into a file must not
// find log lines in it.
func run(t *testing.T, args ...string) string {
	t.Helper()

	out, errs, err := execute(t, args...)
	if err != nil {
		t.Fatalf("koffr %s: %v\n%s", strings.Join(args, " "), err, errs)
	}

	return out
}

func execute(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	var out, errs bytes.Buffer
	root := NewRoot()
	root.SetOut(&out)
	root.SetErr(&errs)
	root.SetArgs(args)

	err = root.Execute()

	return out.String(), errs.String(), err
}
