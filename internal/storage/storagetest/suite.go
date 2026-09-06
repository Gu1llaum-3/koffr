// Package storagetest is the contract every storage.Storage must satisfy.
//
// It is written before the first implementation and reused unchanged by each
// one. That is the whole return on the investment: `fs` and `s3` are developed
// against the same expectations, and the SFTP backend planned for M7 costs
// nothing in test effort.
//
// The suite is deliberately harsh about failure. A storage backend that works
// when everything goes well is not interesting; what matters is that a failed
// upload leaves nothing behind that a later restore could mistake for a backup.
package storagetest

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// Factory builds a fresh, empty Storage for one test.
type Factory func(t *testing.T) storage.Storage

// partSize is the part size used by the large-object test. S3 requires at least
// 5 MiB for every part but the last, so this is the smallest value that
// actually exercises a multipart upload.
const partSize = 5 << 20

// Suite runs the whole contract.
func Suite(t *testing.T, newStorage Factory) {
	t.Helper()

	t.Run("PutGetRoundTrip", func(t *testing.T) { testPutGetRoundTrip(t, newStorage) })
	t.Run("PutIsAtomic", func(t *testing.T) { testPutIsAtomic(t, newStorage) })
	t.Run("NoObjectBeforeFirstByte", func(t *testing.T) { testNoObjectBeforeFirstByte(t, newStorage) })
	t.Run("FailedPutLeavesNothing", func(t *testing.T) { testFailedPutLeavesNothing(t, newStorage) })
	t.Run("FailedOverwriteKeepsOldObject", func(t *testing.T) { testFailedOverwriteKeepsOldObject(t, newStorage) })
	t.Run("Overwrite", func(t *testing.T) { testOverwrite(t, newStorage) })
	t.Run("EmptyObject", func(t *testing.T) { testEmptyObject(t, newStorage) })
	t.Run("Stat", func(t *testing.T) { testStat(t, newStorage) })
	t.Run("MissingKey", func(t *testing.T) { testMissingKey(t, newStorage) })
	t.Run("List", func(t *testing.T) { testList(t, newStorage) })
	t.Run("ListEmpty", func(t *testing.T) { testListEmpty(t, newStorage) })
	t.Run("Delete", func(t *testing.T) { testDelete(t, newStorage) })
	t.Run("GetRange", func(t *testing.T) { testGetRange(t, newStorage) })
	t.Run("Progress", func(t *testing.T) { testProgress(t, newStorage) })
	t.Run("ContextCancellation", func(t *testing.T) { testContextCancellation(t, newStorage) })
	t.Run("CapabilitiesAreHonest", func(t *testing.T) { testCapabilitiesAreHonest(t, newStorage) })
	t.Run("LargeObjectAcrossParts", func(t *testing.T) { testLargeObject(t, newStorage) })
	t.Run("NestedKeys", func(t *testing.T) { testNestedKeys(t, newStorage) })
	t.Run("PutIfAbsent", func(t *testing.T) { testPutIfAbsent(t, newStorage) })
	t.Run("PutIfAbsentIsAtomic", func(t *testing.T) { testPutIfAbsentIsAtomic(t, newStorage) })
}

// errReader fails after yielding a fixed number of bytes. It stands in for the
// real failure this suite is about: pg_dump dying halfway, or a network cut.
type errReader struct {
	data []byte
	at   int
	err  error
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.at >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.at:])
	r.at += n
	return n, nil
}

var errSourceFailed = errors.New("source failed")

func put(t *testing.T, s storage.Storage, key string, content []byte) storage.ObjectInfo {
	t.Helper()
	info, err := s.Put(t.Context(), key, bytes.NewReader(content), storage.PutOptions{})
	require.NoError(t, err)
	return info
}

func read(t *testing.T, s storage.Storage, key string) []byte {
	t.Helper()
	rc, err := s.Get(t.Context(), key)
	require.NoError(t, err)
	defer func() { assert.NoError(t, rc.Close()) }()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	return b
}

func testPutGetRoundTrip(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	content := []byte("koffr backup stream")

	info := put(t, s, "sources/s1/logical/B1/dump.pgdump.zst.age", content)
	assert.Equal(t, int64(len(content)), info.Size)
	assert.Equal(t, "sources/s1/logical/B1/dump.pgdump.zst.age", info.Key)
	assert.Equal(t, content, read(t, s, "sources/s1/logical/B1/dump.pgdump.zst.age"))
}

// An object must not become visible until it is complete. A reader that sees a
// half-written backup and a manifest that says it is finished is the worst
// outcome this package can produce.
func testPutIsAtomic(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	const key = "atomic"
	content := bytes.Repeat([]byte("a"), 1<<20)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := s.Put(t.Context(), key, bytes.NewReader(content), storage.PutOptions{})
		assert.NoError(t, err)
	}()

	// Whatever a concurrent reader observes, it is either absent or complete.
	for {
		select {
		case <-done:
			assert.Equal(t, content, read(t, s, key))
			return
		default:
			rc, err := s.Get(t.Context(), key)
			if err != nil {
				assert.ErrorIs(t, err, storage.ErrNotFound)
				continue
			}
			got, readErr := io.ReadAll(rc)
			assert.NoError(t, rc.Close())
			if readErr == nil {
				assert.Equal(t, content, got, "a partially written object became visible")
			}
		}
	}
}

// ENF-010, amended by P-007: pg_basebackup creates its output file before
// failing on the tablespace check, so a naive implementation stores an empty
// object and calls it a backup.
func testNoObjectBeforeFirstByte(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	const key = "never-started"

	_, err := s.Put(t.Context(), key, &errReader{err: errSourceFailed}, storage.PutOptions{})
	require.Error(t, err)
	assert.ErrorIs(t, err, errSourceFailed, "the source's own error must survive, not be replaced")

	_, err = s.Stat(t.Context(), key)
	assert.ErrorIs(t, err, storage.ErrNotFound,
		"a source that produced no bytes must leave no object behind")
}

func testFailedPutLeavesNothing(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	const key = "died-halfway"

	_, err := s.Put(t.Context(), key,
		&errReader{data: bytes.Repeat([]byte("x"), 512<<10), err: errSourceFailed},
		storage.PutOptions{PartSize: partSize})
	require.Error(t, err)
	assert.ErrorIs(t, err, errSourceFailed)

	_, err = s.Stat(t.Context(), key)
	assert.ErrorIs(t, err, storage.ErrNotFound, "a truncated upload must not be left visible")
}

// A retention pass that has already decided to keep this backup must not lose
// it because the next backup failed to overwrite it.
func testFailedOverwriteKeepsOldObject(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	const key = "overwritten"
	original := []byte("the good backup")
	put(t, s, key, original)

	_, err := s.Put(t.Context(), key,
		&errReader{data: bytes.Repeat([]byte("y"), 256<<10), err: errSourceFailed},
		storage.PutOptions{})
	require.Error(t, err)

	assert.Equal(t, original, read(t, s, key), "a failed overwrite destroyed the previous object")
}

func testOverwrite(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	const key = "twice"
	put(t, s, key, []byte("first, and longer"))
	put(t, s, key, []byte("second"))

	assert.Equal(t, []byte("second"), read(t, s, key))
	info, err := s.Stat(t.Context(), key)
	require.NoError(t, err)
	assert.Equal(t, int64(len("second")), info.Size, "stale size from the previous object")
}

// A zero-byte object is legitimate: pg_dumpall --globals-only produces one on a
// cluster with no roles worth dumping. It must be distinguishable from absence.
func testEmptyObject(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	const key = "empty"
	info := put(t, s, key, nil)
	assert.Equal(t, int64(0), info.Size)

	got, err := s.Stat(t.Context(), key)
	require.NoError(t, err, "an empty object exists and must not read as missing")
	assert.Equal(t, int64(0), got.Size)
	assert.Empty(t, read(t, s, key))
}

func testStat(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	content := bytes.Repeat([]byte("z"), 4096)
	written := put(t, s, "stat-me", content)

	got, err := s.Stat(t.Context(), "stat-me")
	require.NoError(t, err)
	assert.Equal(t, "stat-me", got.Key)
	assert.Equal(t, int64(len(content)), got.Size)
	assert.Equal(t, written.Size, got.Size)
	assert.False(t, got.LastModified.IsZero(), "LastModified drives retention by age")
}

func testMissingKey(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	const key = "not-there"

	_, err := s.Stat(t.Context(), key)
	assert.ErrorIs(t, err, storage.ErrNotFound)

	_, err = s.Get(t.Context(), key)
	assert.ErrorIs(t, err, storage.ErrNotFound)

	_, err = s.GetRange(t.Context(), key, 0, 10)
	assert.ErrorIs(t, err, storage.ErrNotFound)

	// Delete is idempotent: pruning must not fail because a previous run was
	// interrupted after removing the object but before recording it.
	assert.NoError(t, s.Delete(t.Context(), key))
}

func collect(t *testing.T, s storage.Storage, prefix string) []string {
	t.Helper()
	var keys []string
	for info, err := range s.List(t.Context(), prefix) {
		require.NoError(t, err)
		keys = append(keys, info.Key)
	}
	return keys
}

func testList(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	for _, k := range []string{
		"sources/a/logical/B1/manifest.json",
		"sources/a/logical/B1/dump.pgdump.zst.age",
		"sources/a/logical/B2/manifest.json",
		"sources/b/logical/B1/manifest.json",
		"catalog/latest.json.zst.age",
	} {
		put(t, s, k, []byte(k))
	}

	assert.ElementsMatch(t, []string{
		"sources/a/logical/B1/manifest.json",
		"sources/a/logical/B1/dump.pgdump.zst.age",
		"sources/a/logical/B2/manifest.json",
	}, collect(t, s, "sources/a/"))

	assert.Len(t, collect(t, s, "sources/"), 4)
	assert.Len(t, collect(t, s, ""), 5, "an empty prefix lists the whole repository")

	// A prefix is a string prefix, not a directory: "sources/a" must not also
	// match a sibling named "sources/ab".
	put(t, s, "sources/ab/logical/B1/manifest.json", []byte("x"))
	assert.Len(t, collect(t, s, "sources/a/"), 3)
}

func testListEmpty(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	assert.Empty(t, collect(t, s, "nothing/here/"))
	assert.Empty(t, collect(t, s, ""))
}

func testDelete(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	put(t, s, "doomed", []byte("x"))
	require.NoError(t, s.Delete(t.Context(), "doomed"))

	_, err := s.Stat(t.Context(), "doomed")
	assert.ErrorIs(t, err, storage.ErrNotFound)
	assert.NoError(t, s.Delete(t.Context(), "doomed"), "delete must be idempotent")
}

func testGetRange(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	if !s.Capabilities().RangeReads {
		t.Skip("backend does not advertise range reads")
	}
	content := []byte("0123456789abcdefghij")
	put(t, s, "ranged", content)

	rangeOf := func(offset, length int64) ([]byte, error) {
		rc, err := s.GetRange(t.Context(), "ranged", offset, length)
		if err != nil {
			return nil, err
		}
		defer func() { assert.NoError(t, rc.Close()) }()
		return io.ReadAll(rc)
	}

	got, err := rangeOf(0, -1)
	require.NoError(t, err)
	assert.Equal(t, content, got, "length -1 means to the end")

	got, err = rangeOf(5, 5)
	require.NoError(t, err)
	assert.Equal(t, []byte("56789"), got)

	got, err = rangeOf(15, 100)
	require.NoError(t, err)
	assert.Equal(t, []byte("fghij"), got, "a length past the end is truncated, not an error")

	_, err = rangeOf(int64(len(content)), 1)
	assert.Error(t, err, "an offset at or past the end has no bytes to return")
}

// Progress feeds the byte-stall watcher (EF-095), which is what turns a hung
// upload into a failed job instead of one that never ends.
func testProgress(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	content := bytes.Repeat([]byte("p"), 1<<20)

	var seen []int64
	_, err := s.Put(t.Context(), "watched", bytes.NewReader(content), storage.PutOptions{
		OnProgress: func(n int64) { seen = append(seen, n) },
	})
	require.NoError(t, err)

	require.NotEmpty(t, seen, "OnProgress was never called, so a stall would be invisible")
	for i := 1; i < len(seen); i++ {
		assert.GreaterOrEqual(t, seen[i], seen[i-1], "progress went backwards")
	}
	assert.Equal(t, int64(len(content)), seen[len(seen)-1],
		"the final report must be the whole object, or the watcher misjudges the end")
}

func testContextCancellation(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := s.Put(ctx, "cancelled", strings.NewReader("x"), storage.PutOptions{})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)

	_, err = s.Stat(t.Context(), "cancelled")
	assert.ErrorIs(t, err, storage.ErrNotFound)
}

// A backend that quietly ignores an option it cannot honour is worse than one
// that refuses: EF-042 immutability that silently does nothing would be
// discovered by an attacker, not by us.
func testCapabilitiesAreHonest(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	caps := s.Capabilities()

	if !caps.Immutable {
		_, err := s.Put(t.Context(), "locked", strings.NewReader("x"),
			storage.PutOptions{Immutable: true})
		assert.Error(t, err, "immutability was requested and silently ignored")
	}
	if !caps.RangeReads {
		put(t, s, "ranged", []byte("0123456789"))
		_, err := s.GetRange(t.Context(), "ranged", 2, 3)
		assert.Error(t, err, "range reads were requested and silently ignored")
	}

	// A backend claiming a delete gives the bytes back has to actually stop
	// listing them. Retention reports freed space from this claim, and a purge
	// that reports freeing what it did not is worse than one that admits it
	// cannot.
	if caps.DeleteReclaimsSpace {
		put(t, s, "reclaim/one", []byte("payload"))
		require.NoError(t, s.Delete(t.Context(), "reclaim/one"))

		var remaining int
		for range s.List(t.Context(), "reclaim/") {
			remaining++
		}
		assert.Zero(t, remaining, "the object was deleted and the backend still lists it")
	}
}

func testLargeObject(t *testing.T, newStorage Factory) {
	if testing.Short() {
		t.Skip("large object test skipped in short mode")
	}
	s := newStorage(t)

	// Deterministic pseudo-random content: a repeated pattern would hide an
	// implementation that reassembled the parts in the wrong order.
	const size = partSize*2 + 1<<20
	content := make([]byte, size)
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // reproducible test data, not a secret
	var word [8]byte
	for i := 0; i < len(content); i += len(word) {
		binary.LittleEndian.PutUint64(word[:], rng.Uint64())
		copy(content[i:], word[:])
	}

	info, err := s.Put(t.Context(), "big", bytes.NewReader(content),
		storage.PutOptions{PartSize: partSize})
	require.NoError(t, err)
	assert.Equal(t, int64(size), info.Size)

	got := read(t, s, "big")
	require.Len(t, got, size)
	assert.True(t, bytes.Equal(content, got), "content differs across the part boundary")
}

// PutIfAbsent is the repository lock (EF-045). It must report a taken key as
// ErrAlreadyExists and nothing else, because the caller reads that as "another
// Koffr is working on this source" and any other error as "the destination is
// broken".
func testPutIfAbsent(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	const key = "locks/prod.lock"

	require.NoError(t, s.PutIfAbsent(t.Context(), key, []byte("held by host-a")))
	assert.Equal(t, []byte("held by host-a"), read(t, s, key))

	err := s.PutIfAbsent(t.Context(), key, []byte("held by host-b"))
	assert.ErrorIs(t, err, storage.ErrAlreadyExists)
	assert.Equal(t, []byte("held by host-a"), read(t, s, key),
		"a refused write must not have changed the holder")

	// Releasing and taking it again is the normal cycle.
	require.NoError(t, s.Delete(t.Context(), key))
	assert.NoError(t, s.PutIfAbsent(t.Context(), key, []byte("held by host-b")))
}

// Exactly one winner. A Stat followed by a Put would let both instances through
// the gap between them, which is the whole reason this is one operation.
func testPutIfAbsentIsAtomic(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	const key = "locks/contended.lock"
	const racers = 8

	var wg sync.WaitGroup
	results := make([]error, racers)
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = s.PutIfAbsent(t.Context(), key, fmt.Appendf(nil, "racer-%d", i))
		}()
	}
	close(start)
	wg.Wait()

	winners := 0
	for i, err := range results {
		if err == nil {
			winners++
			continue
		}
		assert.ErrorIs(t, err, storage.ErrAlreadyExists, "racer %d", i)
	}
	assert.Equal(t, 1, winners, "a lock that lets several holders in is not a lock")
}

// Keys are paths with several segments (see layout.go). A backend that maps
// them onto a filesystem has to create intermediate directories; one that maps
// them onto object keys has to not.
func testNestedKeys(t *testing.T, newStorage Factory) {
	s := newStorage(t)
	for i := range 3 {
		key := fmt.Sprintf("sources/s%d/logical/B1/deeply/nested/object.age", i)
		put(t, s, key, []byte(key))
		assert.Equal(t, []byte(key), read(t, s, key))
	}
	assert.Len(t, collect(t, s, "sources/"), 3)
}
