package resolve

import (
	"context"
	"fmt"
)

// Source is where a tool came from. The § 5.2 is explicit that this is a
// **tie-break** between candidates that are already compatible, never a
// selection criterion: preferring the host is how an agent ends up dumping a
// PostgreSQL 16 with a client 14.
type Source string

// The three sources of E-013.
const (
	Host      Source = "host"
	Managed   Source = "managed"
	Container Source = "container"
)

// Tool is what a binary is for. koffr needs both: dumping is E-047, restoring
// is E-048, and they do not have the same compatibility rule.
type Tool string

// The two operations a tool performs.
const (
	Dump    Tool = "dump"
	Restore Tool = "restore"
)

// Candidate is one tool koffr found and **ran**. Its family and its version are
// what the binary answered, never what its path or its name suggested (E-039,
// E-041).
type Candidate struct {
	Family  Family
	Tool    Tool
	Path    string
	Version Version
	Source  Source
}

func (c Candidate) String() string {
	return fmt.Sprintf("%s %s (%s, %s)", c.Family, c.Version, c.Source, c.Path)
}

// ToolFinder enumerates the tools available for a family and an operation. It
// expresses **no preference**: the order it returns carries no meaning, and
// choosing is the resolver's job (E-038).
type ToolFinder interface {
	Find(ctx context.Context, family Family, tool Tool) ([]Candidate, error)
}
