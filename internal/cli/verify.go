package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/domain/backup"
	"github.com/Gu1llaum-3/koffr/internal/domain/catalog"
)

// errVerificationFailed — the archive is not what the catalogue says it is.
// Exit code first, message second: `koffr verify` runs from a script.
var errVerificationFailed = errors.New("the archive did not pass its verification")

func newVerifyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "verify <backup-id>",
		Short: "Re-read an archive from its destination and recompute its checksum",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			book, closer, err := catalogFor(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = closer() }()

			archive, err := findArchive(cmd.Context(), book, args[0])
			if err != nil {
				return err
			}

			return verifyArchive(cmd, book, archive)
		},
	}
}

// findArchive names what it knows when it does not know what it was asked for:
// a typo is the likeliest reason to be here.
func findArchive(ctx context.Context, book catalog.Catalog, id string) (catalog.Backup, error) {
	known, err := book.Backups(ctx, catalog.Filter{})
	if err != nil {
		return catalog.Backup{}, fmt.Errorf("read the catalogue: %w", err)
	}

	for _, archive := range known {
		if archive.ID == id {
			return archive, nil
		}
	}

	recent := make([]string, 0, len(known))
	for _, archive := range known[:min(len(known), 5)] {
		recent = append(recent, archive.ID)
	}

	if len(recent) == 0 {
		return catalog.Backup{}, fmt.Errorf("no archive is called %q, and the catalogue is empty", id)
	}

	return catalog.Backup{}, fmt.Errorf("no archive is called %q; the most recent are: %s",
		id, strings.Join(recent, ", "))
}

// verifyArchive re-reads the archive and recomputes its checksum.
//
// It does **not** replay the structure: the archive is encrypted and koffr
// holds only a public key (ADR-0017, E-113). The structure was read while the
// dump streamed past, at backup time, and saying so is part of the command —
// an operator who believes everything was re-checked would be wrong.
func verifyArchive(cmd *cobra.Command, book catalog.Catalog, archive catalog.Backup) error {
	stores, err := storesFor(cmd, archive)
	if err != nil {
		return err
	}

	at := time.Now()

	for _, held := range stores {
		reread, err := checksumOf(cmd.Context(), held.Store, held.Path)
		if err != nil {
			return failVerification(cmd, book, archive, at,
				fmt.Sprintf("the archive could not be read back from %s: %v", held.Destination, err))
		}

		if reread != archive.SHA256Stored {
			return failVerification(cmd, book, archive, at,
				fmt.Sprintf("the checksum on %s does not match the catalogue:\n"+
					"  catalogue  %s\n  on disk    %s", held.Destination, archive.SHA256Stored, reread))
		}

		say(cmd, "checksum   matches on %s\n", held.Destination)
	}

	if err := book.SetVerification(cmd.Context(), archive.ID, catalog.Checksum, at); err != nil {
		return fmt.Errorf("record the verification: %w", err)
	}

	say(cmd, "structure  not replayed: the archive is encrypted and the private key that would\n"+
		"           open it is not on this machine, by design. It was checked while the dump\n"+
		"           streamed past, when the backup was taken (ADR-0017)\n")
	say(cmd, "verified   %s\n", at.UTC().Format(time.RFC3339))

	return nil
}

// failVerification records the failure and reports it. P4 wants a failure
// **visible**, so the catalogue is updated before the command returns.
func failVerification(
	cmd *cobra.Command, book catalog.Catalog, archive catalog.Backup, at time.Time, why string,
) error {
	if err := book.SetVerification(cmd.Context(), archive.ID, catalog.Failed, at); err != nil {
		warn(cmd, "the verification failed and the catalogue could not be updated: %v\n", err)
	}

	return fmt.Errorf("%w: %s", errVerificationFailed, why)
}

// heldArchive is one copy to re-read.
type heldArchive struct {
	Destination string
	Path        string
	Store       backup.Store
}

// storesFor builds a store per destination the archive says it is on. A
// destination the configuration no longer declares is reported, not ignored:
// an archive nobody can reach is exactly what a verification is for.
func storesFor(cmd *cobra.Command, archive catalog.Backup) ([]heldArchive, error) {
	loaded, err := loadResolved(cmd)
	if err != nil {
		return nil, err
	}

	declared := map[string]bool{}
	for _, destination := range loaded.Destinations {
		declared[destination.ID] = true
	}

	var held []heldArchive

	for _, location := range archive.Locations {
		if !declared[location.Destination] {
			return nil, fmt.Errorf("the archive %s is on %q, which the configuration no longer declares",
				archive.ID, location.Destination)
		}

		built, err := destinationsOf(loaded, config.Database{
			ID: archive.Database, Destinations: []string{location.Destination},
		})
		if err != nil {
			return nil, err
		}

		held = append(held, heldArchive{
			Destination: location.Destination, Path: location.Path, Store: built[0].Store,
		})
	}

	if len(held) == 0 {
		return nil, fmt.Errorf("the catalogue says the archive %s is nowhere", archive.ID)
	}

	return held, nil
}

// checksumOf hashes what is really on the destination.
func checksumOf(ctx context.Context, store backup.Store, path string) (string, error) {
	stored, err := store.Read(ctx, path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = stored.Close() }()

	digest := sha256.New()
	if _, err := io.Copy(digest, stored); err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	return hex.EncodeToString(digest.Sum(nil)), nil
}
