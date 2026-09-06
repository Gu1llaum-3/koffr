package backup

import "github.com/Gu1llaum-3/koffr/internal/source"

// snapshotConsistentFor exposes the unexported reader to the package's external
// tests. The behaviour is worth testing on its own: it is the only thing that
// turns a source's sentence into a fact the manifest carries.
func SnapshotConsistentFor(info source.Info) *bool { return snapshotConsistent(info) }
