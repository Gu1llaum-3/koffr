package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// Archive is one file a destination holds. It is the catalogue's own vocabulary
// on purpose: `AR-03` keeps the modules of the domain from knowing each other,
// so this is **not** the entry type that `backup` declares for its own port
// (`N-2`).
type Archive struct {
	Path  string
	Bytes int64
	At    time.Time
}

// ErrNoManifest is what a repository answers for an archive that has none. An
// archive written before the lot 3 has no manifest, and that is an answer, not
// a failure.
var ErrNoManifest = errors.New("no manifest lies beside this archive")

// ErrNoSuchDestination names a destination the configuration never declared.
// `A-27`: answering "nothing" to a typo reads exactly like an empty repository,
// and an operator cannot tell the two apart.
var ErrNoSuchDestination = errors.New("no destination has this identifier")

// Repository is what a destination **really** holds, as opposed to what the
// catalogue remembers. The two are not the same thing, and ADR-0006 says which
// of them is the truth: this one.
type Repository interface {
	// Archives lists what lies under a prefix. An empty prefix is the whole
	// destination.
	Archives(ctx context.Context, prefix string) ([]Archive, error)

	// ReadManifest returns, raw, the manifest deposited beside an archive, or
	// ErrNoManifest. It hands over bytes and not a Manifest: the shape of a
	// manifest belongs here, and an adapter has no business knowing it.
	ReadManifest(ctx context.Context, archive string) ([]byte, error)
}

// Destination is one place archives live, with the identifier the operator gave
// it in the configuration.
type Destination struct {
	ID         string
	Repository Repository
}

// Listed is one line of `koffr list`.
type Listed struct {
	Backup

	// Catalogued says whether koffr has a record of this archive. An archive
	// written by an earlier release has none — and it is shown anyway (`N-1`,
	// `A-19`), because an archive nobody mentions is worse than one nobody
	// checked.
	Catalogued bool

	// Present says whether the destination still holds the file. A catalogue
	// entry whose archive has gone is shown, and says so.
	Present bool
}

// Lister answers `koffr list`: what the catalogue indexed, **merged with** what
// the destinations hold.
//
// The merge goes one way only. Nothing found on a destination is written to the
// catalogue: the catalogue stays the trace of what this koffr did, and the
// destination stays the truth of the repository (ADR-0006, `N-1`).
type Lister struct {
	Catalog      Catalog
	Destinations []Destination
}

// List merges the two, most recent first.
func (l Lister) List(ctx context.Context, filter Filter) ([]Listed, error) {
	destinations, err := l.chosen(filter.Destination)
	if err != nil {
		return nil, err
	}

	indexed, err := l.Catalog.Backups(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("read the catalogue: %w", err)
	}

	listed := make(map[string]*Listed, len(indexed))
	order := make([]string, 0, len(indexed))

	for _, one := range indexed {
		listed[one.ID] = &Listed{Backup: one, Catalogued: true}
		order = append(order, one.ID)
	}

	for _, destination := range destinations {
		found, err := destination.Repository.Archives(ctx, prefixOf(filter.Database))
		if err != nil {
			return nil, fmt.Errorf("list the destination %s: %w", destination.ID, err)
		}

		for _, archive := range found {
			identity, ok := identify(archive.Path)
			if !ok {
				continue
			}

			if known, seen := listed[identity.id]; seen {
				known.Present = true

				continue
			}

			stranger := l.describe(ctx, destination, archive, identity)
			listed[identity.id] = &stranger
			order = append(order, identity.id)
		}
	}

	return sorted(listed, order, filter), nil
}

// chosen narrows to one destination, and refuses an identifier the
// configuration never declared (`CAT-07`).
func (l Lister) chosen(wanted string) ([]Destination, error) {
	if wanted == "" {
		return l.Destinations, nil
	}

	for _, destination := range l.Destinations {
		if destination.ID == wanted {
			return []Destination{destination}, nil
		}
	}

	names := make([]string, 0, len(l.Destinations))
	for _, destination := range l.Destinations {
		names = append(names, destination.ID)
	}

	return nil, fmt.Errorf("%w: %q; the configuration declares %s",
		ErrNoSuchDestination, wanted, strings.Join(names, ", "))
}

// describe builds the line of an archive the catalogue ignores. The manifest
// fills in what the file name cannot say, when it is there; the verification
// is **not** one of those fields, because koffr has no record of having checked
// this archive and `E-064` forbids a blank that reads like a success.
func (l Lister) describe(
	ctx context.Context, destination Destination, archive Archive, identity archiveIdentity,
) Listed {
	line := Listed{
		Backup: Backup{
			ID: identity.id, Database: identity.database,
			StartedAt: identity.at, StoredBytes: archive.Bytes,
			Verified: NotVerified,
		},
		Present: true,
	}

	if raw, err := destination.Repository.ReadManifest(ctx, archive.Path); err == nil {
		var manifest Manifest
		if json.Unmarshal(raw, &manifest) == nil {
			described := manifest.Backup()
			described.Verified, described.VerifiedAt = NotVerified, time.Time{}

			if described.StoredBytes == 0 {
				described.StoredBytes = archive.Bytes
			}

			line.Backup = described
		}
	}

	line.Locations = []Location{{
		Destination: destination.ID, Path: archive.Path,
		Bytes: archive.Bytes, StoredAt: archive.At,
	}}

	return line
}

// sorted renders the merge in a fixed order: most recent first, and the
// identifier breaks a tie so that two runs never disagree.
func sorted(listed map[string]*Listed, order []string, filter Filter) []Listed {
	found := make([]Listed, 0, len(order))

	for _, id := range order {
		one := listed[id]
		if filter.Database != "" && one.Database != filter.Database {
			continue
		}

		found = append(found, *one)
	}

	sort.SliceStable(found, func(a, b int) bool {
		if found[a].StartedAt.Equal(found[b].StartedAt) {
			return found[a].ID > found[b].ID
		}

		return found[a].StartedAt.After(found[b].StartedAt)
	})

	if filter.Limit > 0 && len(found) > filter.Limit {
		found = found[:filter.Limit]
	}

	return found
}

// prefixOf is where a database's archives live, by the deterministic path of
// F5.5: `<database>/<YYYY>/<MM>/…`.
func prefixOf(database string) string {
	if database == "" {
		return ""
	}

	return database + "/"
}

// archiveIdentity is what the name of an archive says about it, which is all
// there is to go on when no manifest lies beside it.
type archiveIdentity struct {
	database string
	id       string
	at       time.Time
}

// identify reads `<database>/<YYYY>/<MM>/<database>_<timestamp>_<id>.<ext>`,
// the deterministic path of F5.5 — written so that a repository stays usable
// when the agent disappears, which is exactly the situation here.
func identify(archive string) (archiveIdentity, bool) {
	name := path.Base(archive)

	// A manifest describes an archive; it is not one.
	if strings.HasSuffix(name, ".json") {
		return archiveIdentity{}, false
	}

	stem, _, found := strings.Cut(name, ".")
	if !found {
		return archiveIdentity{}, false
	}

	cut := strings.LastIndex(stem, "_")
	if cut < 0 {
		return archiveIdentity{}, false
	}

	identity := archiveIdentity{id: stem[cut+1:]}

	rest := stem[:cut]
	if cut = strings.LastIndex(rest, "_"); cut < 0 {
		return archiveIdentity{}, false
	}

	identity.database = rest[:cut]

	if moment, err := time.Parse("20060102T150405Z", rest[cut+1:]); err == nil {
		identity.at = moment
	}

	if identity.id == "" || identity.database == "" {
		return archiveIdentity{}, false
	}

	return identity, true
}
