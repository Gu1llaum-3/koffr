// Package backup runs one backup job from end to end.
//
// It is where the pieces meet: probe the source, take the repository lock, run
// the pipeline, store the artifacts, write the manifest, record it. Nothing
// here is clever; what it has to get right is the order, and what happens when
// a step in the middle fails.
//
// The order is not arbitrary. The manifest is written last, and that is the
// point of no return (ENF-010): before it exists nothing is a backup, and once
// it exists everything it names is already there. A failure before it removes
// what was written, because an object with no manifest pointing at it is litter
// -- and litter shaped like a backup is worse than none.
package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/catalog/replica"
	"github.com/Gu1llaum-3/koffr/internal/crypto"
	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/manifest"
	"github.com/Gu1llaum-3/koffr/internal/pipeline"
	"github.com/Gu1llaum-3/koffr/internal/restore"
	"github.com/Gu1llaum-3/koffr/internal/source"
	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// ErrSourceBusy means another job holds the source's lock (EF-045).
//
// A distinct error because the answer differs: a busy source is retried later,
// while an unreachable destination is a problem to fix.
var ErrSourceBusy = errors.New("backup: the source is already being backed up")

// Runner executes backup jobs against one repository.
type Runner struct {
	Storage storage.Storage
	Catalog catalog.MetadataStore
	Sealer  crypto.Sealer

	// Now and NewID are injected so a test can pin them. Everything else about
	// a backup is derived from what the source reports.
	Now   func() time.Time
	NewID func() catalog.ID

	KoffrVersion string
	// RepositoryName is how the destination is written in RESTORE.md, in the
	// form an operator would type.
	RepositoryName string
	// Holder identifies this Koffr in the lock, so an operator finding a stale
	// one knows which machine to look at.
	Holder string
}

// Request is one backup to take.
type Request struct {
	SourceID string
	// Trigger says what asked for this backup. Empty means a person did, which
	// is the only thing that can reach the Runner today; the scheduler will set
	// it explicitly (EF-090).
	Trigger     catalog.Trigger
	Source      source.Source
	Executor    executor.Executor
	Backup      source.Request
	Destination string
}

// Result describes the backup that was taken.
type Result struct {
	BackupID catalog.ID
	Prefix   string
	Manifest manifest.Manifest

	// Warnings are things that went wrong after the point of no return, which
	// by definition cannot fail the job: the backup is already written and
	// restorable.
	Warnings []string
}

// Run takes one backup.
func (r *Runner) Run(ctx context.Context, req Request) (res Result, err error) {
	if err := r.validate(req); err != nil {
		return Result{}, err
	}

	// The job is opened before anything is attempted and closed on every exit
	// path, including the ones that never touch the repository.
	//
	// This is PD-007 where it is hardest: a backup that failed leaves no
	// manifest and no objects, so without this record the question "did last
	// night's backup run?" has no answer for the only case where it matters.
	// The record is also what a crashed process leaves behind -- a job stuck in
	// "running" says the machine died, which is worth knowing.
	job := catalog.Job{
		ID:        string(r.NewID()),
		SourceID:  req.SourceID,
		Kind:      string(req.Backup.Kind),
		Trigger:   req.trigger(),
		Status:    catalog.StatusRunning,
		StartedAt: r.Now().UTC(),
	}
	r.openJob(ctx, &res, job)

	// Registered before the lock and the cleanup, so it runs after both: the
	// replica should describe the repository as it was left, not as it was
	// halfway through being tidied.
	defer func() { r.closeJob(ctx, &res, job, err) }()

	src, err := storage.ForSource(req.SourceID)
	if err != nil {
		return Result{}, fmt.Errorf("backup: %w", err)
	}

	// Probe before locking. A source that cannot produce what was asked for is
	// refused without touching the repository, so a misconfiguration does not
	// leave a lock behind for someone to clear (PD-006).
	info, err := req.Source.Probe(ctx, req.Executor)
	if err != nil {
		return Result{}, fmt.Errorf("backup: probe %s: %w", req.SourceID, err)
	}
	if !slices.Contains(info.Kinds, req.Backup.Kind) {
		return Result{}, fmt.Errorf(
			"backup: source %s cannot produce a %s backup; it offers %v%s",
			req.SourceID, req.Backup.Kind, info.Kinds, restrictionSuffix(info))
	}

	release, err := r.lock(ctx, src)
	if err != nil {
		return Result{}, err
	}
	defer release()

	backupID := r.NewID()
	dir := dirFor(req.Backup.Kind)
	b, err := src.Backup(dir, string(backupID))
	if err != nil {
		return Result{}, fmt.Errorf("backup: %w", err)
	}

	// Everything written from here is removed if the manifest is not reached.
	committed := false
	defer func() {
		if !committed {
			r.discard(ctx, b.Prefix())
		}
	}()

	result, err := r.store(ctx, req, info, b, backupID)
	if err != nil {
		return Result{}, err
	}
	committed = true

	if err := r.record(ctx, req, b, result); err != nil {
		// The backup exists and is restorable; only our note of it is missing.
		// Saying so plainly beats deleting a good backup to keep the catalog
		// tidy, and `koffr catalog sync` rebuilds from the repository (EF-142).
		return Result{}, fmt.Errorf(
			"backup: %s was written to %s but could not be recorded in the catalog: %w; "+
				"run `koffr catalog sync` to pick it up", backupID, b.Prefix(), err)
	}

	return Result{BackupID: backupID, Prefix: b.Prefix(), Manifest: result}, nil
}

// openJob records the attempt as it starts.
func (r *Runner) openJob(ctx context.Context, res *Result, job catalog.Job) {
	if r.Catalog == nil {
		return
	}
	if err := r.Catalog.RecordJob(ctx, job); err != nil {
		res.Warnings = append(res.Warnings, "the job was not recorded in the catalog: "+err.Error())
	}
}

// closeJob writes the attempt's outcome and replicates the catalog.
//
// Neither can fail the job, and the signature says so. On the success path the
// manifest is already written, so the backup exists and is restorable, and
// reporting failure here would send an operator to rerun work that is done. On
// the failure path there is already an error worth more than these.
func (r *Runner) closeJob(ctx context.Context, res *Result, job catalog.Job, runErr error) {
	if r.Catalog == nil {
		return
	}

	job.FinishedAt = r.Now().UTC()
	if runErr != nil {
		job.Status = catalog.StatusFailed
		// The class decides whether a scheduler retries; the message is for a
		// person (ENF-011). Both are kept, and neither is derived from the
		// other.
		job.ErrorClass = pipeline.ClassOf(runErr)
		// Errors never carry a credential (ENF-021), which matters more here
		// than anywhere: the catalog is where a message lives for months.
		job.ErrorDetail = runErr.Error()
	} else {
		job.Status = catalog.StatusCompleted
	}

	if err := r.Catalog.RecordJob(ctx, job); err != nil {
		res.Warnings = append(res.Warnings, "the job outcome was not recorded: "+err.Error())
		return
	}

	// Replication comes after the job is closed out, so the copy in the
	// repository describes a finished attempt rather than a running one. A
	// failed job is replicated too: it is the only record that it happened.
	if warn := r.replicate(ctx); warn != "" {
		res.Warnings = append(res.Warnings, warn)
	}
}

// replicate copies the catalog into the repository (EF-141).
//
// This is what makes the catalog a cache rather than a second source of truth.
// Losing the machine Koffr runs on then loses nothing that matters.
func (r *Runner) replicate(ctx context.Context) string {
	snap, err := r.Catalog.Export(ctx)
	if err != nil {
		return "the catalog was not replicated to the repository: " + err.Error()
	}
	if err := replica.Write(ctx, r.Storage, r.Sealer, snap); err != nil {
		return "the catalog was not replicated to the repository: " + err.Error()
	}
	return ""
}

// trigger defaults to manual: today the only caller is a person at a terminal.
func (req Request) trigger() catalog.Trigger {
	if req.Trigger == "" {
		return catalog.TriggerManual
	}
	return req.Trigger
}

func (r *Runner) validate(req Request) error {
	switch {
	case req.SourceID == "":
		return errors.New("backup: no source id")
	case req.Source == nil:
		return errors.New("backup: no source")
	case req.Backup.Kind == "":
		return errors.New("backup: no backup kind")
	}
	return nil
}

func restrictionSuffix(info source.Info) string {
	if len(info.Restrictions) == 0 {
		return ""
	}
	return " (" + info.Restrictions[0] + ")"
}

// lock takes the repository lock for a source.
//
// The lock lives in the repository rather than in memory because it guards
// against a second Koffr instance, not a second goroutine: two of them writing
// to one prefix would produce a manifest describing neither run.
func (r *Runner) lock(ctx context.Context, src storage.Source) (release func(), err error) {
	holder := fmt.Appendf(nil, "%s\n%s\n", r.Holder, r.Now().UTC().Format(time.RFC3339))

	if err := r.Storage.PutIfAbsent(ctx, src.LockKey(), holder); err != nil {
		if errors.Is(err, storage.ErrAlreadyExists) {
			return nil, fmt.Errorf("%w: %s holds %s", ErrSourceBusy, r.describeHolder(ctx, src), src.LockKey())
		}
		return nil, fmt.Errorf("backup: take the lock for %s: %w", src.ID(), err)
	}

	return func() {
		// Best effort, and deliberately not on the caller's context: a
		// cancelled job must still release its lock, or the source stays
		// blocked until someone notices.
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = r.Storage.Delete(releaseCtx, src.LockKey())
	}, nil
}

// describeHolder reads whoever holds the lock, so the error can name them.
// An operator seeing "the source is busy" needs to know whether that is a live
// job on another host or a lock nobody released.
func (r *Runner) describeHolder(ctx context.Context, src storage.Source) string {
	rc, err := r.Storage.Get(ctx, src.LockKey())
	if err != nil {
		return "another Koffr"
	}
	defer func() { _ = rc.Close() }()

	content, err := io.ReadAll(io.LimitReader(rc, 1<<10))
	if err != nil || len(content) == 0 {
		return "another Koffr"
	}
	return string(bytes.TrimSpace(bytes.ReplaceAll(content, []byte("\n"), []byte(" since "))))
}

// store writes every artifact and finishes with the manifest.
func (r *Runner) store(
	ctx context.Context, req Request, info source.Info, b storage.Backup, backupID catalog.ID,
) (manifest.Manifest, error) {
	startedAt := r.Now().UTC()

	name := artifactName(info.Engine, req.Backup.Kind)
	run, err := pipeline.Run(ctx, pipeline.Request{
		Source:   req.Source,
		Executor: req.Executor,
		Backup:   req.Backup,
		Storage:  r.Storage,
		Key:      b.ObjectKey(name + ".zst.age"),
		Sealer:   r.Sealer,
	})
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("backup: %w", err)
	}

	objects := []manifest.Object{{
		Key:        name + ".zst.age",
		SizeBytes:  run.Object.Size,
		SHA256:     run.SHA256,
		Codec:      run.Codec,
		Encryption: "age",
		Recipients: run.Recipients,
	}}

	// Sidecars are small and already in memory: pg_dumpall's globals, and
	// whatever else a source hands back.
	for _, sidecarName := range slices.Sorted(maps(run.Sidecars)) {
		obj, err := r.putSealed(ctx, b.ObjectKey(sidecarName+".zst.age"), run.Sidecars[sidecarName])
		if err != nil {
			return manifest.Manifest{}, err
		}
		objects = append(objects, obj)
	}

	// The details describe the content, so they are sealed (EF-055).
	var detailsBuf bytes.Buffer
	if err := manifest.EncodeDetails(&detailsBuf, manifest.Details{Databases: info.Databases}); err != nil {
		return manifest.Manifest{}, fmt.Errorf("backup: %w", err)
	}
	detailsObj, err := r.putSealed(ctx, b.DetailsKey(), detailsBuf.Bytes())
	if err != nil {
		return manifest.Manifest{}, err
	}
	detailsObj.Key = storage.DetailsFile
	objects = append(objects, detailsObj)

	m := manifest.Manifest{
		FormatVersion: manifest.FormatVersion,
		BackupID:      string(backupID),
		SourceID:      req.SourceID,
		Engine:        string(info.Engine),
		ServerVersion: info.ServerVersion,
		Kind:          string(req.Backup.Kind),
		StartedAt:     startedAt,
		FinishedAt:    r.Now().UTC(),
		Status:        string(catalog.StatusCompleted),
		Objects:       objects,
		Tool:          manifest.ToolFrom(string(info.Engine), info.ServerVersion, nil),
		KoffrVersion:  r.KoffrVersion,
	}
	if err := m.Validate(); err != nil {
		return manifest.Manifest{}, fmt.Errorf("backup: %w", err)
	}

	// Before the manifest, so that a manifest's presence means the procedure to
	// use it is there too (PD-001).
	var doc bytes.Buffer
	if err := restore.WriteDoc(&doc, restore.DocInput{
		Manifest: m, Repository: r.RepositoryName, Prefix: b.Prefix(),
	}); err != nil {
		return manifest.Manifest{}, fmt.Errorf("backup: %w", err)
	}
	if _, err := r.Storage.Put(ctx, b.RestoreDocKey(), bytes.NewReader(doc.Bytes()), storage.PutOptions{}); err != nil {
		return manifest.Manifest{}, fmt.Errorf("backup: write the restore procedure: %w", err)
	}

	// The point of no return.
	var encoded bytes.Buffer
	if err := manifest.Encode(&encoded, m); err != nil {
		return manifest.Manifest{}, fmt.Errorf("backup: %w", err)
	}
	if _, err := r.Storage.Put(ctx, b.ManifestKey(), bytes.NewReader(encoded.Bytes()), storage.PutOptions{}); err != nil {
		return manifest.Manifest{}, fmt.Errorf("backup: write the manifest: %w", err)
	}
	return m, nil
}

// putSealed compresses, seals and stores a small in-memory artifact.
//
// The digest covers the ciphertext, exactly as the pipeline's does, so every
// object in the manifest can be checked the same way and without a key
// (EF-053).
func (r *Runner) putSealed(ctx context.Context, key string, content []byte) (manifest.Object, error) {
	var sealed bytes.Buffer
	digest := sha256.New()

	w, err := r.Sealer.Seal(io.MultiWriter(&sealed, digest))
	if err != nil {
		return manifest.Object{}, fmt.Errorf("backup: seal %s: %w", key, err)
	}
	z, err := zstd.NewWriter(w)
	if err != nil {
		_ = w.Close()
		return manifest.Object{}, fmt.Errorf("backup: compress %s: %w", key, err)
	}
	if _, err := z.Write(content); err != nil {
		_ = z.Close()
		_ = w.Close()
		return manifest.Object{}, fmt.Errorf("backup: compress %s: %w", key, err)
	}
	if err := z.Close(); err != nil {
		_ = w.Close()
		return manifest.Object{}, fmt.Errorf("backup: compress %s: %w", key, err)
	}
	// Closing writes age's final chunk marker; without it the object reads as
	// truncated.
	if err := w.Close(); err != nil {
		return manifest.Object{}, fmt.Errorf("backup: seal %s: %w", key, err)
	}

	info, err := r.Storage.Put(ctx, key, bytes.NewReader(sealed.Bytes()), storage.PutOptions{})
	if err != nil {
		return manifest.Object{}, fmt.Errorf("backup: write %s: %w", key, err)
	}
	return manifest.Object{
		Key:        baseName(key),
		SizeBytes:  info.Size,
		SHA256:     hex.EncodeToString(digest.Sum(nil)),
		Codec:      "zstd",
		Encryption: "age",
		Recipients: r.Sealer.Recipients(),
	}, nil
}

// record notes the backup in the catalog.
func (r *Runner) record(ctx context.Context, req Request, b storage.Backup, m manifest.Manifest) error {
	var total int64
	for _, o := range m.Objects {
		total += o.SizeBytes
	}
	return r.Catalog.RecordBackup(ctx, catalog.Backup{
		ID:          catalog.ID(m.BackupID),
		SourceID:    req.SourceID,
		Kind:        m.Kind,
		Destination: req.Destination,
		Status:      catalog.StatusCompleted,
		StartedAt:   m.StartedAt,
		FinishedAt:  m.FinishedAt,
		SizeBytes:   total,
		ManifestKey: b.ManifestKey(),
	})
}

// discard removes everything a failed job wrote.
//
// Best effort and on a detached context: the job has already failed, and
// leaving objects behind that no manifest names would make a later listing
// report a backup that never completed.
func (r *Runner) discard(ctx context.Context, prefix string) {
	cleanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()

	var keys []string
	for info, err := range r.Storage.List(cleanCtx, prefix) {
		if err != nil {
			return
		}
		keys = append(keys, info.Key)
	}
	for _, key := range keys {
		_ = r.Storage.Delete(cleanCtx, key)
	}
}

// artifactName is what the main object is called, before its extensions.
func artifactName(engine source.Engine, kind source.Kind) string {
	switch {
	case engine == source.EnginePostgreSQL && kind == source.KindLogical:
		return "dump.pgdump"
	case engine == source.EnginePostgreSQL:
		return "base.tar"
	case engine == source.EngineMariaDB && kind == source.KindLogical:
		return "dump.sql"
	default:
		return "base.xb"
	}
}

func dirFor(kind source.Kind) storage.Dir {
	if kind == source.KindLogical {
		return storage.DirLogical
	}
	return storage.DirPhysical
}

// baseName is the object's name inside its backup, which is what the manifest
// records: a manifest full of absolute keys would break the moment a repository
// was copied elsewhere.
func baseName(key string) string {
	if i := bytes.LastIndexByte([]byte(key), '/'); i >= 0 {
		return key[i+1:]
	}
	return key
}

// maps yields a map's keys, for sorting.
func maps[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}
