package engine

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
)

// PostgreSQL error codes koffr tells apart, from the standard table.
const (
	invalidPassword      = "28P01"
	invalidAuthorization = "28000"
	invalidCatalogName   = "3D000"
)

// probePostgreSQL opens a connection and reads the version PostgreSQL announces
// during the startup handshake. No query is sent at all: the version travels in
// the parameters the server volunteers, which is as close to "nothing else" as
// a probe can get.
func probePostgreSQL(ctx context.Context, target resolve.Target) (resolve.ServerInfo, error) {
	connection, err := pgx.Connect(ctx, postgresURL(target))
	if err != nil {
		return resolve.ServerInfo{}, postgresFailure(err)
	}
	defer func() { _ = connection.Close(ctx) }()

	announced := connection.PgConn().ParameterStatus("server_version")

	info := resolve.ServerInfo{
		Reachable: true,
		Family:    resolve.PostgreSQL,
		Version:   resolve.ParseVersion(announced),
	}

	// One query, for the size E-061 falls back on. A probe that sent none was
	// a nice property to have; a disk-space check that has nothing to work with
	// on a first backup is a worse one to lose.
	var size int64
	if err := connection.QueryRow(ctx, postgresSizeQuery).Scan(&size); err == nil {
		info.DatabaseBytes = size
	}

	return info, nil
}

// postgresSizeQuery asks how much this database occupies. It reads the
// catalogue, never a table koffr backs up (ADR-0013).
const postgresSizeQuery = "SELECT pg_database_size(current_database())"

func postgresURL(target resolve.Target) string {
	address := fmt.Sprintf("%s:%d", target.Host, target.Port)

	dsn := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(target.User, target.Password),
		Host:   address,
		Path:   "/" + target.Database,
	}

	query := url.Values{}
	// A probe must not hang a diagnostic: doctor walks a whole fleet.
	query.Set("connect_timeout", "5")
	dsn.RawQuery = query.Encode()

	return dsn.String()
}

// postgresFailure turns what the driver says into what an operator can act on.
func postgresFailure(err error) error {
	var serverError *pgconn.PgError
	if errors.As(err, &serverError) {
		switch serverError.Code {
		case invalidPassword, invalidAuthorization:
			return fmt.Errorf("%w: %s", resolve.ErrDenied, serverError.Message)

		case invalidCatalogName:
			return fmt.Errorf("%w: %s", resolve.ErrNoSuchDatabase, serverError.Message)
		}
	}

	// Anything else — refused, timed out, name not resolved — is the server not
	// answering. The original error is kept: it names the address and the cause.
	return fmt.Errorf("%w: %w", resolve.ErrUnreachable, err)
}
