package memory_test

import (
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/storage"
	"github.com/Gu1llaum-3/koffr/internal/storage/memory"
	"github.com/Gu1llaum-3/koffr/internal/storage/storagetest"
)

// The same contract fs and s3 run. That is the point: a backend other packages
// test against has to be held to the real behaviour, or their tests pass
// against something no production backend does.
func TestContract(t *testing.T) {
	storagetest.Suite(t, func(t *testing.T) storage.Storage { return memory.New() })
}
