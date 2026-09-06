// Package s3 stores backups in S3 and S3-compatible object stores.
//
// Tested against MinIO, which is also what makes the suite runnable without an
// AWS account. Cloudflare R2, Wasabi and Garage speak the same API; the
// differences that matter are advertised through Capabilities rather than
// assumed.
package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"strings"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// defaultPartSize matches config.DefaultPartSizeMiB, which is where the choice
// is explained. Repeated here so a store built without going through the
// configuration -- a test, another caller -- behaves the same way.
const defaultPartSize = 16 << 20

// defaultMaxParts matches config.DefaultMaxParts, and is what S3 itself allows.
//
// It is only half the ceiling. The upload manager sizes parts for itself only
// when it knows the total length, which it cannot when reading a pipe, so part
// size times part count fixes the largest artifact that can ever be written --
// and the refusal arrives at the last part, after everything before it has been
// uploaded. The count is provider-set and cannot be asked for: several
// S3-compatible services allow a tenth of this, which is why Config carries it.
const defaultMaxParts = 10000

// On the deprecated upload manager.
//
// AWS marks feature/s3/manager as superseded by feature/s3/transfermanager, but
// that replacement is still v0: no compatibility promise, on the one code path
// where a silent behaviour change would corrupt backups. The deprecated package
// is v1, stable and widely exercised, and deprecation is not removal.
//
// So this stays until transfermanager reaches v1. The cost of being wrong is
// small: the choice is confined to this file, behind storage.Storage, and the
// contract suite already proves a replacement behaves identically.
//
// Reviewed 2026-09-05. Revisit when transfermanager is v1.

// Config describes one S3 destination.
type Config struct {
	Bucket string
	// Prefix scopes a repository inside a shared bucket. Empty means the
	// bucket root.
	Prefix string
	// PartSize is the default for uploads that do not override it.
	PartSize int64

	// MaxParts is how many parts one upload may have, enforced here rather
	// than left to the service. A provider that refuses part 1001 does so in
	// its own words, which no translation of ours would recognise; refusing it
	// ourselves keeps the failure legible on every backend.
	MaxParts int
}

// Storage is an S3-backed object store.
type Storage struct {
	client   *awss3.Client
	uploader *manager.Uploader //nolint:staticcheck // see "On the deprecated upload manager"
	cfg      Config
	// immutable records what the bucket can actually enforce, discovered at
	// construction rather than assumed. See Capabilities.
	immutable bool

	// reclaims records whether a delete gives the bytes back. False on a
	// versioned or locked bucket, where it writes a marker instead.
	reclaims bool
}

// New builds a Storage over an already-configured S3 client.
//
// The client is passed in rather than built here so that endpoint, credentials
// and path-style addressing stay in the configuration layer, where a test or a
// MinIO deployment can set them without this package growing knobs.
func New(ctx context.Context, client *awss3.Client, cfg Config) (*Storage, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("storage/s3: no bucket configured")
	}
	if cfg.PartSize == 0 {
		cfg.PartSize = defaultPartSize
	}
	if cfg.MaxParts == 0 {
		cfg.MaxParts = defaultMaxParts
	}
	// Bounded here and not only in the configuration, because a store can be
	// built without going through it, and because the number crosses into an
	// int32 two lines below.
	if cfg.MaxParts < 1 || cfg.MaxParts > defaultMaxParts {
		return nil, fmt.Errorf(
			"storage/s3: max parts is %d; S3 allows 1 to %d", cfg.MaxParts, defaultMaxParts)
	}

	s := &Storage{
		client: client,
		cfg:    cfg,
		uploader: manager.NewUploader(client, func(u *manager.Uploader) { //nolint:staticcheck // see above
			u.PartSize = cfg.PartSize
			// Bounded to [1, defaultMaxParts] a few lines above, which is well
			// inside an int32; the linter cannot see across the closure.
			u.MaxUploadParts = int32(cfg.MaxParts) //nolint:gosec // bounded above
		}),
	}
	s.immutable = bucketHasObjectLock(ctx, client, cfg.Bucket)

	// Asked once, at construction, and asked of the bucket rather than of the
	// configuration: what matters is what the bucket does, not what someone
	// believes it does.
	s.reclaims = !s.immutable && !bucketIsVersioned(ctx, client, cfg.Bucket)
	return s, nil
}

// bucketHasObjectLock asks the bucket rather than trusting configuration.
//
// EF-042 immutability that silently does nothing would be discovered by an
// attacker rather than by us, so the capability reflects what the bucket will
// actually enforce. Any error is read as "not available": claiming less than
// the truth is safe, claiming more is not.
func bucketHasObjectLock(ctx context.Context, client *awss3.Client, bucket string) bool {
	out, err := client.GetObjectLockConfiguration(ctx, &awss3.GetObjectLockConfigurationInput{
		Bucket: aws.String(bucket),
	})
	if err != nil || out.ObjectLockConfiguration == nil {
		return false
	}
	return out.ObjectLockConfiguration.ObjectLockEnabled == types.ObjectLockEnabledEnabled
}

// bucketIsVersioned reports whether a delete leaves the data behind.
//
// On a versioned bucket, DeleteObject without a version id writes a delete
// marker: the object stops being listed and every byte of it stays, and stays
// billed, until a lifecycle rule expires the noncurrent versions. A purge that
// reported freeing that space would be reporting something that did not happen.
//
// Unreadable configuration reads as versioned, which makes the reporting
// cautious rather than optimistic: claiming less than was freed costs nothing,
// claiming more costs trust.
func bucketIsVersioned(ctx context.Context, client *awss3.Client, bucket string) bool {
	out, err := client.GetBucketVersioning(ctx, &awss3.GetBucketVersioningInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return true
	}
	return out.Status == types.BucketVersioningStatusEnabled ||
		out.Status == types.BucketVersioningStatusSuspended
}

// key maps a repository key onto an object key inside the bucket.
func (s *Storage) key(k string) string {
	if s.cfg.Prefix == "" {
		return k
	}
	return strings.TrimSuffix(s.cfg.Prefix, "/") + "/" + k
}

// unkey is the inverse, for listings.
func (s *Storage) unkey(k string) string {
	if s.cfg.Prefix == "" {
		return k
	}
	return strings.TrimPrefix(k, strings.TrimSuffix(s.cfg.Prefix, "/")+"/")
}

// Put uploads an object.
//
// Atomicity comes from the protocol: a multipart upload is invisible until it
// is completed, and a single PutObject either lands whole or not at all. A
// source that fails partway aborts the upload, leaving no object and leaving
// any previous object at that key untouched (ENF-010).
func (s *Storage) Put(ctx context.Context, key string, r io.Reader, opts storage.PutOptions) (storage.ObjectInfo, error) {
	if opts.Immutable && !s.immutable {
		return storage.ObjectInfo{}, fmt.Errorf(
			"storage/s3: immutability was requested but bucket %q has no Object Lock configuration; "+
				"enable it on the bucket, or drop the requirement explicitly", s.cfg.Bucket)
	}
	if err := ctx.Err(); err != nil {
		return storage.ObjectInfo{}, err
	}

	counted := &progressReader{ctx: ctx, r: r, onProgress: opts.OnProgress}
	input := &awss3.PutObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(s.key(key)),
		Body:   counted,
	}
	if opts.Immutable {
		input.ObjectLockMode = types.ObjectLockModeCompliance
		if !opts.RetainUntil.IsZero() {
			input.ObjectLockRetainUntilDate = aws.Time(opts.RetainUntil)
		}
	}

	upload := func(u *manager.Uploader) { //nolint:staticcheck // see above
		if opts.PartSize > 0 {
			u.PartSize = max(opts.PartSize, manager.MinUploadPartSize)
		}
	}
	if _, err := s.uploader.Upload(ctx, input, upload); err != nil { //nolint:staticcheck // see above
		return storage.ObjectInfo{}, s.uploadError(key, opts, err)
	}

	info, err := s.Stat(ctx, key)
	if err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("stat %q after upload: %w", key, err)
	}
	// Report what the source produced, not what the service says it stored: the
	// two agreeing is exactly what the manifest digest is there to prove.
	info.Size = counted.total.Load()
	return info, nil
}

// uploadError names the setting an operator can actually change.
//
// The upload manager's own message ends with "Adjust PartSize to fit in this
// limit". PartSize is not a name that appears in a Koffr configuration file, so
// an operator reading it goes looking for a knob that does not exist -- at the
// end of an upload that has already run for hours, which is the worst moment to
// be sent hunting. Everything else is passed through untouched.
func (s *Storage) uploadError(key string, opts storage.PutOptions, err error) error {
	part := s.cfg.PartSize
	if opts.PartSize > 0 {
		part = max(opts.PartSize, manager.MinUploadPartSize) //nolint:staticcheck // see above
	}
	// Matched on the identifier rather than the sentence: the SDK builds this
	// message inline, with no constant to compare against, and a name is far
	// likelier to survive a rewording than the prose around it.
	if !strings.Contains(err.Error(), "MaxUploadParts") {
		return fmt.Errorf("upload %q: %w", key, err)
	}
	ceiling := part * int64(s.cfg.MaxParts)
	return fmt.Errorf(
		"upload %q: this artifact is larger than %s, which is all %d parts of %d MiB can carry "+
			"(the size cannot change once an upload has started): raise part_size_mib on this "+
			"destination, and max_parts too if your provider allows more than %d -- AWS allows "+
			"10000, several S3-compatible services only 1000: %w",
		key, humanBytes(ceiling), s.cfg.MaxParts, part>>20, s.cfg.MaxParts, err)
}

// humanBytes renders a ceiling the way an operator would write it.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%d GiB", n>>30)
	case n >= 1<<20:
		return fmt.Sprintf("%d MiB", n>>20)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// PutIfAbsent writes only if the key is free.
//
// If-None-Match: * makes the service perform the check, which is the only place
// it can be atomic: two Koffr instances asking S3 whether a key exists and then
// writing would both be told no.
func (s *Storage) PutIfAbsent(ctx context.Context, key string, content []byte) error {
	_, err := s.client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:      aws.String(s.cfg.Bucket),
		Key:         aws.String(s.key(key)),
		Body:        bytes.NewReader(content),
		IfNoneMatch: aws.String("*"),
	})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.ErrorCode() {
			case "PreconditionFailed", "ConditionalRequestConflict", "412":
				return fmt.Errorf("%q: %w", key, storage.ErrAlreadyExists)
			}
		}
		return fmt.Errorf("create %q: %w", key, err)
	}
	return nil
}

// Get opens an object for reading.
func (s *Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(s.key(key)),
	})
	if err != nil {
		return nil, translate(key, err)
	}
	return out.Body, nil
}

// GetRange opens part of an object. A length of -1 reads to the end.
func (s *Storage) GetRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if offset < 0 {
		return nil, fmt.Errorf("storage: negative offset %d for %q", offset, key)
	}
	spec := fmt.Sprintf("bytes=%d-", offset)
	if length >= 0 {
		spec = fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)
	}

	out, err := s.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(s.key(key)),
		Range:  aws.String(spec),
	})
	if err != nil {
		// An unsatisfiable range is not a missing object. Conflating them would
		// make a caller delete an object it merely mis-addressed.
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidRange" {
			return nil, fmt.Errorf("storage: range %s is not satisfiable for %q", spec, key)
		}
		return nil, translate(key, err)
	}
	return out.Body, nil
}

// Stat reports an object's size and modification time.
func (s *Storage) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	out, err := s.client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(s.key(key)),
	})
	if err != nil {
		return storage.ObjectInfo{}, translate(key, err)
	}

	info := storage.ObjectInfo{Key: key}
	if out.ContentLength != nil {
		info.Size = *out.ContentLength
	}
	if out.LastModified != nil {
		info.LastModified = *out.LastModified
	}
	if out.ETag != nil {
		info.ETag = strings.Trim(*out.ETag, `"`)
	}
	return info, nil
}

// List yields every object whose key starts with prefix.
func (s *Storage) List(ctx context.Context, prefix string) iter.Seq2[storage.ObjectInfo, error] {
	return func(yield func(storage.ObjectInfo, error) bool) {
		pager := awss3.NewListObjectsV2Paginator(s.client, &awss3.ListObjectsV2Input{
			Bucket: aws.String(s.cfg.Bucket),
			Prefix: aws.String(s.key(prefix)),
		})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				yield(storage.ObjectInfo{}, fmt.Errorf("list %q: %w", prefix, err))
				return
			}
			for _, obj := range page.Contents {
				info := storage.ObjectInfo{Key: s.unkey(aws.ToString(obj.Key))}
				if obj.Size != nil {
					info.Size = *obj.Size
				}
				if obj.LastModified != nil {
					info.LastModified = *obj.LastModified
				}
				if obj.ETag != nil {
					info.ETag = strings.Trim(*obj.ETag, `"`)
				}
				if !yield(info, nil) {
					return
				}
			}
		}
	}
}

// Delete removes an object. S3 treats deleting an absent key as success, which
// is the idempotence a retried prune needs.
func (s *Storage) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(s.key(key)),
	})
	if err != nil {
		return fmt.Errorf("delete %q: %w", key, err)
	}
	return nil
}

// Capabilities reports what this bucket can do, as discovered at construction.
func (s *Storage) Capabilities() storage.Capabilities {
	return storage.Capabilities{
		Immutable:           s.immutable,
		Multipart:           true,
		RangeReads:          true,
		DeleteReclaimsSpace: s.reclaims,
		ServerSideCopy:      true,
	}
}

// translate maps an S3 error onto the storage sentinel.
func translate(key string, err error) error {
	var noSuchKey *types.NoSuchKey
	var notFound *types.NotFound
	if errors.As(err, &noSuchKey) || errors.As(err, &notFound) {
		return fmt.Errorf("%q: %w", key, storage.ErrNotFound)
	}
	// HeadObject reports a missing object as a bare 404 with no typed error.
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound", "404":
			return fmt.Errorf("%q: %w", key, storage.ErrNotFound)
		}
	}
	return fmt.Errorf("%q: %w", key, err)
}

// progressReader reports bytes read and honours cancellation between reads.
//
// The count is atomic because the upload manager may read ahead while parts
// are in flight.
type progressReader struct {
	ctx        context.Context
	r          io.Reader
	onProgress func(int64)
	total      atomic.Int64
}

func (p *progressReader) Read(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := p.r.Read(b)
	if n > 0 {
		total := p.total.Add(int64(n))
		if p.onProgress != nil {
			p.onProgress(total)
		}
	}
	return n, err
}

var _ storage.Storage = (*Storage)(nil)
