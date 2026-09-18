// Package build reports what this binary is: its version, the commit it was
// built from, and the platform it runs on.
package build

import "runtime"

// Name is the product name, fixed by ADR-0001.
const Name = "koffr"

// Values stamped at release time with -ldflags -X. A build made without them
// still reports a placeholder rather than an empty string, so that a bug report
// from a hand-built binary is never silently unattributable.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// Details describes the running binary.
type Details struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// Info returns the details of the running binary.
func Info() Details {
	return Details{
		Name:      Name,
		Version:   version,
		Commit:    commit,
		Date:      date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// String renders the details on a single line, the way `koffr version` prints
// them without --json.
func (d Details) String() string {
	return d.Name + " " + d.Version +
		" (commit " + d.Commit + ", built " + d.Date + ")" +
		" " + d.GoVersion + " " + d.Platform
}
