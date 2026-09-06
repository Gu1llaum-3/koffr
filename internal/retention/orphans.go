package retention

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// Orphan is a backup prefix with no manifest.
//
// The manifest is the point of no return (ENF-010): its presence is what makes
// a set of objects a backup. A prefix without one was never a backup, so it is
// invisible to `koffr ls`, invisible to a purge that reads the catalog, and
// paid for every month.
//
// They come from a job that died between its first upload and its manifest --
// a machine rebooting, a SIGKILL, a network that went away. The backup runner
// cleans up after itself when it can; this is for when it could not.
type Orphan struct {
	Prefix string
	Bytes  int64
	// NewestAt is the most recent object in the prefix, which is how a backup
	// in progress is told from litter left months ago.
	NewestAt time.Time
}

// FindOrphans lists prefixes with objects and no manifest.
//
// It reads the repository and not the catalog, deliberately. The repository is
// the truth (ADR-0004), so a catalog that has lost a row must not be able to
// turn a good backup into rubbish.
func FindOrphans(ctx context.Context, st storage.Storage) ([]Orphan, error) {
	return FindOrphansOlderThan(ctx, st, 0)
}

// FindOrphansOlderThan ignores prefixes touched more recently than grace.
//
// A backup being written right now has objects and no manifest yet, which is
// exactly what litter looks like from outside. A grace period is the only thing
// that tells them apart without a lock, and deleting a job in progress would be
// a worse outcome than paying for a stale prefix another day.
func FindOrphansOlderThan(ctx context.Context, st storage.Storage, grace time.Duration) ([]Orphan, error) {
	type group struct {
		bytes    int64
		newest   time.Time
		manifest bool
	}
	groups := map[string]*group{}

	for info, err := range st.List(ctx, storage.SourcesDir+"/") {
		if err != nil {
			return nil, fmt.Errorf("retention: list the repository: %w", err)
		}
		prefix, name, found := cutLast(info.Key)
		if !found {
			continue
		}
		g, seen := groups[prefix]
		if !seen {
			g = &group{}
			groups[prefix] = g
		}
		g.bytes += info.Size
		if info.LastModified.After(g.newest) {
			g.newest = info.LastModified
		}
		if name == storage.ManifestFile {
			g.manifest = true
		}
	}

	cutoff := time.Now().Add(-grace)
	var out []Orphan
	for prefix, g := range groups {
		if g.manifest {
			continue
		}
		if grace > 0 && g.newest.After(cutoff) {
			continue
		}
		out = append(out, Orphan{Prefix: prefix, Bytes: g.bytes, NewestAt: g.newest})
	}
	return out, nil
}

// RemoveOrphans deletes them and reports the bytes reclaimed.
func RemoveOrphans(ctx context.Context, st storage.Storage, orphans []Orphan) (int64, error) {
	var freed int64
	var failures []error

	for _, o := range orphans {
		n, err := deletePrefix(ctx, st, o.Prefix)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		freed += n
	}
	if len(failures) > 0 {
		return freed, errors.Join(failures...)
	}
	return freed, nil
}

// cutLast splits a key into its prefix and its filename.
func cutLast(key string) (prefix, name string, found bool) {
	i := strings.LastIndex(key, "/")
	if i < 0 {
		return "", "", false
	}
	return key[:i+1], key[i+1:], true
}
