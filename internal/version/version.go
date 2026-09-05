// Package version holds the build identity.
//
// It is its own package because the manifest, the CLI and the generated
// RESTORE.md all record which Koffr wrote a backup, and a linker flag can only
// point at one variable. Restoring a two-year-old backup starts with knowing
// what made it.
package version

// Value is set at build time with
// -X github.com/Gu1llaum-3/koffr/internal/version.Value=<version>.
//
// "dev" is what an untagged `go build` produces, and it is deliberately not a
// version number: a backup labelled 0.0.0 from someone's laptop is worse than
// one labelled dev.
var Value = "dev"
