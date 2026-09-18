package resolve

import "context"

// ServerProbe asks a database server what it is. The domain declares it; the
// adapters implement it. That is what lets the compatibility matrix — the part
// of koffr that decides which tool dumps which database — be tested without a
// single database running (ADR-0010, AR-01).
type ServerProbe interface {
	// Probe reports whether the server answers, which family it belongs to and
	// which version it runs. The family it returns is the server's own, never
	// the one the configuration claims (E-041).
	Probe(ctx context.Context, target Target) (ServerInfo, error)
}
