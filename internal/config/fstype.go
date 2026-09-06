package config

import (
	"os"
	"path/filepath"
	"strings"
)

// ENF-033: the catalog must never live on a network filesystem.
//
// SQLite's locking is advisory and depends on the filesystem honouring it. NFS
// and the SMB family do not do so reliably, and the failure is not an error but
// a corrupted database, discovered when someone needs the catalog most. The
// documentation says so; a check says it at a time when it can still be acted
// on.
//
// Detection is best effort by nature -- a container can hide its mounts, and a
// FUSE layer can look local. So a negative answer means "no reason to object",
// never "proven safe".
var networkFilesystems = []string{
	"nfs", "nfs3", "nfs4", "smbfs", "cifs", "smb2", "afpfs",
	"fuse.sshfs", "fuse.davfs", "fuse.glusterfs", "9p", "afs", "lustre", "ceph",
}

// isNetworkFSName classifies a filesystem type name.
//
// Split from the syscall so the judgement can be tested: mounting an NFS share
// inside a unit test is not possible, and a check nobody can exercise is a
// check nobody can trust.
func isNetworkFSName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	for _, known := range networkFilesystems {
		if name == known {
			return true
		}
	}
	// A FUSE filesystem is only suspect when it names a network backend, which
	// the entries above already cover; a bare "fuse" says nothing.
	return false
}

// checkCatalogFilesystem adds a problem when the catalog would sit on a
// filesystem SQLite cannot lock reliably.
func checkCatalogFilesystem(v *validator, path string) {
	// The file may not exist yet on a first run, so the directory that will
	// hold it is what gets inspected.
	target := path
	if _, err := os.Stat(target); err != nil {
		target = filepath.Dir(target)
		if _, err := os.Stat(target); err != nil {
			// Nothing to inspect. The directory being absent is a separate
			// problem, and one the first write will report clearly.
			return
		}
	}

	name, ok := filesystemName(target)
	if !ok {
		return
	}
	if isNetworkFSName(name) {
		v.add("catalog.path",
			"the catalog would sit on a "+name+" filesystem, where SQLite's locking is "+
				"unreliable and the database will eventually be corrupted",
			"put it on local or block storage -- in Kubernetes a ReadWriteOnce block "+
				"volume, which follows the pod across nodes. The catalog is only a "+
				"cache: `koffr catalog sync` rebuilds it from the repository, so this "+
				"costs nothing but a restart")
	}
}
