// This tree is never compiled: the go tool ignores testdata, and the boundary
// check parses imports instead of building. Every file here breaks a rule of
// ADR-0010 on purpose, so that a check which stops working is caught.
//
// AR-08b — a package other than state reaching for the local SQLite driver.
package catalog

import (
	_ "modernc.org/sqlite"
)
