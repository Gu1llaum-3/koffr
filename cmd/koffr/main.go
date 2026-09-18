// Command koffr is the backup agent: a single static binary that takes, keeps
// and restores database backups without a service dependency.
package main

import (
	"fmt"
	"os"

	"github.com/Gu1llaum-3/koffr/internal/build"
	"github.com/Gu1llaum-3/koffr/internal/cli"
)

func main() {
	if err := cli.NewRoot().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", build.Name, err)
		os.Exit(1)
	}
}
