package store_test

import (
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/domain/backup"
	"github.com/Gu1llaum-3/koffr/internal/store"
	"github.com/Gu1llaum-3/koffr/internal/store/storetest"
)

// E-012a and E-066 — the filesystem destination, held to the same promises S3
// and SFTP will be held to at lot 4. The suite lives in storetest for exactly
// that reason.
func TestTheFilesystemStoreConforms(t *testing.T) {
	storetest.Conformance(t, func(t *testing.T) backup.Store {
		t.Helper()

		return store.NewFilesystem(t.TempDir())
	})
}

// A destination whose directory cannot be written to says why (BKP-12).
func TestCheckSaysWhyADestinationIsUnusable(t *testing.T) {
	unusable := store.NewFilesystem("/proc/koffr-cannot-write-here")

	err := unusable.Check(t.Context())
	if err == nil {
		t.Fatal("Check accepted a directory it cannot write to")
	}
	if got := err.Error(); got == "" {
		t.Error("Check refused without saying why")
	}
}
