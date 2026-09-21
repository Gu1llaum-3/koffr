//go:build darwin

package cli

import "syscall"

// bytesFree turns a statfs answer into bytes. See the Linux file next to this
// one: the block size is a uint32 here and an int64 there.
func bytesFree(volume syscall.Statfs_t) int64 {
	return int64(volume.Bavail) * int64(volume.Bsize) //nolint:gosec // block counts, not user input
}
