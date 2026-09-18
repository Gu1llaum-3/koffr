// This tree is never compiled: the go tool ignores testdata, and the boundary
// check parses imports instead of building. Every file here breaks a rule of
// ADR-0010 on purpose, so that a check which stops working is caught.
//
// Allowed on purpose: shared is the one module every domain module may use.
package restore

import (
	_ "github.com/Gu1llaum-3/koffr/internal/domain/shared"
)
