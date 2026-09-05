package backup_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/Gu1llaum-3/koffr/internal/backup"
	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/catalog/sqlite"
	"github.com/Gu1llaum-3/koffr/internal/crypto"
	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/manifest"
	"github.com/Gu1llaum-3/koffr/internal/source"
	"github.com/Gu1llaum-3/koffr/internal/storage"
	"github.com/Gu1llaum-3/koffr/internal/storage/memory"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const sourceID = "prod-pg-main"

var startedAt = time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC)

// rig assembles a runner over real components: the real pipeline, real
// encryption, a contract-verified in-memory repository and a real SQLite
// catalog. Only the database is a double, because standing one up per case
// would make this suite slow enough to stop being run.
type rig struct {
	runner  *backup.Runner
	store   *memory.Storage
	catalog catalog.MetadataStore
	opener  crypto.Opener
}

func newRig(t *testing.T) *rig {
	t.Helper()

	operational, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	recovery, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	sealer, err := crypto.NewSealer([]string{
		operational.Recipient().String(), recovery.Recipient().String(),
	})
	require.NoError(t, err)
	opener, err := crypto.NewOpener(operational.String())
	require.NoError(t, err)

	cat, err := sqlite.Open(t.TempDir() + "/catalog.db")
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, cat.Close()) })

	store := memory.New()
	return &rig{
		store:   store,
		catalog: cat,
		opener:  opener,
		runner: &backup.Runner{
			Storage:        store,
			Catalog:        cat,
			Sealer:         sealer,
			Now:            func() time.Time { return startedAt },
			NewID:          func() catalog.ID { return "01JQ8Z3K5M7P9R2T4V6X8Y0A2B" },
			KoffrVersion:   "0.1.0",
			RepositoryName: "memory://test",
			Holder:         "koffr@test-host",
		},
	}
}

func (r *rig) request(src source.Source) backup.Request {
	return backup.Request{
		SourceID:    sourceID,
		Source:      src,
		Executor:    nopExecutor{},
		Backup:      source.Request{Kind: source.KindLogical},
		Destination: "memory://test",
	}
}

func (r *rig) keys(t *testing.T, prefix string) []string {
	t.Helper()
	var out []string
	for info, err := range r.store.List(t.Context(), prefix) {
		require.NoError(t, err)
		out = append(out, info.Key)
	}
	slices.Sort(out)
	return out
}

const prefix = "sources/prod-pg-main/logical/01JQ8Z3K5M7P9R2T4V6X8Y0A2B/"

func TestRun_StoresEverythingABackupNeeds(t *testing.T) {
	r := newRig(t)
	src := &fakeSource{
		payload:  []byte("PGDMP fake archive body"),
		sidecars: map[string][]byte{"globals.sql": []byte("CREATE ROLE probe;")},
	}

	res, err := r.runner.Run(t.Context(), r.request(src))
	require.NoError(t, err)
	assert.Equal(t, catalog.ID("01JQ8Z3K5M7P9R2T4V6X8Y0A2B"), res.BackupID)

	assert.Equal(t, []string{
		prefix + "RESTORE.md",
		prefix + "details.json.age",
		prefix + "dump.pgdump.zst.age",
		prefix + "globals.sql.zst.age",
		prefix + "manifest.json",
	}, r.keys(t, prefix))

	// The stored bytes must come back. Anything less is not a backup.
	assert.Equal(t, src.payload, r.unwrap(t, prefix+"dump.pgdump.zst.age"))
	assert.Equal(t, []byte("CREATE ROLE probe;"), r.unwrap(t, prefix+"globals.sql.zst.age"))
}

// EF-055: the manifest is plaintext so a write-only node can list and prune
// without a key, and anything describing the contents is sealed.
func TestRun_ManifestIsPlaintextAndDetailsAreNot(t *testing.T) {
	r := newRig(t)
	_, err := r.runner.Run(t.Context(), r.request(&fakeSource{
		payload:  []byte("body"),
		sidecars: map[string][]byte{"globals.sql": []byte("CREATE ROLE probe;")},
	}))
	require.NoError(t, err)

	raw := r.read(t, prefix+"manifest.json")
	m, err := manifest.Decode(bytes.NewReader(raw))
	require.NoError(t, err, "the manifest must be readable without any key")
	assert.Equal(t, sourceID, m.SourceID)
	assert.Equal(t, "postgresql", m.Engine)
	assert.Len(t, m.Objects, 3, "the dump, the globals sidecar and the details")

	sealed := r.read(t, prefix+"details.json.age")
	assert.NotContains(t, string(sealed), "probe_database",
		"the details must not be readable without a key")
	details, err := manifest.DecodeDetails(bytes.NewReader(r.unwrapRaw(t, sealed)))
	require.NoError(t, err)
	assert.Contains(t, details.Databases, "probe_database")
}

// ENF-010's point of no return.
//
// Before the manifest exists, nothing is a backup. So it must be written last,
// and it must be the only thing whose presence means the rest is there.
func TestRun_ManifestIsWrittenLast(t *testing.T) {
	r := newRig(t)
	var order []string
	r.runner.Storage = &recordingStorage{Storage: r.store, order: &order}

	_, err := r.runner.Run(t.Context(), r.request(&fakeSource{payload: []byte("body")}))
	require.NoError(t, err)

	last := order[len(order)-1]
	assert.Equal(t, prefix+"manifest.json", last,
		"a manifest written before the rest would name objects that may never arrive")
	assert.Less(t, slices.Index(order, prefix+"RESTORE.md"), slices.Index(order, prefix+"manifest.json"),
		"the restore procedure has to be in place before the backup counts as existing")
}

// A failure before the manifest must leave nothing a later pass could mistake
// for a backup. Objects with no manifest pointing at them are litter, and
// litter that looks like a backup is worse than none.
func TestRun_FailureBeforeTheManifestLeavesNothing(t *testing.T) {
	r := newRig(t)
	r.runner.Storage = &failingStorage{Storage: r.store, failOn: "details.json.age"}

	_, err := r.runner.Run(t.Context(), r.request(&fakeSource{payload: []byte("body")}))
	require.Error(t, err)

	assert.Empty(t, r.keys(t, prefix), "orphaned objects were left behind")
	backups, listErr := r.catalog.ListBackups(t.Context(), catalog.BackupFilter{})
	require.NoError(t, listErr)
	assert.Empty(t, backups, "a backup that failed must not be recorded as one")
}

// EF-045. Two Koffr instances backing up the same source at once would write to
// the same prefix and produce a manifest describing neither run.
func TestRun_SecondJobOnTheSameSourceIsRefused(t *testing.T) {
	r := newRig(t)

	release := make(chan struct{})
	slow := &fakeSource{payload: []byte("body"), blockUntil: release}

	first := make(chan error, 1)
	go func() { _, err := r.runner.Run(t.Context(), r.request(slow)); first <- err }()

	slow.waitUntilOpen(t)
	_, err := r.runner.Run(t.Context(), r.request(&fakeSource{payload: []byte("other")}))
	require.Error(t, err)
	assert.ErrorIs(t, err, backup.ErrSourceBusy)
	assert.Contains(t, err.Error(), "koffr@test-host",
		"the error should name the holder, or the operator cannot tell a live job from a stale lock")

	close(release)
	require.NoError(t, <-first)
}

func TestRun_LockIsReleasedOnSuccessAndOnFailure(t *testing.T) {
	lockKey := "locks/" + sourceID + ".lock"

	t.Run("success", func(t *testing.T) {
		r := newRig(t)
		_, err := r.runner.Run(t.Context(), r.request(&fakeSource{payload: []byte("body")}))
		require.NoError(t, err)
		assert.Empty(t, r.keys(t, lockKey))
	})

	t.Run("failure", func(t *testing.T) {
		r := newRig(t)
		r.runner.Storage = &failingStorage{Storage: r.store, failOn: "dump.pgdump.zst.age"}
		_, err := r.runner.Run(t.Context(), r.request(&fakeSource{payload: []byte("body")}))
		require.Error(t, err)
		assert.Empty(t, r.keys(t, lockKey),
			"a lock left behind by a failed job blocks the source until someone notices")
	})
}

func TestRun_RecordsTheBackupInTheCatalog(t *testing.T) {
	r := newRig(t)
	_, err := r.runner.Run(t.Context(), r.request(&fakeSource{payload: []byte("body")}))
	require.NoError(t, err)

	backups, err := r.catalog.ListBackups(t.Context(), catalog.BackupFilter{})
	require.NoError(t, err)
	require.Len(t, backups, 1)

	b := backups[0]
	assert.Equal(t, catalog.ID("01JQ8Z3K5M7P9R2T4V6X8Y0A2B"), b.ID)
	assert.Equal(t, sourceID, b.SourceID)
	assert.Equal(t, catalog.StatusCompleted, b.Status)
	assert.Equal(t, prefix+"manifest.json", b.ManifestKey)
	assert.Positive(t, b.SizeBytes)
}

// PD-006: a source that cannot produce what was asked for is refused before
// anything is written, not after half a backup exists.
func TestRun_RefusesAKindTheSourceCannotProduce(t *testing.T) {
	r := newRig(t)
	src := &fakeSource{payload: []byte("body"), kinds: []source.Kind{source.KindPhysical}}

	_, err := r.runner.Run(t.Context(), r.request(src))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "logical")
	assert.Empty(t, r.keys(t, "sources/"))
	assert.Empty(t, r.keys(t, "locks/"), "a refused job must not leave a lock")
}

func TestRun_ProbeFailureIsReportedAndLeavesNothing(t *testing.T) {
	r := newRig(t)
	src := &fakeSource{payload: []byte("body"), probeErr: errors.New("database unreachable")}

	_, err := r.runner.Run(t.Context(), r.request(src))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unreachable")
	assert.Empty(t, r.keys(t, ""))
}

func TestRun_RejectsAnIncompleteRequest(t *testing.T) {
	r := newRig(t)
	for name, mutate := range map[string]func(*backup.Request){
		"no source id": func(q *backup.Request) { q.SourceID = "" },
		"no source":    func(q *backup.Request) { q.Source = nil },
		"bad source id": func(q *backup.Request) {
			q.SourceID = "../escape" // layout refuses it; so must we
		},
	} {
		t.Run(name, func(t *testing.T) {
			req := r.request(&fakeSource{payload: []byte("body")})
			mutate(&req)
			_, err := r.runner.Run(t.Context(), req)
			require.Error(t, err)
		})
	}
}

// --- helpers ---

func (r *rig) read(t *testing.T, key string) []byte {
	t.Helper()
	rc, err := r.store.Get(t.Context(), key)
	require.NoError(t, err)
	defer func() { assert.NoError(t, rc.Close()) }()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	return b
}

// unwrap decrypts and decompresses a stored object.
func (r *rig) unwrap(t *testing.T, key string) []byte {
	t.Helper()
	return decompress(t, r.unwrapRaw(t, r.read(t, key)))
}

func (r *rig) unwrapRaw(t *testing.T, sealed []byte) []byte {
	t.Helper()
	plain, err := r.opener.Open(bytes.NewReader(sealed))
	require.NoError(t, err)
	out, err := io.ReadAll(plain)
	require.NoError(t, err)
	return decompress(t, out)
}

// recordingStorage notes the order objects are committed in.
type recordingStorage struct {
	storage.Storage
	order *[]string
}

func (s *recordingStorage) Put(ctx context.Context, key string, r io.Reader, opts storage.PutOptions) (storage.ObjectInfo, error) {
	info, err := s.Storage.Put(ctx, key, r, opts)
	if err == nil {
		*s.order = append(*s.order, key)
	}
	return info, err
}

// failingStorage refuses one key, to interrupt a job at a chosen point.
type failingStorage struct {
	storage.Storage
	failOn string
}

var errStorageRefused = errors.New("storage refused the write")

func (s *failingStorage) Put(ctx context.Context, key string, r io.Reader, opts storage.PutOptions) (storage.ObjectInfo, error) {
	if strings.HasSuffix(key, s.failOn) {
		return storage.ObjectInfo{}, errStorageRefused
	}
	return s.Storage.Put(ctx, key, r, opts)
}

type nopExecutor struct{}

func (nopExecutor) Dial(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("not implemented")
}
func (nopExecutor) Start(context.Context, executor.Command) (executor.Process, error) {
	return nil, errors.New("not implemented")
}
func (nopExecutor) Capabilities() executor.Capabilities {
	return executor.Capabilities{CanDial: true, Direct: true, Target: "fake"}
}
func (nopExecutor) Close() error { return nil }
