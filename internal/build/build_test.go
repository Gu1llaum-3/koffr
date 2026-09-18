package build

import (
	"encoding/json"
	"runtime"
	"testing"
)

// Info reports the name, version, commit, date, Go version and platform of the
// running binary.
func TestInfoCarriesEveryField(t *testing.T) {
	got := Info()

	if got.Name != "koffr" {
		t.Errorf("Name = %q, want %q", got.Name, "koffr")
	}
	if got.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", got.GoVersion, runtime.Version())
	}
	if want := runtime.GOOS + "/" + runtime.GOARCH; got.Platform != want {
		t.Errorf("Platform = %q, want %q", got.Platform, want)
	}
	for _, f := range []struct{ name, value string }{
		{"Version", got.Version},
		{"Commit", got.Commit},
		{"Date", got.Date},
	} {
		if f.value == "" {
			t.Errorf("%s is empty; an unstamped build still reports a placeholder", f.name)
		}
	}
}

// The --json output carries exactly these keys, and none is empty once the
// binary is stamped by -ldflags at release time.
func TestInfoJSONHasEveryKeyAndNoEmptyValueInAReleaseBuild(t *testing.T) {
	stampForTest(t, "1.4.0", "9f3c1ab", "2026-09-18T10:00:00Z")

	raw, err := json.Marshal(Info())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	want := []string{"name", "version", "commit", "date", "go_version", "platform"}
	for _, key := range want {
		value, ok := got[key]
		if !ok {
			t.Errorf("key %q is missing from %s", key, raw)
			continue
		}
		if value == "" {
			t.Errorf("key %q is empty in a release build", key)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d keys in %s, want exactly %d", len(got), raw, len(want))
	}
}

// The values injected by -ldflags land in the reported fields.
func TestInfoReportsTheStampedValues(t *testing.T) {
	stampForTest(t, "1.4.0", "9f3c1ab", "2026-09-18T10:00:00Z")

	got := Info()

	if got.Version != "1.4.0" {
		t.Errorf("Version = %q, want %q", got.Version, "1.4.0")
	}
	if got.Commit != "9f3c1ab" {
		t.Errorf("Commit = %q, want %q", got.Commit, "9f3c1ab")
	}
	if got.Date != "2026-09-18T10:00:00Z" {
		t.Errorf("Date = %q, want %q", got.Date, "2026-09-18T10:00:00Z")
	}
}

// stampForTest sets the values -ldflags injects at release time, and restores
// the unstamped defaults when the test ends.
func stampForTest(t *testing.T, v, c, d string) {
	t.Helper()

	previous := [3]string{version, commit, date}
	t.Cleanup(func() { version, commit, date = previous[0], previous[1], previous[2] })

	version, commit, date = v, c, d
}
