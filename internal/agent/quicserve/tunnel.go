package quicserve

import (
	"context"

	"github.com/YangTaeyoung/deskhand/internal/agent/server"
	"github.com/YangTaeyoung/deskhand/internal/wire"
	"github.com/quic-go/quic-go"
)

// TunnelStreams are the handlers of the tunnel streams of srv
// (docs/protocol.md §2).
func TunnelStreams(srv *server.Server) StreamHandlers {
	return StreamHandlers{
		"tunnel": func(ctx context.Context, st *quic.Stream, h wire.StreamHeader) {
			srv.ServeTunnel(ctx, transferStream{st}, h)
		},
	}
}
