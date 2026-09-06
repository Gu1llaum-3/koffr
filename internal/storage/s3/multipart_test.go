package s3_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/storage"
	koffrs3 "github.com/Gu1llaum-3/koffr/internal/storage/s3"
)

// leak starts a multipart upload and walks away from it, which is what a job
// killed mid-transfer leaves on the service. Returns the service key used.
func leak(t *testing.T, bucket, key string) {
	t.Helper()
	create, err := shared.client.CreateMultipartUpload(t.Context(), &awss3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	require.NoError(t, err)
	_, err = shared.client.UploadPart(t.Context(), &awss3.UploadPartInput{
		Bucket: aws.String(bucket), Key: aws.String(key), UploadId: create.UploadId,
		PartNumber: aws.Int32(1), Body: bytes.NewReader(bytes.Repeat([]byte("x"), 5<<20)),
	})
	require.NoError(t, err)
}

func TestIncompleteUploads(t *testing.T) {
	if shared.skipWhy != "" {
		t.Skip(shared.skipWhy)
	}

	t.Run("a killed upload is invisible to List but reported here", func(t *testing.T) {
		bucket := newBucket(t)
		st, err := koffrs3.New(t.Context(), shared.client, koffrs3.Config{Bucket: bucket})
		require.NoError(t, err)

		key := "sources/shop/logical/01KILLED/dump.pgdump.zst.age"
		leak(t, bucket, key)

		// The premise. If a listing showed it, retention's orphan sweep would
		// already handle it and none of this would need to exist.
		for range st.List(t.Context(), "sources/") {
			t.Fatal("List reported an object for an upload that was never completed")
		}

		// Asserted through the interface, because that is how prune reaches it:
		// a store that leaks must say so through storage.Storage or not at all.
		var base storage.Storage = st
		m, ok := base.(storage.MultipartMaintainer)
		require.True(t, ok, "the S3 store must be able to answer for what it leaks")

		found, err := m.ListIncompleteUploads(t.Context(), "sources/")
		require.NoError(t, err)
		require.Len(t, found, 1)
		require.Equal(t, key, found[0].Key)
		require.NotEmpty(t, found[0].UploadID)
		require.WithinDuration(t, time.Now(), found[0].Initiated, time.Minute)

		require.NoError(t, m.AbortIncompleteUpload(t.Context(), found[0]))

		after, err := m.ListIncompleteUploads(t.Context(), "sources/")
		require.NoError(t, err)
		require.Empty(t, after)
	})

	t.Run("keys are repository keys, not service keys", func(t *testing.T) {
		bucket := newBucket(t)
		st, err := koffrs3.New(t.Context(), shared.client, koffrs3.Config{
			Bucket: bucket, Prefix: "tenant-a",
		})
		require.NoError(t, err)

		leak(t, bucket, "tenant-a/sources/shop/logical/01KILLED/dump.zst.age")
		// Another tenant's litter in the same bucket must stay invisible.
		leak(t, bucket, "tenant-b/sources/shop/logical/01OTHER/dump.zst.age")

		var base storage.Storage = st
		m := base.(storage.MultipartMaintainer)
		found, err := m.ListIncompleteUploads(t.Context(), "sources/")
		require.NoError(t, err)
		require.Len(t, found, 1)
		require.Equal(t, "sources/shop/logical/01KILLED/dump.zst.age", found[0].Key)

		// And aborting one addressed by its repository key must reach the right
		// object: a prefix dropped on the way back has to be put back on.
		require.NoError(t, m.AbortIncompleteUpload(t.Context(), found[0]))
		after, err := m.ListIncompleteUploads(t.Context(), "sources/")
		require.NoError(t, err)
		require.Empty(t, after)
	})

	t.Run("aborting one that is already gone is not an error", func(t *testing.T) {
		bucket := newBucket(t)
		st, err := koffrs3.New(t.Context(), shared.client, koffrs3.Config{Bucket: bucket})
		require.NoError(t, err)

		key := "sources/shop/logical/01KILLED/dump.zst.age"
		leak(t, bucket, key)
		var base storage.Storage = st
		m := base.(storage.MultipartMaintainer)
		found, err := m.ListIncompleteUploads(t.Context(), "sources/")
		require.NoError(t, err)
		require.Len(t, found, 1)

		require.NoError(t, m.AbortIncompleteUpload(t.Context(), found[0]))
		// Two operators pruning at once is not a failure. Aborting twice is not
		// enough to prove it: MinIO answers the second abort with a 204, so the
		// branch that forgives a vanished upload is never reached. An upload id
		// that never existed is what actually produces NoSuchUpload.
		require.NoError(t, m.AbortIncompleteUpload(t.Context(), found[0]))
		require.NoError(t, m.AbortIncompleteUpload(t.Context(), storage.IncompleteUpload{
			Key: key, UploadID: "an-upload-id-that-never-existed",
		}))
	})

	t.Run("a clean store answers empty rather than failing", func(t *testing.T) {
		st, err := koffrs3.New(t.Context(), shared.client, koffrs3.Config{Bucket: newBucket(t)})
		require.NoError(t, err)
		var base storage.Storage = st
		found, err := base.(storage.MultipartMaintainer).ListIncompleteUploads(t.Context(), "sources/")
		require.NoError(t, err)
		require.Empty(t, found)
	})
}

// A listing that reaches outside its own bucket is not a hypothetical: MinIO
// answers ListMultipartUploads with every upload it holds, whichever bucket is
// named. Measured, a freshly created bucket reported two thousand uploads
// belonging to other buckets before anything had been written to it.
//
// Every Koffr repository uses the same key layout, so filtering on the prefix
// alone does not tell one repository's litter from another's. Two repositories
// sharing an endpoint would each report the other's backup paths -- and, on a
// service whose abort were as loose as its listing, abort a running backup.
func TestIncompleteUploads_AnotherBucketIsNotOurs(t *testing.T) {
	if shared.skipWhy != "" {
		t.Skip(shared.skipWhy)
	}
	ours, theirs := newBucket(t), newBucket(t)
	leak(t, theirs, "sources/shop/logical/01THEIRS/dump.zst.age")

	st, err := koffrs3.New(t.Context(), shared.client, koffrs3.Config{Bucket: ours})
	require.NoError(t, err)
	var base storage.Storage = st

	found, err := base.(storage.MultipartMaintainer).ListIncompleteUploads(t.Context(), "sources/")
	require.NoError(t, err)
	require.Empty(t, found, "a sweep was about to act on another repository's upload")
}

// What the parts hold is the number that makes the report worth reading: an
// operator asked to tidy up wants to know what they are paying for.
func TestIncompleteUploads_ReportWhatTheyHold(t *testing.T) {
	if shared.skipWhy != "" {
		t.Skip(shared.skipWhy)
	}
	bucket := newBucket(t)
	leak(t, bucket, "sources/shop/logical/01KILLED/dump.zst.age")

	st, err := koffrs3.New(t.Context(), shared.client, koffrs3.Config{Bucket: bucket})
	require.NoError(t, err)
	var base storage.Storage = st

	found, err := base.(storage.MultipartMaintainer).ListIncompleteUploads(t.Context(), "sources/")
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, int64(5<<20), found[0].Bytes, "leak sends one 5 MiB part")
}
