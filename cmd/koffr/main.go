// Command koffr backs up PostgreSQL and MariaDB.
//
// Milestone M0 ships interfaces only: this entry point exists so the build and
// the cross-compilation checks have something to produce. The CLI itself is
// built in M1.
package main

import (
	"fmt"
	"os"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println("koffr", version)
		return
	}

	fmt.Fprintln(os.Stderr, "koffr", version, "- not implemented yet (milestone M0: interfaces only)")
	os.Exit(2)
}
