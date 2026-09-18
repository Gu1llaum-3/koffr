package resolve_test

import (
	"context"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
)

// fakeProbe answers what a test tells it to. It exists so that the rules of
// § 5.2 can be tested against a fleet that is described rather than started:
// the real probe has its own tests, against real servers.
type fakeProbe struct {
	servers map[string]resolve.ServerInfo
	failure error
}

func (f fakeProbe) Probe(_ context.Context, target resolve.Target) (resolve.ServerInfo, error) {
	if f.failure != nil {
		return resolve.ServerInfo{}, f.failure
	}

	info, known := f.servers[target.Host]
	if !known {
		return resolve.ServerInfo{}, resolve.ErrUnreachable
	}

	return info, nil
}

// The fake is a ServerProbe, checked at compile time rather than by hope.
var _ resolve.ServerProbe = fakeProbe{}
