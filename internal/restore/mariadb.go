package restore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Gu1llaum-3/koffr/internal/executor"
	"github.com/Gu1llaum-3/koffr/internal/source/mariadb"
)

// MariaDB replays a logical backup into a MariaDB server.
//
// It drives the mariadb client rather than parsing SQL, for the same reason the
// backup side drives mariadb-dump: the dialect is the server's, it changes
// between majors, and the only implementation always right about it ships with
// the server (PD-002).
type MariaDB struct {
	// Config points at the server to restore *into*. A full source
	// configuration, because restoring needs what dumping needs: a tunnel, a
	// credentials file, and a client of the right major.
	Config mariadb.Config
}

// MariaDBRequest is what to restore and where.
type MariaDBRequest struct {
	// Database is the database to restore into.
	//
	// A mariadb-dump taken with --databases carries its own CREATE DATABASE and
	// USE, so the dump decides where its contents land. Naming a different
	// target here rewrites that, which is the only way a test restore can be
	// kept off the database the backup came from.
	Database string

	// Dump is the SQL stream, already decrypted and decompressed.
	Dump io.Reader

	// Grants is the grants.sql sidecar, replayed after the data so the accounts
	// it names can be granted rights on tables that exist. Optional.
	Grants io.Reader
}

// MariaDBResult reports what happened.
type MariaDBResult struct {
	// Warnings are problems that did not stop the restore, principally from
	// replaying grants into a server that already has some of those accounts.
	Warnings []string
}

// Restore replays a backup into the configured server.
func (m MariaDB) Restore(ctx context.Context, ex executor.Executor, req MariaDBRequest) (MariaDBResult, error) {
	var res MariaDBResult

	if req.Database == "" {
		return res, errors.New(
			"restore: no target database; name the database to restore into, it is never taken from the backup")
	}
	if req.Dump == nil {
		return res, errors.New("restore: no dump to restore")
	}

	cfg := m.Config
	cfg.Database = req.Database
	session, err := cfg.Open(ctx, ex)
	if err != nil {
		return res, err
	}
	defer func() { _ = session.Close() }()

	bin, err := cfg.ResolveBin("mariadb")
	if err != nil {
		return res, err
	}

	// The dump carries CREATE DATABASE IF NOT EXISTS for the name it was taken
	// from, so the target has to exist before the client can select it -- and
	// when the target is a different name, this is what makes it exist at all.
	if err := m.createDatabase(ctx, session, bin, req.Database); err != nil {
		return res, err
	}

	// The dump names its own database, and that name wins over anything given
	// to the client. Rewriting it is the only thing that makes Database mean
	// what it says.
	if err := run(ctx, m.Config.ToolRunner, executor.Command{
		Path: bin,
		Args: m.clientArgs(session, req.Database, true),
		Env:  session.Env(bin),
	}, retarget(req.Dump, req.Database), "mariadb"); err != nil {
		return res, err
	}

	if req.Grants != nil {
		if warn := m.replayGrants(ctx, session, bin, req.Database, req.Grants); warn != "" {
			res.Warnings = append(res.Warnings, warn)
		}
	}
	return res, nil
}

// clientArgs builds the command line for the mariadb client.
func (m MariaDB) clientArgs(session *mariadb.Session, database string, withDatabase bool) []string {
	args := []string{
		session.DefaultsFile(),
		// The client reads "localhost" as an instruction to use a Unix socket
		// rather than as an address; Koffr always speaks TCP.
		"--protocol=TCP",
		// Stop at the first error rather than carrying on: a restore that
		// reports success having skipped half its statements is the failure
		// this whole project exists to prevent.
		"--batch",
	}
	if withDatabase {
		args = append(args, "--database="+database)
	}
	return args
}

// createDatabase makes the target exist without failing when it already does.
//
// Unlike PostgreSQL's restore, this does not refuse a database that is already
// there: the dump itself is written to be replayed onto an existing schema, and
// the guard that matters -- not landing on the wrong database -- is that the
// target is always named explicitly rather than taken from the backup.
func (m MariaDB) createDatabase(ctx context.Context, session *mariadb.Session, bin, name string) error {
	if strings.ContainsAny(name, "`\n\r") {
		return fmt.Errorf("restore: %q is not a usable database name", name)
	}
	stmt := "CREATE DATABASE IF NOT EXISTS `" + name + "`;"
	return run(ctx, m.Config.ToolRunner, executor.Command{
		Path: bin,
		Args: append(m.clientArgs(session, name, false), "--execute="+stmt),
		Env:  session.Env(bin),
	}, nil, "mariadb")
}

// replayGrants replays grants.sql and reports rather than fails.
//
// An account the target server already has makes the statement error, and that
// is not a reason to call a restore that put every row back a failure. The
// warning says what did not apply so an operator can look.
func (m MariaDB) replayGrants(
	ctx context.Context, session *mariadb.Session, bin, database string, grants io.Reader,
) string {
	// Nothing is returned as an error on purpose: every outcome here is a
	// warning, and saying so with a string keeps that impossible to get wrong.
	err := run(ctx, m.Config.ToolRunner, executor.Command{
		Path: bin,
		Args: append(m.clientArgs(session, database, false), "--force"),
		Env:  session.Env(bin),
	}, grants, "mariadb (grants)")
	if err != nil {
		return "grants were not fully replayed: " + err.Error() +
			"; accounts and privileges may need to be set by hand"
	}
	return ""
}
