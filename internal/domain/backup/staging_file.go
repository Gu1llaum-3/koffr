package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// stagingDirectory is the buffer of § 4.5, under the state directory so that it
// lives on the volume koffr was given rather than on whatever /tmp happens to
// be. E-026 names it.
const stagingDirectory = "tmp"

// stagingPrefix and stagingSuffix frame a name koffr recognises as its own.
const (
	stagingPrefix = "staging-"
	stagingSuffix = ".koffr"
)

// StagingFileName is how a buffer is named: it carries the **pid of the job
// that owns it**, which is what lets the next job tell an abandoned buffer from
// one being written right now.
//
// That matters because the lock of E-051 is per **database**: two jobs on two
// databases run at the same time, and a purge that swept the directory would
// delete the other one's buffer underneath it (`N-4`).
func StagingFileName(pid int, job string) string {
	return fmt.Sprintf("%s%d-%s%s", stagingPrefix, pid, safeSegment(job), stagingSuffix)
}

// OwnerOfStagingFile reads the pid back out of a name, and says whether the
// file is one of koffr's at all. A file koffr did not write is left alone.
func OwnerOfStagingFile(name string) (int, bool) {
	if !strings.HasPrefix(name, stagingPrefix) || !strings.HasSuffix(name, stagingSuffix) {
		return 0, false
	}

	rest := strings.TrimPrefix(name, stagingPrefix)

	owner, _, found := strings.Cut(rest, "-")
	if !found {
		return 0, false
	}

	pid, err := strconv.Atoi(owner)
	if err != nil || pid <= 0 {
		return 0, false
	}

	return pid, true
}

// purgeStaging removes the buffers of jobs that no longer exist.
//
// `internal/state` purges this directory when it opens the local state (E-026),
// but a backup never opens it, so nothing was collecting them: a job killed
// mid-stream left its buffer for ever, and a machine where jobs get interrupted
// filled its disk — which is what E-061 exists to prevent (A-16).
func purgeStaging(stateDirectory string) error {
	directory := filepath.Join(stateDirectory, stagingDirectory)

	entries, err := os.ReadDir(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return fmt.Errorf("read the staging directory %s: %w", directory, err)
	}

	for _, entry := range entries {
		owner, ours := OwnerOfStagingFile(entry.Name())
		if !ours || running(owner) {
			continue
		}

		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove the abandoned buffer %s: %w", entry.Name(), err)
		}
	}

	return nil
}
