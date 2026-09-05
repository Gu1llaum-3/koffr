//go:build linux || darwin

package config

import "golang.org/x/sys/unix"

// filesystemName reports the filesystem type of a path.
//
// The second return says whether an answer was available at all: a platform or
// a sandbox that will not tell us is not the same as a local disk, and the
// caller must not read silence as safety.
func filesystemName(path string) (string, bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return "", false
	}
	return fsTypeName(&st), true
}
