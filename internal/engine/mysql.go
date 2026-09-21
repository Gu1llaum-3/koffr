package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
)

// MySQL error numbers koffr tells apart.
const (
	accessDenied    = 1045
	unknownDatabase = 1049
)

// probeMySQLFamily opens a connection and asks the server its version — one
// query, and the only one this package sends to a database it did not create
// (ADR-0013). The family is read from the answer: MariaDB says so in its own
// version string, which is the only place E-041 allows koffr to look.
func probeMySQLFamily(ctx context.Context, target resolve.Target) (resolve.ServerInfo, error) {
	connector, err := mysql.NewConnector(mysqlConfig(target))
	if err != nil {
		return resolve.ServerInfo{}, fmt.Errorf("%w: %w", resolve.ErrUnreachable, err)
	}

	database := sql.OpenDB(connector)
	defer func() { _ = database.Close() }()

	var announced string
	if err := database.QueryRowContext(ctx, "SELECT VERSION()").Scan(&announced); err != nil {
		return resolve.ServerInfo{}, mysqlFailure(err)
	}

	info := resolve.ServerInfo{
		Reachable: true,
		Family:    familyOf(announced),
		Version:   resolve.ParseVersion(announced),
	}
	info.MyISAMTables, info.MyISAMUnknown = myISAMTables(ctx, database)

	return info, nil
}

// myISAMQuery asks the catalogue which tables of this database are not
// transactional. It is written on one line because the guard in
// queries_test.go reads string literals, and a query split across two of them
// would be a query it cannot see.
const myISAMQuery = "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND engine = 'MyISAM' ORDER BY table_name"

// myISAMTables returns the tables MyISAM holds, or why koffr could not tell.
//
// A failure here is not a failure of the probe: an account that cannot read the
// catalogue can still be backed up, and doctor must still be able to walk a
// fleet. What koffr refuses is to report an absence it never verified (E-056).
func myISAMTables(ctx context.Context, database *sql.DB) ([]string, string) {
	rows, err := database.QueryContext(ctx, myISAMQuery)
	if err != nil {
		return nil, err.Error()
	}
	defer func() { _ = rows.Close() }()

	var found []string

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err.Error()
		}

		found = append(found, name)
	}

	if err := rows.Err(); err != nil {
		return nil, err.Error()
	}

	return found, ""
}

// familyOf reads the family out of the version banner. MariaDB writes its name
// there — "11.4.8-MariaDB-ubu2404" — and MySQL does not. Nothing else in koffr
// decides this: not the port, not the configuration, not the name of a binary
// that happens to be installed (E-041).
func familyOf(announced string) resolve.Family {
	if strings.Contains(strings.ToLower(announced), "mariadb") {
		return resolve.MariaDB
	}

	return resolve.MySQL
}

func mysqlConfig(target resolve.Target) *mysql.Config {
	config := mysql.NewConfig()
	config.Net = "tcp"
	config.Addr = fmt.Sprintf("%s:%d", target.Host, target.Port)
	config.User = target.User
	config.Passwd = target.Password
	config.DBName = target.Database
	// A probe must not hang a diagnostic: doctor walks a whole fleet.
	config.Timeout = 5 * time.Second
	config.ReadTimeout = 5 * time.Second

	return config
}

// mysqlFailure turns what the driver says into what an operator can act on.
func mysqlFailure(err error) error {
	var serverError *mysql.MySQLError
	if errors.As(err, &serverError) {
		switch serverError.Number {
		case accessDenied:
			return fmt.Errorf("%w: %s", resolve.ErrDenied, serverError.Message)

		case unknownDatabase:
			return fmt.Errorf("%w: %s", resolve.ErrNoSuchDatabase, serverError.Message)
		}
	}

	return fmt.Errorf("%w: %w", resolve.ErrUnreachable, err)
}
