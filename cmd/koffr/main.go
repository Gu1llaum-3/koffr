// Command koffr is the backup agent: a single static binary that takes, keeps
// and restores database backups without a service dependency.
package main

import (
	"fmt"
	"os"

	"github.com/Gu1llaum-3/koffr/internal/build"
	"github.com/Gu1llaum-3/koffr/internal/cli"
	"github.com/Gu1llaum-3/koffr/internal/domain/catalog"
	"github.com/Gu1llaum-3/koffr/internal/state"
)

// openCatalog is the one place the binary says that the local index is SQLite.
// `internal/cli` may not import `internal/state` (`AR-04`, vérifié par
// `internal/arch`), and the domain may not either (`AR-01`) — so the wiring
// lives here, which is what a main is for.
func openCatalog(directory string) (catalog.Catalog, func() error, error) {
	opened, err := state.Open(directory)
	if err != nil {
		return nil, nil, fmt.Errorf("open the local state: %w", err)
	}

	return state.NewCatalog(opened), opened.Close, nil
}

func main() {
	if err := cli.NewRoot(cli.WithCatalog(openCatalog)).Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", build.Name, err)
		os.Exit(1)
	}
}
