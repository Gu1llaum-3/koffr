package resolve

import "errors"

// Why a probe did not answer. An operator reads these to know what to fix, so
// they are told apart rather than folded into one "connection failed": a wrong
// port and a wrong password are not the same afternoon.
var (
	// ErrUnreachable — nothing answered at that address.
	ErrUnreachable = errors.New("the server did not answer")

	// ErrDenied — something answered and refused these credentials.
	ErrDenied = errors.New("the server refused these credentials")

	// ErrNoSuchDatabase — the server answered, the credentials were accepted,
	// and the database named does not exist.
	ErrNoSuchDatabase = errors.New("the server has no such database")

	// ErrUnsupportedEngine — the configuration names an engine koffr does not
	// know. The scope is fixed by ADR-0004.
	ErrUnsupportedEngine = errors.New("koffr does not know this engine")
)
