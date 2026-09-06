package retention

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/storage"
)

// FindIncompleteUploadsOlderThan reports multipart uploads a dead job left
// behind, ignoring any begun more recently than grace.
//
// It is the sibling of FindOrphansOlderThan and exists because that function
// cannot do this: it works by listing objects, and an unfinished upload is
// precisely what a listing omits. The two describe the same accident -- a job
// killed partway -- and only one of them can be found by looking.
//
// A store with nothing to leak reports nothing rather than an error. A
// filesystem has no multipart uploads to lose, and an error there would be a
// warning an operator learns to ignore, which is worse than silence.
func FindIncompleteUploadsOlderThan(
	ctx context.Context, st storage.Storage, grace time.Duration,
) ([]storage.IncompleteUpload, error) {
	m, ok := st.(storage.MultipartMaintainer)
	if !ok {
		return nil, nil
	}
	found, err := m.ListIncompleteUploads(ctx, storage.SourcesDir+"/")
	if err != nil {
		return nil, fmt.Errorf("retention: list unfinished uploads: %w", err)
	}

	cutoff := time.Now().Add(-grace)
	var out []storage.IncompleteUpload
	for _, u := range found {
		// An upload whose start time the service did not give cannot be aged,
		// and the safe reading of an unknown age is "possibly running". The
		// cost of being wrong here is not a stale part: it is aborting the
		// upload of a backup in progress, which kills it.
		if u.Initiated.IsZero() || u.Initiated.After(cutoff) {
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

// AbortIncompleteUploads discards them, releasing the parts they hold.
//
// Every failure is collected rather than the first one returned: one upload the
// service refuses to abort must not leave the rest paid for.
func AbortIncompleteUploads(
	ctx context.Context, st storage.Storage, uploads []storage.IncompleteUpload,
) error {
	if len(uploads) == 0 {
		return nil
	}
	m, ok := st.(storage.MultipartMaintainer)
	if !ok {
		return nil
	}
	var failures []error
	for _, u := range uploads {
		if err := m.AbortIncompleteUpload(ctx, u); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
