// This tree is never compiled: the go tool ignores testdata, and the boundary
// check parses imports instead of building. Every file here breaks a rule of
// ADR-0010 on purpose, so that a check which stops working is caught.
//
// AR-09 — the public protocol package reaching back into the agent.
package protocol

import (
	_ "github.com/Gu1llaum-3/koffr/internal/config"
)
