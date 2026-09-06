package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// Descriptor is koffr.json at the root of a repository (EF-043).
//
// It exists so that a layout change has something to detect. Without it, an
// older Koffr pointed at a newer repository reads what it can, ignores what it
// cannot, and reports a partial view as a complete one -- which is how a
// retention pass deletes a backup it did not understand.
type Descriptor struct {
	FormatVersion int       `json:"format_version"`
	RepositoryID  string    `json:"repository_id"`
	CreatedAt     time.Time `json:"created_at"`
	CreatedBy     string    `json:"created_by"`
}

// DescriptorKey is where it lives.
func DescriptorKey() string { return DescriptorFile }

// ErrRepositoryTooNew means the repository was written by a Koffr that knows a
// layout this one does not.
var ErrRepositoryTooNew = errors.New("repository format is newer than this Koffr understands")

// OpenRepository reads the descriptor, writing it first if the repository is
// new.
//
// PutIfAbsent rather than Stat-then-Put: two Koffr instances can reach a fresh
// repository at the same moment, and the descriptor is exactly the object where
// a lost race would leave two different repository ids.
func OpenRepository(ctx context.Context, s Storage, id, koffrVersion string, now time.Time) (Descriptor, error) {
	want := Descriptor{
		FormatVersion: FormatVersion,
		RepositoryID:  id,
		CreatedAt:     now.UTC(),
		CreatedBy:     koffrVersion,
	}
	body, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		return Descriptor{}, fmt.Errorf("storage: encode %s: %w", DescriptorFile, err)
	}

	switch err := s.PutIfAbsent(ctx, DescriptorKey(), append(body, '\n')); {
	case err == nil:
		return want, nil
	case errors.Is(err, ErrAlreadyExists):
		// Someone got there first, which is the normal case for every run
		// after the first.
	default:
		return Descriptor{}, fmt.Errorf("storage: write %s: %w", DescriptorFile, err)
	}

	got, err := ReadDescriptor(ctx, s)
	if err != nil {
		return Descriptor{}, err
	}
	if got.FormatVersion > FormatVersion {
		return got, fmt.Errorf(
			"storage: %s says format version %d and this Koffr understands %d: %w; upgrade Koffr "+
				"rather than reading part of the repository",
			DescriptorFile, got.FormatVersion, FormatVersion, ErrRepositoryTooNew)
	}
	return got, nil
}

// ReadDescriptor returns the repository's descriptor.
func ReadDescriptor(ctx context.Context, s Storage) (Descriptor, error) {
	rc, err := s.Get(ctx, DescriptorKey())
	if err != nil {
		return Descriptor{}, fmt.Errorf("storage: read %s: %w", DescriptorFile, err)
	}
	defer func() { _ = rc.Close() }()

	body, err := io.ReadAll(rc)
	if err != nil {
		return Descriptor{}, fmt.Errorf("storage: read %s: %w", DescriptorFile, err)
	}
	var d Descriptor
	if err := json.Unmarshal(body, &d); err != nil {
		return Descriptor{}, fmt.Errorf("storage: %s is not readable: %w", DescriptorFile, err)
	}
	if d.FormatVersion == 0 {
		return Descriptor{}, fmt.Errorf("storage: %s has no format version", DescriptorFile)
	}
	return d, nil
}
