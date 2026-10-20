package quicserve

import (
	"context"

	"github.com/YangTaeyoung/deskhand/internal/agent/server"
	"github.com/YangTaeyoung/deskhand/internal/wire"
	"github.com/quic-go/quic-go"
)

// PTYStreams are the handlers of the pty streams of srv
// (docs/protocol.md §2, §7).
func PTYStreams(srv *server.Server) StreamHandlers {
	return StreamHandlers{
		"pty": func(ctx context.Context, st *quic.Stream, h wire.StreamHeader) {
			srv.ServePTY(ctx, transferStream{st}, h)
		},
	}
}
