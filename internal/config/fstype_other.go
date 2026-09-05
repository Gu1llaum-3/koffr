//go:build !linux && !darwin

package config

// filesystemName has no answer on platforms Koffr does not target for
// production. Reporting "unknown" keeps the check honest rather than silently
// approving.
func filesystemName(string) (string, bool) { return "", false }
