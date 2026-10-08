package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Gu1llaum-3/koffr/internal/domain/backup"
	"github.com/Gu1llaum-3/koffr/internal/domain/catalog"
	"github.com/Gu1llaum-3/koffr/internal/store"
)

// storeRepository is internal/store behind the port `catalog` declares. It
// hands over **bytes**: the shape of a manifest belongs to the domain, and an
// adapter that parsed it would be deciding what a manifest is (`N-2`).
type storeRepository struct{ store backup.Store }

func (r storeRepository) Archives(ctx context.Context, prefix string) ([]catalog.Archive, error) {
	found, err := r.store.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("list the destination: %w", err)
	}

	archives := make([]catalog.Archive, 0, len(found))
	for _, entry := range found {
		archives = append(archives, catalog.Archive{Path: entry.Path, Bytes: entry.Bytes, At: entry.At})
	}

	return archives, nil
}

func (r storeRepository) ReadManifest(ctx context.Context, archive string) ([]byte, error) {
	opened, err := r.store.Read(ctx, archive+manifestSuffix)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, catalog.ErrNoManifest
		}

		return nil, fmt.Errorf("read the manifest of %s: %w", archive, err)
	}
	defer func() { _ = opened.Close() }()

	raw, err := io.ReadAll(io.LimitReader(opened, maxManifestBytes))
	if err != nil {
		return nil, fmt.Errorf("read the manifest of %s: %w", archive, err)
	}

	return raw, nil
}

// manifestSuffix is how a manifest is named: beside its archive (`N-4` of the
// lot 3). maxManifestBytes bounds what a listing will read of a file that
// claims to be one — a repository is not trusted to be small.
const (
	manifestSuffix   = ".json"
	maxManifestBytes = 1 << 20
)

// listerFor builds the use case of `koffr list`: the catalogue, and every
// destination the configuration declares.
//
// Every destination, not only those a database names: an archive whose database
// has since left the configuration still lies on the disk, and `A-19` is about
// seeing what is there.
func listerFor(cmd *cobra.Command, book catalog.Catalog) (catalog.Lister, error) {
	loaded, err := loadShape(cmd)
	if err != nil {
		return catalog.Lister{}, err
	}

	destinations := make([]catalog.Destination, 0, len(loaded.Destinations))

	for _, declared := range loaded.Destinations {
		if !strings.EqualFold(declared.Type, "filesystem") {
			continue
		}

		destinations = append(destinations, catalog.Destination{
			ID:         declared.ID,
			Repository: storeRepository{store: store.NewFilesystem(declared.Path)},
		})
	}

	return catalog.Lister{Catalog: book, Destinations: destinations}, nil
}
