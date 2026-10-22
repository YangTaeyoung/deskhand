//go:build !windows

package server

import (
	"context"
	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func currentRDPListener(context.Context) (wire.RDPListener, error) {
	return wire.RDPListener{}, session.ErrUnsupported
}
