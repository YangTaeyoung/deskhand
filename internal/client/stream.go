package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Stream is a connection that stream.attach turned into binary frames
// (docs/local-api.md §4).
type Stream struct {
	// Result is the result of stream.attach.
	Result map[string]any

	c   net.Conn
	r   *bufio.Reader
	wmu sync.Mutex
}

// attachTimeout bounds hello and stream.attach on a stream connection.
const attachTimeout = 60 * time.Second

// Attach opens a connection of its own to the daemon, starting it as
// DialOptions does, sends hello as clientKind named name and then
// stream.attach with kind and params, and returns the stream once the
// daemon accepted it. A refusal is an *Error.
func Attach(ctx context.Context, opt Options, clientKind, name, kind string, params map[string]any) (*Stream, error) {
	opt = opt.withDefaults()
	dial := func() (net.Conn, error) {
		var d net.Dialer
		dctx, cancel := context.WithTimeout(ctx, probeTimeout)
		defer cancel()
		return d.DialContext(dctx, "unix", opt.Socket)
	}
	nc, err := dial()
	if err != nil {
		if err := ensure(ctx, opt); err != nil {
			return nil, err
		}
		if nc, err = dial(); err != nil {
			return nil, err
		}
	}
	s := &Stream{c: nc, r: bufio.NewReaderSize(nc, 64<<10)}
	deadline := time.Now().Add(attachTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = nc.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = nc.SetDeadline(time.Now()) })
	if _, err := s.request(1, "hello", map[string]any{"client": clientKind, "name": name, "version": opt.Version}); err != nil {
		stop()
		nc.Close()
		return nil, err
	}
	attach := map[string]any{"kind": kind}
	for k, v := range params {
		attach[k] = v
	}
	res, err := s.request(2, "stream.attach", attach)
	if !stop() && err == nil {
		err = ctx.Err()
	}
	if err != nil {
		nc.Close()
		if ctx.Err() != nil {
			return nil, apierr.New(wire.CodeTimeout, "스트림 연결을 기다리는 동안 시간이 다 됐습니다. 다시 시도하세요", true, apierr.NextRetry)
		}
		return nil, err
	}
	_ = json.Unmarshal(res, &s.Result)
	_ = nc.SetDeadline(time.Time{})
	return s, nil
}

// request writes one request with id and reads lines until its response.
func (s *Stream) request(id int, method string, params any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, apierr.Invalid(fmt.Sprintf("%s 파라미터를 JSON으로 만들 수 없습니다(%v). 값을 확인하세요", method, err))
	}
	if _, err := s.c.Write(buf.Bytes()); err != nil {
		return nil, errClosed
	}
	for {
		line, err := s.r.ReadBytes('\n')
		if err != nil {
			return nil, errClosed
		}
		var m struct {
			ID *int `json:"id"`
			response
		}
		if json.Unmarshal(line, &m) != nil || m.ID == nil || *m.ID != id {
			continue
		}
		var raw json.RawMessage
		if err := decodeResponse(method, m.response, &raw); err != nil {
			return nil, err
		}
		return raw, nil
	}
}

// ReadFrame reads the next frame of at most max bytes.
func (s *Stream) ReadFrame(max int) (kind byte, payload []byte, err error) {
	return wire.ReadFrame(s.r, max)
}

// WriteFrame writes one frame; writes from several goroutines do not
// interleave.
func (s *Stream) WriteFrame(kind byte, payload []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_ = s.c.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return wire.WriteFrame(s.c, kind, payload)
}

// Close closes the connection.
func (s *Stream) Close() error { return s.c.Close() }
