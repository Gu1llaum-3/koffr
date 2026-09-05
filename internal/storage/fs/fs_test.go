package fs_test

import (
	"testing"

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
