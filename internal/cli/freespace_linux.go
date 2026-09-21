//go:build linux

package cli

import "syscall"

// bytesFree turns a statfs answer into bytes. It has one file per platform
// because the block size is not the same type on each — int64 on Linux, uint32
// on macOS — and a conversion that is right on one is flagged as needless on
// the other.
func bytesFree(volume syscall.Statfs_t) int64 {
	return int64(volume.Bavail) * volume.Bsize //nolint:gosec // block counts, not user input
}
