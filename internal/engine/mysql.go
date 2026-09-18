package engine

import (
	"context"

	"github.com/Gu1llaum-3/koffr/internal/domain/resolve"
)

// probeMySQLFamily is written in the next task of the wave.
func probeMySQLFamily(_ context.Context, _ resolve.Target) (resolve.ServerInfo, error) {
	return resolve.ServerInfo{}, resolve.ErrUnsupportedEngine
}
