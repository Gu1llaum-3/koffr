// Command koffr backs up PostgreSQL and MariaDB.
//
// Everything lives in internal/cli. This file exists to own the two things a
// process owns and a library must not: the streams, and the exit code.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Gu1llaum-3/koffr/internal/cli"
)

func main() {
	// A cancelled context is how a backup stops cleanly: it kills pg_dump,
	// which releases whatever it was holding on the server. Leaving that to the
	// default SIGINT handler would kill Koffr and leave the dump running.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cli.Run(ctx, os.Args[1:], cli.Streams{
		In: os.Stdin, Out: os.Stdout, Err: os.Stderr,
	}))
}
