package fs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/storage"
	"github.com/Gu1llaum-3/koffr/internal/storage/fs"
	"github.com/Gu1llaum-3/koffr/internal/storage/storagetest"
)

// The whole contract, against a filesystem. Nothing here is specific to fs:
// storage/s3 runs the identical suite.
func TestContract(t *testing.T) {
	storagetest.Suite(t, func(t *testing.T) storage.Storage {
		s, err := fs.New(t.TempDir())
		require.NoError(t, err)
		return s
	})
}

// A filesystem repository is something people look at with ls, and a purge that
// leaves thousands of empty directories behind answers "did it work" with no.
// S3 has no directories, so this is not in the shared contract.
func TestDelete_RemovesTheDirectoriesItEmptied(t *testing.T) {
	dir := t.TempDir()
	st, err := fs.New(dir)
	require.NoError(t, err)

	const key = "sources/prod/logical/01ABC/dump.pgdump.zst.age"
	_, err = st.Put(t.Context(), key, strings.NewReader("payload"), storage.PutOptions{})
	require.NoError(t, err)
	require.NoError(t, st.Delete(t.Context(), key))

	_, err = os.Stat(filepath.Join(dir, "sources", "prod", "logical", "01ABC"))
	assert.True(t, os.IsNotExist(err), "the backup's directory should be gone with its objects")

	// And the repository root survives, or the next backup has nowhere to go.
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

// A directory holding another backup's objects is not empty and must stay.
func TestDelete_LeavesADirectoryThatStillHoldsSomething(t *testing.T) {
	dir := t.TempDir()
	st, err := fs.New(dir)
	require.NoError(t, err)

	for _, key := range []string{
		"sources/prod/logical/01ABC/dump.pgdump.zst.age",
		"sources/prod/logical/01ABC/manifest.json",
	} {
		_, err := st.Put(t.Context(), key, strings.NewReader("x"), storage.PutOptions{})
		require.NoError(t, err)
	}
	require.NoError(t, st.Delete(t.Context(), "sources/prod/logical/01ABC/dump.pgdump.zst.age"))

	_, err = os.Stat(filepath.Join(dir, "sources", "prod", "logical", "01ABC"))
	require.NoError(t, err, "the manifest is still there, so the directory has to be")
}
