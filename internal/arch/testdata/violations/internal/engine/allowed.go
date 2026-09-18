// This tree is never compiled: the go tool ignores testdata, and the boundary
// check parses imports instead of building. Every file here breaks a rule of
// ADR-0010 on purpose, so that a check which stops working is caught.
//
// Allowed on purpose: engine may open a SQL connection to probe a server.
// It may NOT reach for the local SQLite driver — that is state only (ADR-0013).
package engine

import (
	_ "database/sql"
)
