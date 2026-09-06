package retention_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/retention"
	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// leaky is a repository that can lose an upload, which a filesystem cannot.
// It embeds a real store so everything else behaves normally.
type leaky struct {
	storage.Storage
	uploads []storage.IncompleteUpload
	aborted []string
	listErr error
}

func (l *leaky) ListIncompleteUploads(context.Context, string) ([]storage.IncompleteUpload, error) {
	if l.listErr != nil {
		return nil, l.listErr
	}
	return l.uploads, nil
}

func (l *leaky) AbortIncompleteUpload(_ context.Context, u storage.IncompleteUpload) error {
	l.aborted = append(l.aborted, u.UploadID)
	return nil
}

func TestFindIncompleteUploads(t *testing.T) {
	t.Run("a store with nothing to leak reports nothing, not an error", func(t *testing.T) {
		// A filesystem has no multipart uploads to lose. Asking it must be
		// quiet rather than an error an operator has to learn to ignore.
		st, _ := repo(t, "01GOOD0000000000000000000A")
		found, err := retention.FindIncompleteUploadsOlderThan(t.Context(), st, time.Hour)
		require.NoError(t, err)
		assert.Empty(t, found)
	})

	t.Run("litter past the grace period is reported", func(t *testing.T) {
		st, _ := repo(t, "01GOOD0000000000000000000A")
		l := &leaky{Storage: st, uploads: []storage.IncompleteUpload{
			{Key: "sources/prod/logical/01DEAD/dump.zst.age", UploadID: "u-old",
				Initiated: time.Now().Add(-48 * time.Hour)},
		}}
		found, err := retention.FindIncompleteUploadsOlderThan(t.Context(), l, 24*time.Hour)
		require.NoError(t, err)
		require.Len(t, found, 1)
		assert.Equal(t, "u-old", found[0].UploadID)
	})

	t.Run("an upload in flight is left alone", func(t *testing.T) {
		// The one mistake that cannot be undone: aborting the upload of a
		// backup that is running right now kills it. From outside, a running
		// job and litter are the same thing, and only age tells them apart.
		st, _ := repo(t, "01GOOD0000000000000000000A")
		l := &leaky{Storage: st, uploads: []storage.IncompleteUpload{
			{Key: "sources/prod/logical/01LIVE/dump.zst.age", UploadID: "u-live",
				Initiated: time.Now().Add(-5 * time.Minute)},
			{Key: "sources/prod/logical/01DEAD/dump.zst.age", UploadID: "u-old",
				Initiated: time.Now().Add(-48 * time.Hour)},
		}}
		found, err := retention.FindIncompleteUploadsOlderThan(t.Context(), l, 24*time.Hour)
		require.NoError(t, err)
		require.Len(t, found, 1)
		assert.Equal(t, "u-old", found[0].UploadID, "a running backup was about to be aborted")
	})

	t.Run("an upload with no start time is left alone", func(t *testing.T) {
		// A service that does not say when an upload began cannot be reasoned
		// about, and the safe reading of "unknown age" is "possibly running".
		st, _ := repo(t, "01GOOD0000000000000000000A")
		l := &leaky{Storage: st, uploads: []storage.IncompleteUpload{
			{Key: "sources/prod/logical/01ODD/dump.zst.age", UploadID: "u-unknown"},
		}}
		found, err := retention.FindIncompleteUploadsOlderThan(t.Context(), l, 24*time.Hour)
		require.NoError(t, err)
		assert.Empty(t, found)
	})

	t.Run("a listing that fails is reported, not swallowed", func(t *testing.T) {
		st, _ := repo(t, "01GOOD0000000000000000000A")
		l := &leaky{Storage: st, listErr: errors.New("service said no")}
		_, err := retention.FindIncompleteUploadsOlderThan(t.Context(), l, time.Hour)
		require.Error(t, err)
	})
}

func TestAbortIncompleteUploads(t *testing.T) {
	st, _ := repo(t, "01GOOD0000000000000000000A")
	l := &leaky{Storage: st}
	ups := []storage.IncompleteUpload{{UploadID: "u1"}, {UploadID: "u2"}}
	require.NoError(t, retention.AbortIncompleteUploads(t.Context(), l, ups))
	assert.Equal(t, []string{"u1", "u2"}, l.aborted)
}
