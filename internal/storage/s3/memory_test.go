package s3_test

import (
	"io"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Gu1llaum-3/koffr/internal/storage"
	koffrs3 "github.com/Gu1llaum-3/koffr/internal/storage/s3"
)

// zeros feeds the uploader without holding anything, so what the heap shows is
// what the upload manager is keeping.
type zeros struct{ left int64 }

func (z *zeros) Read(p []byte) (int, error) {
	if z.left <= 0 {
		return 0, io.EOF
	}
	n := min(int64(len(p)), z.left)
	clear(p[:n])
	z.left -= n
	return int(n), nil
}

// Part size is a memory setting as much as a transfer one: the upload manager
// holds several parts at once, and nothing about a streamed artifact tells it
// how many more are coming. config.MaxPartSizeMiB is derived from that, so the
// derivation is measured rather than reasoned about -- an operator who follows
// our own advice to raise part_size_mib must not end up over ENF-001's 512 MiB.
func TestPut_PartSizeDecidesTheMemoryCeiling(t *testing.T) {
	if shared.skipWhy != "" {
		t.Skip(shared.skipWhy)
	}
	const largest = 32 << 20 // config.MaxPartSizeMiB

	st, err := koffrs3.New(t.Context(), shared.client, koffrs3.Config{
		Bucket: newBucket(t), PartSize: largest,
	})
	require.NoError(t, err)

	var before, peak runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	// Enough parts for the manager to reach its steady state, which is what
	// decides the ceiling; more data would measure the same thing for longer.
	_, err = st.Put(t.Context(), "sources/prod/logical/01BIG/dump.zst.age",
		&zeros{left: 12 * largest}, storage.PutOptions{})
	require.NoError(t, err)

	runtime.ReadMemStats(&peak)
	used := peak.TotalAlloc - before.TotalAlloc
	t.Logf("part size %d MiB: heap in use %d MiB, allocated during upload %d MiB",
		largest>>20, peak.HeapInuse>>20, used>>20)

	// Held well under the 512 MiB of ENF-001 rather than merely inside it: the
	// requirement covers the whole process, and this measures one stage of it.
	// At 64 MiB parts the same measurement read 452 MiB, which is why the
	// configuration does not allow that much.
	require.Less(t, peak.HeapInuse, uint64(256<<20),
		"part size is what decides whether ENF-001 holds for the whole pipeline")
}
