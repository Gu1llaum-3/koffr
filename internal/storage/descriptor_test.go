package storage_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/storage"
	"github.com/Gu1llaum-3/koffr/internal/storage/memory"
)

var at = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

func TestOpenRepository_WritesTheDescriptorOnce(t *testing.T) {
	st := memory.New()

	first, err := storage.OpenRepository(t.Context(), st, "repo-a", "1.0.0", at)
	require.NoError(t, err)
	assert.Equal(t, storage.FormatVersion, first.FormatVersion)
	assert.Equal(t, "repo-a", first.RepositoryID)

	// A second run must adopt what is there rather than stamping its own id
	// over it: the descriptor is what says two repositories are not the same
	// repository.
	second, err := storage.OpenRepository(t.Context(), st, "repo-b", "2.0.0", at.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, "repo-a", second.RepositoryID)
	assert.Equal(t, "1.0.0", second.CreatedBy)
	assert.True(t, at.Equal(second.CreatedAt))
}

// EF-043 is only worth having if it refuses. An older Koffr that read a newer
// repository and ignored what it did not understand would report a partial view
// as a complete one, which is how retention deletes a backup it never saw.
func TestOpenRepository_RefusesANewerFormat(t *testing.T) {
	st := memory.New()
	body := []byte(`{"format_version":99,"repository_id":"from-the-future","created_at":"2026-03-01T10:00:00Z"}`)
	_, err := st.Put(t.Context(), storage.DescriptorKey(), bytes.NewReader(body), storage.PutOptions{})
	require.NoError(t, err)

	_, err = storage.OpenRepository(t.Context(), st, "mine", "1.0.0", at)
	require.ErrorIs(t, err, storage.ErrRepositoryTooNew)
	assert.Contains(t, err.Error(), "99")
}

func TestOpenRepository_RefusesADescriptorItCannotRead(t *testing.T) {
	st := memory.New()
	_, err := st.Put(t.Context(), storage.DescriptorKey(),
		bytes.NewReader([]byte("{ truncated")), storage.PutOptions{})
	require.NoError(t, err)

	_, err = storage.OpenRepository(t.Context(), st, "mine", "1.0.0", at)
	require.Error(t, err)
	assert.Contains(t, err.Error(), storage.DescriptorFile)
}
