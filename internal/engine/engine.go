// Package engine adapts PostgreSQL, MySQL and MariaDB: the probes that ask a
// server what it is, and the sub-processes that dump and restore.
//
// A driver connection opened here serves to **probe** — reachability, version,
// family — and never to read the data koffr is there to back up: the dump stays
// a sub-process (ADR-0013, E-001). No import rule can see that difference, so
// the probes are written to emit one query and their tests say which.
package engine

import (
	"context"
	"fmt"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
)

// Engine implements the ports the domain declares for reaching a server.
type Engine struct{}

// New builds the adapter. It holds no connection: a probe opens one, asks its
// question and closes it.
func New() *Engine {
	return &Engine{}
}

// Probe asks a server whether it answers, what family it belongs to and what
// version it runs. The family is read from the server, never from the
// configuration (E-041).
func (e *Engine) Probe(ctx context.Context, target resolve.Target) (resolve.ServerInfo, error) {
	switch target.Engine {
	case resolve.PostgreSQL:
		return probePostgreSQL(ctx, target)

	case resolve.MySQL, resolve.MariaDB:
		return probeMySQLFamily(ctx, target)

	default:
		return resolve.ServerInfo{}, fmt.Errorf("%w: %q", resolve.ErrUnsupportedEngine, target.Engine)
	}
}
