package retention_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/retention"
	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// put writes one object under a backup prefix.
func put(t *testing.T, st storage.Storage, source, backupID, name, body string) {
	t.Helper()
	src, err := storage.ForSource(source)
	require.NoError(t, err)
	b, err := src.Backup(storage.DirLogical, backupID)
	require.NoError(t, err)
	_, err = st.Put(t.Context(), b.Prefix()+name, bytes.NewReader([]byte(body)), storage.PutOptions{})
	require.NoError(t, err)
}

// A backup interrupted before its manifest leaves objects nothing points at:
// invisible to `koffr ls`, invisible to the purge, which only reads the
// catalog, and paid for every month. The manifest is the point of no return
// (ENF-010), so a prefix without one was never a backup.
func TestFindOrphans_APrefixWithNoManifest(t *testing.T) {
	st, _ := repo(t, "01GOOD0000000000000000000A")
	put(t, st, "prod", "01LITTER00000000000000000B", "dump.pgdump.zst.age", "half an upload")
	put(t, st, "prod", "01LITTER00000000000000000B", "RESTORE.md", "procedure")

	orphans, err := retention.FindOrphans(t.Context(), st)
	require.NoError(t, err)
	require.Len(t, orphans, 1)
	assert.Contains(t, orphans[0].Prefix, "01LITTER00000000000000000B")
	assert.Positive(t, orphans[0].Bytes)
}

// A complete backup is never litter, whatever the catalog says about it. This
// runs against the repository, and the repository is the truth (ADR-0004): a
// catalog that has lost a row must not turn a good backup into rubbish.
func TestFindOrphans_LeavesCompleteBackupsAlone(t *testing.T) {
	st, _ := repo(t, "01GOOD0000000000000000000A", "01ALSO0000000000000000000B")

	orphans, err := retention.FindOrphans(t.Context(), st)
	require.NoError(t, err)
	assert.Empty(t, orphans,
		"a backup with a manifest is a backup, whether or not the catalog remembers it")
}

// The catalog replica and the repository descriptor live outside sources/ and
// are not backups. Sweeping them would delete the thing a rebuild reads.
func TestFindOrphans_IgnoresWhatIsNotABackup(t *testing.T) {
	st, _ := repo(t, "01GOOD0000000000000000000A")
	_, err := st.Put(t.Context(), storage.CatalogLatestKey(),
		bytes.NewReader([]byte("replica")), storage.PutOptions{})
	require.NoError(t, err)
	_, err = st.Put(t.Context(), storage.DescriptorKey(),
		bytes.NewReader([]byte("{}")), storage.PutOptions{})
	require.NoError(t, err)

	orphans, err := retention.FindOrphans(t.Context(), st)
	require.NoError(t, err)
	assert.Empty(t, orphans)
}

func TestRemoveOrphans(t *testing.T) {
	st, _ := repo(t, "01GOOD0000000000000000000A")
	put(t, st, "prod", "01LITTER00000000000000000B", "dump.pgdump.zst.age", "half an upload")

	orphans, err := retention.FindOrphans(t.Context(), st)
	require.NoError(t, err)

	freed, err := retention.RemoveOrphans(t.Context(), st, orphans)
	require.NoError(t, err)
	assert.Positive(t, freed)

	assert.Empty(t, keys(t, st, "sources/prod/logical/01LITTER00000000000000000B/"))
	assert.Len(t, keys(t, st, "sources/prod/logical/01GOOD0000000000000000000A/"), 3,
		"the good backup must not be touched")
}

func TestFindOrphans_EmptyRepository(t *testing.T) {
	st, _ := repo(t)
	orphans, err := retention.FindOrphans(t.Context(), st)
	require.NoError(t, err)
	assert.Empty(t, orphans)
}

// A backup being written right now has objects and no manifest yet, which is
// exactly what litter looks like. Deleting it would destroy a job in progress,
// so anything recent is left alone.
func TestFindOrphans_LeavesRecentPrefixesAlone(t *testing.T) {
	st, _ := repo(t, "01GOOD0000000000000000000A")
	put(t, st, "prod", "01INFLIGHT000000000000000C", "dump.pgdump.zst.age", "being written")

	orphans, err := retention.FindOrphansOlderThan(t.Context(), st, time.Hour)
	require.NoError(t, err)
	assert.Empty(t, orphans,
		"a backup in progress has objects and no manifest, and deleting it would destroy a running job")
}
