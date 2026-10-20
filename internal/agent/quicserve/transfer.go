package quicserve

import (
	"context"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/server"
	"github.com/YangTaeyoung/deskhand/internal/wire"
	"github.com/quic-go/quic-go"
)

// TransferStreams are the handlers of the put and get streams of srv
// (docs/protocol.md §2).
func TransferStreams(srv *server.Server) StreamHandlers {
	return StreamHandlers{
		"put": func(ctx context.Context, st *quic.Stream, h wire.StreamHeader) {
			srv.ServePut(ctx, transferStream{st}, h)
		},
		"get": func(ctx context.Context, st *quic.Stream, h wire.StreamHeader) {
			srv.ServeGet(ctx, transferStream{st}, h)
		},
	}
}

// transferStream is a QUIC stream as a server.TransferStream.
type transferStream struct{ st *quic.Stream }

func (t transferStream) Read(p []byte) (int, error)         { return t.st.Read(p) }
func (t transferStream) Write(p []byte) (int, error)        { return t.st.Write(p) }
func (t transferStream) SetReadDeadline(d time.Time) error  { return t.st.SetReadDeadline(d) }
func (t transferStream) SetWriteDeadline(d time.Time) error { return t.st.SetWriteDeadline(d) }
func (t transferStream) Close() error                       { return t.st.Close() }
func (t transferStream) CancelRead()                        { t.st.CancelRead(0) }
func (t transferStream) CancelWrite()                       { t.st.CancelWrite(0) }
