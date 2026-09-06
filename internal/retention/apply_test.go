package retention_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/catalog/sqlite"
	"github.com/Gu1llaum-3/koffr/internal/retention"
	"github.com/Gu1llaum-3/koffr/internal/storage"
	"github.com/Gu1llaum-3/koffr/internal/storage/memory"
)

// repo builds a repository holding one backup per id, and a catalog to match.
func repo(t *testing.T, ids ...string) (*memory.Storage, catalog.MetadataStore) {
	t.Helper()
	st := memory.New()
	cat, err := sqlite.Open(t.Context(), t.TempDir()+"/catalog.db")
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, cat.Close()) })

	src, err := storage.ForSource("prod")
	require.NoError(t, err)

	for i, id := range ids {
		b, err := src.Backup(storage.DirLogical, id)
		require.NoError(t, err)
		for name, body := range map[string]string{
			"dump.pgdump.zst.age": "payload for " + id,
			"RESTORE.md":          "procedure",
			storage.ManifestFile:  `{"backup_id":"` + id + `"}`,
		} {
			_, err := st.Put(t.Context(), b.Prefix()+name,
				bytes.NewReader([]byte(body)), storage.PutOptions{})
			require.NoError(t, err)
		}
		require.NoError(t, cat.RecordBackup(t.Context(), catalog.Backup{
			ID: catalog.ID(id), SourceID: "prod", Kind: "logical",
			Status: catalog.StatusCompleted, StartedAt: now.Add(-time.Duration(i) * 24 * time.Hour),
		}))
	}
	return st, cat
}

func keys(t *testing.T, st *memory.Storage, prefix string) []string {
	t.Helper()
	var out []string
	for info, err := range st.List(context.Background(), prefix) {
		require.NoError(t, err)
		out = append(out, info.Key)
	}
	return out
}

func TestApply_DeletesTheObjectsAndTheRow(t *testing.T) {
	st, cat := repo(t, "01NEWEST00000000000000000A", "01OLDER000000000000000000B")

	backups, err := cat.ListBackups(t.Context(), catalog.BackupFilter{})
	require.NoError(t, err)
	plan, err := retention.Plan(backups, retention.Policy{KeepLast: 1}, now)
	require.NoError(t, err)

	applied, err := retention.Apply(t.Context(), st, cat, plan)
	require.NoError(t, err)
	assert.Equal(t, []catalog.ID{"01OLDER000000000000000000B"}, applied.Deleted)
	assert.Positive(t, applied.FreedBytes)

	assert.Empty(t, keys(t, st, "sources/prod/logical/01OLDER000000000000000000B/"),
		"the objects are what cost money; the row is only bookkeeping")
	assert.Len(t, keys(t, st, "sources/prod/logical/01NEWEST00000000000000000A/"), 3)

	remaining, err := cat.ListBackups(t.Context(), catalog.BackupFilter{})
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	assert.Equal(t, catalog.ID("01NEWEST00000000000000000A"), remaining[0].ID)
}

// A plan that deletes nothing must touch nothing. The commonest run of a purge
// is the one with nothing to do, and it is the one that must be harmless.
func TestApply_APlanWithNoDeletions(t *testing.T) {
	st, cat := repo(t, "01ONLY0000000000000000000A")

	backups, _ := cat.ListBackups(t.Context(), catalog.BackupFilter{})
	plan, err := retention.Plan(backups, retention.Policy{KeepLast: 5}, now)
	require.NoError(t, err)

	applied, err := retention.Apply(t.Context(), st, cat, plan)
	require.NoError(t, err)
	assert.Empty(t, applied.Deleted)
	assert.Len(t, keys(t, st, "sources/"), 3)
}

// Re-running after an interruption has to be safe: the objects are gone, the
// row may not be, and the pass should finish the job rather than refuse it.
func TestApply_IsSafeToRunTwice(t *testing.T) {
	st, cat := repo(t, "01NEWEST00000000000000000A", "01OLDER000000000000000000B")

	backups, _ := cat.ListBackups(t.Context(), catalog.BackupFilter{})
	plan, err := retention.Plan(backups, retention.Policy{KeepLast: 1}, now)
	require.NoError(t, err)

	_, err = retention.Apply(t.Context(), st, cat, plan)
	require.NoError(t, err)

	// The same plan again, against a repository where the work is already done.
	_, err = retention.Apply(t.Context(), st, cat, plan)
	require.NoError(t, err)
}

// The manifest goes first, and that ordering is ENF-010 run backwards: its
// presence is what makes a set of objects a backup, so an interrupted pass
// leaves litter rather than something a later reader would try to restore.
func TestApply_RemovesTheManifestFirst(t *testing.T) {
	st, cat := repo(t, "01NEWEST00000000000000000A", "01OLDER000000000000000000B")
	order := &recordingStorage{Storage: st}

	backups, _ := cat.ListBackups(t.Context(), catalog.BackupFilter{})
	plan, err := retention.Plan(backups, retention.Policy{KeepLast: 1}, now)
	require.NoError(t, err)

	_, err = retention.Apply(t.Context(), order, cat, plan)
	require.NoError(t, err)

	require.NotEmpty(t, order.deleted)
	assert.Contains(t, order.deleted[0], storage.ManifestFile,
		"an interrupted pass must not leave a manifest pointing at objects that are gone")
}

// A destination that refuses one prefix will probably refuse the next, but the
// ones that succeeded are space actually reclaimed. The errors come back
// together rather than at the first stumble.
func TestApply_ReportsFailuresWithoutStoppingTheWholePass(t *testing.T) {
	st, cat := repo(t,
		"01NEWEST00000000000000000A", "01MIDDLE00000000000000000B", "01OLDEST00000000000000000C")

	backups, _ := cat.ListBackups(t.Context(), catalog.BackupFilter{})
	plan, err := retention.Plan(backups, retention.Policy{KeepLast: 1}, now)
	require.NoError(t, err)

	refusing := &refusingStorage{Storage: st, refuse: "01MIDDLE00000000000000000B"}
	applied, err := retention.Apply(t.Context(), refusing, cat, plan)
	require.Error(t, err)
	assert.Equal(t, []catalog.ID{"01OLDEST00000000000000000C"}, applied.Deleted,
		"the one that worked has to count")
}

type recordingStorage struct {
	storage.Storage
	deleted []string
}

func (s *recordingStorage) Delete(ctx context.Context, key string) error {
	s.deleted = append(s.deleted, key)
	return s.Storage.Delete(ctx, key)
}

type refusingStorage struct {
	storage.Storage
	refuse string
}

func (s *refusingStorage) Delete(ctx context.Context, key string) error {
	if bytes.Contains([]byte(key), []byte(s.refuse)) {
		return assert.AnError
	}
	return s.Storage.Delete(ctx, key)
}

// The manifest-first guarantee, checked at a size where an invalid sort
// comparator would have had room to misbehave. Nine objects is more than a
// backup has, and more than the threshold where Go's sort changes strategy.
func TestApply_ManifestFirstAtEveryPosition(t *testing.T) {
	for position := range 9 {
		t.Run(fmt.Sprintf("manifest at %d", position), func(t *testing.T) {
			st := memory.New()
			cat, err := sqlite.Open(t.Context(), t.TempDir()+"/catalog.db")
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, cat.Close()) })

			src, err := storage.ForSource("prod")
			require.NoError(t, err)
			keep, err := src.Backup(storage.DirLogical, "01KEEP0000000000000000000A")
			require.NoError(t, err)
			drop, err := src.Backup(storage.DirLogical, "01DROP0000000000000000000B")
			require.NoError(t, err)

			for i := range 9 {
				name := fmt.Sprintf("obj-%02d.age", i)
				if i == position {
					name = storage.ManifestFile
				}
				_, err := st.Put(t.Context(), drop.Prefix()+name,
					bytes.NewReader([]byte("x")), storage.PutOptions{})
				require.NoError(t, err)
			}
			_, err = st.Put(t.Context(), keep.Prefix()+storage.ManifestFile,
				bytes.NewReader([]byte("x")), storage.PutOptions{})
			require.NoError(t, err)

			for i, id := range []string{"01KEEP0000000000000000000A", "01DROP0000000000000000000B"} {
				require.NoError(t, cat.RecordBackup(t.Context(), catalog.Backup{
					ID: catalog.ID(id), SourceID: "prod", Kind: "logical",
					Status: catalog.StatusCompleted, StartedAt: now.Add(-time.Duration(i) * time.Hour),
				}))
			}

			backups, _ := cat.ListBackups(t.Context(), catalog.BackupFilter{})
			plan, err := retention.Plan(backups, retention.Policy{KeepLast: 1}, now)
			require.NoError(t, err)

			order := &recordingStorage{Storage: st}
			_, err = retention.Apply(t.Context(), order, cat, plan)
			require.NoError(t, err)

			require.NotEmpty(t, order.deleted)
			assert.True(t, strings.HasSuffix(order.deleted[0], storage.ManifestFile),
				"an interrupted pass must never leave a manifest pointing at objects that are gone")
		})
	}
}
