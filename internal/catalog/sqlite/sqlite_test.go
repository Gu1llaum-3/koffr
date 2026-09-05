package sqlite_test

import (
	gosql "database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/catalog/catalogtest"
	"github.com/Gu1llaum-3/koffr/internal/catalog/sqlite"
)

func TestContract(t *testing.T) {
	// One file per test, reopened in place, so persistence is exercised by the
	// same suite that exercises everything else.
	paths := map[catalog.MetadataStore]string{}

	catalogtest.Suite(t, catalogtest.Harness{
		New: func(t *testing.T) catalog.MetadataStore {
			path := filepath.Join(t.TempDir(), "catalog.db")
			s, err := sqlite.Open(path)
			require.NoError(t, err)
			paths[s] = path
			t.Cleanup(func() { _ = s.Close() })
			return s
		},
		Reopen: func(t *testing.T, store catalog.MetadataStore) catalog.MetadataStore {
			path := paths[store]
			require.NotEmpty(t, path)
			require.NoError(t, store.Close())

			s, err := sqlite.Open(path)
			require.NoError(t, err)
			paths[s] = path
			t.Cleanup(func() { _ = s.Close() })
			return s
		},
	})
}

func TestOpen_CreatesAndMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")

	s, err := sqlite.Open(path)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	assert.Equal(t, sqlite.SchemaVersion, readUserVersion(t, path))
}

// Opening a catalog that is already at the current version must change
// nothing. A migration that ran twice would be a migration nobody could trust
// to be safe.
func TestOpen_IsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")

	for range 3 {
		s, err := sqlite.Open(path)
		require.NoError(t, err)
		require.NoError(t, s.RecordBackup(t.Context(), catalog.Backup{
			ID: "B1", SourceID: "prod", Kind: "logical", Status: catalog.StatusCompleted,
		}))
		require.NoError(t, s.Close())
	}

	s, err := sqlite.Open(path)
	require.NoError(t, err)
	defer func() { assert.NoError(t, s.Close()) }()

	got, err := s.ListBackups(t.Context(), catalog.BackupFilter{})
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, sqlite.SchemaVersion, readUserVersion(t, path))
}

// A catalog written by a newer Koffr must be refused, not read.
//
// Reading it would appear to work and quietly ignore whatever the newer version
// added, which for a catalog means losing track of backups that exist. The
// operator can delete it and let it rebuild from the repository (EF-142); that
// costs time, and guessing costs data.
func TestOpen_RefusesANewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	s, err := sqlite.Open(path)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	setUserVersion(t, path, sqlite.SchemaVersion+1)

	_, err = sqlite.Open(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "newer")
	assert.Contains(t, err.Error(), "catalog sync",
		"the error should say how to recover, not merely that it refused")
}

func TestOpen_RejectsAnEmptyPath(t *testing.T) {
	_, err := sqlite.Open("")
	require.Error(t, err)
}

// The file holds no backup content, but it does say which databases exist and
// when each was last backed up. On a shared host that is worth keeping to
// ourselves.
func TestOpen_FileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	s, err := sqlite.Open(path)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	assert.Equal(t, "-rw-------", statMode(t, path))
}

func readUserVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := gosql.Open("sqlite", path)
	require.NoError(t, err)
	defer func() { assert.NoError(t, db.Close()) }()

	var version int
	require.NoError(t, db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version))
	return version
}

func setUserVersion(t *testing.T, path string, version int) {
	t.Helper()
	db, err := gosql.Open("sqlite", path)
	require.NoError(t, err)
	defer func() { assert.NoError(t, db.Close()) }()

	_, err = db.ExecContext(t.Context(), "PRAGMA user_version = "+itoa(version))
	require.NoError(t, err)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func statMode(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return info.Mode().Perm().String()
}
