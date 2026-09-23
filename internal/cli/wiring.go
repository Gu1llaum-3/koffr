package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Gu1llaum-3/koffr/internal/domain/catalog"
)

// OpenCatalog opens the index of E-028 for a state directory, and hands back
// what closes it.
//
// It is a function rather than a value because the state directory comes from a
// flag, parsed after the root is built. And it is **supplied from outside**
// because `AR-04` forbids `internal/cli` to import `internal/state`: a command
// goes through a use case and never knows there is SQLite underneath (`N-3`).
type OpenCatalog func(stateDirectory string) (catalog.Catalog, func() error, error)

// Option configures the root. `cmd/koffr` passes what the transport may not
// build for itself; the tests pass nothing and get a root that says so.
type Option func(*wiring)

type wiring struct {
	openCatalog OpenCatalog
}

// WithCatalog gives the root its index.
func WithCatalog(open OpenCatalog) Option {
	return func(w *wiring) { w.openCatalog = open }
}

// errNoCatalog — a binary built without an index. It cannot happen in the
// real one; it happens in a test that did not ask for it, and saying so beats
// a nil dereference at two in the morning.
var errNoCatalog = errors.New("this build has no catalogue wired")

// catalogFor opens the index a command needs, and returns what closes it.
func catalogFor(cmd *cobra.Command) (catalog.Catalog, func() error, error) {
	if built.openCatalog == nil {
		return nil, nil, errNoCatalog
	}

	opened, closer, err := built.openCatalog(stateDir(cmd))
	if err != nil {
		return nil, nil, fmt.Errorf("open the catalogue: %w", err)
	}

	return opened, closer, nil
}

// built is what NewRoot was given. One root per process, so one value.
var built wiring
