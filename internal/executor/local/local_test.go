package local_test

import (
	"testing"

	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/executor/executortest"
	"github.com/Gu1llaum-3/koffr/internal/executor/local"
)

// The whole contract, run locally. storage/ssh runs the identical suite.
func TestContract(t *testing.T) {
	executortest.Suite(t, executortest.Harness{
		New: func(t *testing.T) executor.Executor { return local.New() },
		DialTarget: func(t *testing.T) (string, string) {
			return executortest.Listener(t, "KOFFR-PROBE-BANNER"), "KOFFR-PROBE-BANNER"
		},
	})
}
