// Package quicserve serves the agent over QUIC: the first stream of a
// connection is the rpc stream of server.ServeConn, later streams go to the
// handler their header names (docs/protocol.md §2).
package quicserve

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/config"
	"github.com/YangTaeyoung/deskhand/internal/agent/server"
	"github.com/YangTaeyoung/deskhand/internal/transport"
	"github.com/YangTaeyoung/deskhand/internal/wire"
	"github.com/quic-go/quic-go"
)

// StreamHandlers serve the streams after the rpc stream by their header's s.
// A handler owns the stream and closes it.
type StreamHandlers map[string]func(ctx context.Context, st *quic.Stream, h wire.StreamHeader)

const (
	// headerTimeout bounds the wait for a stream and for its header line.
	headerTimeout = 10 * time.Second
	// refusalTimeout bounds the write of a refusal line.
	refusalTimeout = 5 * time.Second
	// refusalGrace is how long a connection whose first stream was refused
	// stays open for the refusal line to arrive, unless the peer closes it.
	refusalGrace = time.Second
)

// Application error codes of a closed connection.
const (
	closeNormal   quic.ApplicationErrorCode = 0
	closeProtocol quic.ApplicationErrorCode = 1
)

// Run listens on cfg.Listen for controllers whose certificate fingerprint is
// one of cfg.ControllerFingerprints and serves each connection until ctx
// ends; it then returns nil. A failure to listen or to accept is returned.
func Run(ctx context.Context, cfg config.Agent, id transport.Identity, srv *server.Server, streams StreamHandlers) error {
	allowed := map[string]bool{}
	for _, fp := range cfg.ControllerFingerprints {
		allowed[fp] = true
	}
	ln, err := transport.Listen(cfg.Listen, id, func(fp string) bool { return allowed[fp] })
	if err != nil {
		return err
	}
	defer ln.Close()
	srv.Start()
	lg := srv.Logger()
	for {
		conn, err := ln.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept on %s: %w", cfg.Listen, err)
		}
		go serveConn(ctx, conn, srv, streams, lg)
	}
}

// serveConn serves the rpc stream that has to come first on conn, and the
// streams after it, until the rpc stream or ctx ends; then it closes conn.
func serveConn(ctx context.Context, conn *quic.Conn, srv *server.Server, streams StreamHandlers, lg *log.Logger) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	peer := conn.RemoteAddr().String()
	acceptCtx, acceptCancel := context.WithTimeout(ctx, headerTimeout)
	st, err := conn.AcceptStream(acceptCtx)
	acceptCancel()
	if err != nil {
		conn.CloseWithError(closeProtocol, "no rpc stream")
		return
	}
	h, err := readHeader(st)
	if err != nil {
		lg.Printf("the first stream from %s has no header (%v); closing the connection", peer, err)
		refuse(st, wire.CodeInvalidArgument, "the first stream has no valid header line; open it with {\"s\":\"rpc\"}")
		closeAfterRefusal(conn, "no rpc stream")
		return
	}
	if h.S != "rpc" {
		lg.Printf("the first stream from %s is %q, not rpc; closing the connection", peer, h.S)
		refuse(st, wire.CodeInvalidArgument, fmt.Sprintf("the first stream of a connection is the rpc stream, not '%s'; open it with {\"s\":\"rpc\"}", h.S))
		closeAfterRefusal(conn, "the first stream is not rpc")
		return
	}
	go acceptStreams(ctx, conn, streams, lg)
	srv.ServeConn(ctx, rpcStream{st}, peer)
	conn.CloseWithError(closeNormal, "rpc stream closed")
}

// closeAfterRefusal closes conn with reason once the peer closed it or
// refusalGrace passed.
func closeAfterRefusal(conn *quic.Conn, reason string) {
	select {
	case <-conn.Context().Done():
	case <-time.After(refusalGrace):
	}
	conn.CloseWithError(closeProtocol, reason)
}

// acceptStreams hands each later stream of conn to its handler until conn
// or ctx ends.
func acceptStreams(ctx context.Context, conn *quic.Conn, streams StreamHandlers, lg *log.Logger) {
	for {
		st, err := conn.AcceptStream(ctx)
		if err != nil {
			return
		}
		go serveStream(ctx, st, streams, lg)
	}
}

// serveStream reads the header of st and calls its handler, or refuses a
// stream whose header is missing or names no handler.
func serveStream(ctx context.Context, st *quic.Stream, streams StreamHandlers, lg *log.Logger) {
	h, err := readHeader(st)
	switch {
	case err != nil:
		refuse(st, wire.CodeInvalidArgument, "the stream has no valid header line; write {\"s\":\"…\"} first")
		return
	case h.S == "rpc":
		refuse(st, wire.CodeInvalidArgument, "a connection has one rpc stream, its first; send the request on that stream")
		return
	}
	handler, ok := streams[h.S]
	if !ok {
		refuse(st, wire.CodeCapabilityUnsupported, fmt.Sprintf("this agent does not serve '%s' streams; update deskhand-agent", h.S))
		return
	}
	handler(ctx, st, h)
}

// byteReader reads at most one byte per call, so a bufio.Reader over it
// holds nothing past the line it returns.
type byteReader struct{ r io.Reader }

func (b byteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return b.r.Read(p)
}

// readHeader reads the header line of st within headerTimeout and leaves
// the bytes after it unread.
func readHeader(st *quic.Stream) (wire.StreamHeader, error) {
	var h wire.StreamHeader
	_ = st.SetReadDeadline(time.Now().Add(headerTimeout))
	err := wire.ReadHeader(bufio.NewReader(byteReader{st}), &h)
	_ = st.SetReadDeadline(time.Time{})
	if err == nil && h.S == "" {
		err = errors.New("the header has no s")
	}
	return h, err
}

// refusal is the line written to a refused stream.
type refusal struct {
	OK    bool   `json:"ok"`
	Code  string `json:"code"`
	Error string `json:"error"`
}

// refuse writes the refusal line to st and closes it in both directions.
func refuse(st *quic.Stream, code, text string) {
	_ = st.SetWriteDeadline(time.Now().Add(refusalTimeout))
	_ = wire.WriteHeader(st, refusal{OK: false, Code: code, Error: text})
	st.CancelRead(0)
	_ = st.Close()
}

// rpcStream is the rpc stream; Close ends both of its directions.
type rpcStream struct{ *quic.Stream }

func (s rpcStream) Close() error {
	s.CancelRead(0)
	return s.Stream.Close()
}
