//go:build linux

package config

import "golang.org/x/sys/unix"

// magic numbers Linux reports instead of a name.
const (
	nfsSuperMagic    = 0x6969
	smbSuperMagic    = 0x517B
	cifsMagicNumber  = 0xFF534D42
	smb2MagicNumber  = 0xFE534D42
	fuseSuperMagic   = 0x65735546
	v9fsMagic        = 0x01021997
	cephSuperMagic   = 0x00C36400
	afsFsMagic       = 0x6B414653
	lustreSuperMagic = 0x0BD00BD0
)

// fsTypeName maps Linux's magic number onto the names isNetworkFSName knows.
//
// FUSE is deliberately not treated as a network filesystem: it is a transport,
// and most of what runs on it is local. Calling every FUSE mount unsafe would
// make the check noisy enough to be turned off.
func fsTypeName(st *unix.Statfs_t) string {
	// Compared against untyped constants rather than converted: Statfs_t.Type
	// is int64 on amd64 and arm64 but not on every architecture, and a
	// conversion that is redundant on one is a lint failure there.
	switch st.Type {
	case nfsSuperMagic:
		return "nfs"
	case smbSuperMagic:
		return "smbfs"
	case cifsMagicNumber:
		return "cifs"
	case smb2MagicNumber:
		return "smb2"
	case v9fsMagic:
		return "9p"
	case cephSuperMagic:
		return "ceph"
	case afsFsMagic:
		return "afs"
	case lustreSuperMagic:
		return "lustre"
	case fuseSuperMagic:
		return "fuse"
	default:
		return "local"
	}
}
