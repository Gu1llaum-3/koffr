package s3

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// listPageSize is asked for explicitly rather than left to the service.
//
// The two backends disagree on the default: MinIO returned 1003 uploads in a
// single unpaginated response where AWS caps at 1000. Naming the size makes the
// paging path the same everywhere, which is the only way a test can reach it.
const listPageSize = 1000

// ListIncompleteUploads reports multipart uploads that were begun and never
// finished, under prefix.
//
// This is the only way to see them. A killed job leaves its parts stored and
// billed, and ListObjectsV2 does not mention them -- so neither `koffr ls` nor
// retention's orphan sweep, which reads a listing, can find them. Measured
// against MinIO: after a job was killed mid-transfer, ListObjectsV2 returned
// the same objects as before while the parts sat on disk.
func (s *Storage) ListIncompleteUploads(ctx context.Context, prefix string) ([]storage.IncompleteUpload, error) {
	var (
		out         []storage.IncompleteUpload
		keyMarker   *string
		uploadMaker *string
	)
	// Filtered here rather than by the service. MinIO returns nothing at all
	// when ListMultipartUploads is given a Prefix, where AWS filters on it --
	// measured, both against the version this suite runs and against a
	// standalone probe. Asking the service would therefore report "nothing to
	// clean up" on MinIO, which is the one wrong answer that reassures.
	//
	// The listing is small in practice: an unfinished upload is an accident,
	// not a normal state, so scanning the bucket costs one request.
	scoped := s.key(prefix)
	for {
		page, err := s.client.ListMultipartUploads(ctx, &awss3.ListMultipartUploadsInput{
			Bucket:         aws.String(s.cfg.Bucket),
			MaxUploads:     aws.Int32(listPageSize),
			KeyMarker:      keyMarker,
			UploadIdMarker: uploadMaker,
		})
		if err != nil {
			return nil, fmt.Errorf("list unfinished uploads under %q: %w", prefix, err)
		}
		for _, u := range page.Uploads {
			if u.Key == nil || u.UploadId == nil {
				continue
			}
			if !strings.HasPrefix(*u.Key, scoped) {
				continue
			}
			held, ours, err := s.partsHeld(ctx, *u.Key, *u.UploadId)
			if err != nil {
				return nil, err
			}
			if !ours {
				continue
			}
			up := storage.IncompleteUpload{
				Key:      s.unkey(*u.Key),
				UploadID: *u.UploadId,
				Bytes:    held,
			}
			if u.Initiated != nil {
				up.Initiated = *u.Initiated
			}
			out = append(out, up)
		}
		// The pager helper covers ListObjectsV2 but not this call, so the two
		// markers are advanced by hand. Both are needed: one key can carry
		// several unfinished uploads, and paging on the key alone loops.
		if !aws.ToBool(page.IsTruncated) {
			return out, nil
		}
		keyMarker, uploadMaker = page.NextKeyMarker, page.NextUploadIdMarker
	}
}

// partsHeld reports what an upload is holding, and whether it is ours at all.
//
// The ownership question is not paranoia. MinIO answers ListMultipartUploads
// with every upload it holds, whichever bucket is named: measured, a bucket
// created seconds earlier reported two thousand uploads belonging to other
// buckets. Every Koffr repository uses the same key layout, so the prefix does
// not separate them either -- two repositories sharing an endpoint would each
// report the other's backup paths, and a service whose abort were as loose as
// its listing would let one abort the other's running backup.
//
// ListParts is scoped where the listing is not, measured on the same instance:
// it refuses a foreign upload with NoSuchUpload. So it is asked, and what comes
// back is both the answer and the size. On a healthy repository this costs
// nothing, because an unfinished upload is an accident rather than a state.
func (s *Storage) partsHeld(ctx context.Context, serviceKey, uploadID string) (int64, bool, error) {
	var (
		held   int64
		marker *string
	)
	for {
		page, err := s.client.ListParts(ctx, &awss3.ListPartsInput{
			Bucket:           aws.String(s.cfg.Bucket),
			Key:              aws.String(serviceKey),
			UploadId:         aws.String(uploadID),
			PartNumberMarker: marker,
		})
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchUpload" {
				return 0, false, nil
			}
			return 0, false, fmt.Errorf("inspect unfinished upload of %q: %w", serviceKey, err)
		}
		for _, p := range page.Parts {
			held += aws.ToInt64(p.Size)
		}
		if !aws.ToBool(page.IsTruncated) {
			return held, true, nil
		}
		marker = page.NextPartNumberMarker
	}
}

// AbortIncompleteUpload discards one, releasing its parts.
//
// An upload that is already gone is reported as success. Two operators pruning
// at once, or a retry after a partial failure, must not turn a repository that
// is now tidy into a failed command.
func (s *Storage) AbortIncompleteUpload(ctx context.Context, u storage.IncompleteUpload) error {
	_, err := s.client.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{
		Bucket:   aws.String(s.cfg.Bucket),
		Key:      aws.String(s.key(u.Key)),
		UploadId: aws.String(u.UploadID),
	})
	if err == nil {
		return nil
	}
	// AWS answers NoSuchUpload when the upload is already gone; MinIO answers
	// success. So this branch is unreachable against the test backend and
	// necessary against the real one -- measured, not assumed. It is the safe
	// direction to be untested in: it turns an error into success only for a
	// condition that already means the work is done.
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchUpload" {
		return nil
	}
	return fmt.Errorf("abort unfinished upload of %q: %w", u.Key, err)
}
