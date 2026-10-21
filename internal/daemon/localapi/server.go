// Package localapi serves the daemon's local API: NDJSON JSON-RPC 2.0 over
// a unix socket (docs/local-api.md).
package localapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/control"
	"github.com/YangTaeyoung/deskhand/internal/daemon/debug"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/hosts"
	"github.com/YangTaeyoung/deskhand/internal/daemon/ids"
	"github.com/YangTaeyoung/deskhand/internal/daemon/jobs"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/observe"
	"github.com/YangTaeyoung/deskhand/internal/daemon/paths"
	"github.com/YangTaeyoung/deskhand/internal/daemon/pty"
	"github.com/YangTaeyoung/deskhand/internal/daemon/qa"
	"github.com/YangTaeyoung/deskhand/internal/daemon/screen"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/daemon/tunnel"
	"github.com/YangTaeyoung/deskhand/internal/daemon/xfer"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeServer         = -32000
)

const (
	// maxRequestLine bounds one request line.
	maxRequestLine = 16 << 20
	// writeTimeout closes a connection whose write does not finish in time.
	writeTimeout = 30 * time.Second
)

// ErrInUse is returned by Listen when another daemon answers on the socket.
var ErrInUse = errors.New("Deskhand 데몬이 이미 이 소켓에서 실행 중입니다. deskhand daemon status로 확인하세요")

// Listen removes a stale socket at path and listens on it with mode 0600 in
// a folder of mode 0700.
func Listen(path string) (net.Listener, error) {
	if err := paths.CheckSocket(path); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("소켓 폴더를 만들지 못했습니다. %s의 권한을 확인하세요: %w", filepath.Dir(path), err)
	}
	if _, err := os.Lstat(path); err == nil {
		if c, err := net.DialTimeout("unix", path, 500*time.Millisecond); err == nil {
			c.Close()
			return nil, ErrInUse
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("남아 있는 소켓 %s를 지우지 못했습니다. 파일을 직접 지우고 다시 시작하세요: %w", path, err)
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("소켓 %s를 열지 못했습니다. 경로와 권한을 확인하세요: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("소켓 %s의 권한을 바꾸지 못했습니다. 폴더 소유자를 확인하세요: %w", path, err)
	}
	return ln, nil
}

// Deps are what the methods use.
type Deps struct {
	Store *store.Store
	Bus   *events.Bus
	Hosts *hosts.Registry
	Jobs  *jobs.Registry
	// Transfers serves the transfer methods; nil leaves them out.
	Transfers *xfer.Manager
	// PTYs serves pty.open and the pty streams; nil leaves them out.
	PTYs *pty.Hub
	// Tunnels serves the tunnel methods; nil leaves them out.
	Tunnels *tunnel.Manager
	// Debug serves the debug methods; nil leaves them out.
	Debug *debug.Manager
	// QA serves the qa methods; nil leaves them out.
	QA *qa.Manager
	// Screens serves the screen streams; nil leaves them out.
	Screens *screen.Hub
	// Control serves the control methods and gives the UI operations their
	// writer generation; nil leaves the methods out and sends writer
	// generation 0.
	Control *control.Manager
	// Observe serves screen.capture, the input methods and observe; nil
	// leaves them out.
	Observe *observe.Service
	Version string
	Logger  *log.Logger
}

// handler serves one method for a session.
type handler func(ctx context.Context, s *session, params json.RawMessage) (any, error)

// streamHandler serves one kind of stream.attach: it checks the params and
// prepares the stream, and returns the result of the attach and the
// function that then runs the stream on the connection, which it owns and
// closes.
type streamHandler func(ctx context.Context, s *session, params json.RawMessage) (result any, run func(rw io.ReadWriteCloser), err error)

// Server serves local API connections.
type Server struct {
	d       Deps
	methods map[string]handler
	streams map[string]streamHandler

	// base outlives the connections and ends with Close; operations that
	// change the PC run under it.
	base context.Context
	stop context.CancelFunc

	mu       sync.Mutex
	ln       net.Listener
	sessions map[*session]struct{}
	closed   bool
	wg       sync.WaitGroup
}

// New returns a Server.
func New(d Deps) *Server {
	if d.Logger == nil {
		d.Logger = log.New(io.Discard, "", 0)
	}
	s := &Server{d: d, sessions: map[*session]struct{}{}}
	s.base, s.stop = context.WithCancel(context.Background())
	s.methods = map[string]handler{}
	s.streams = map[string]streamHandler{}
	s.registerSession()
	s.registerHosts()
	s.registerExec()
	s.registerJobs()
	s.registerFiles()
	s.registerProc()
	s.registerTransfer()
	s.registerPTY()
	s.registerTunnel()
	s.registerDebug()
	s.registerDiag()
	s.registerQA()
	s.registerControl()
	s.registerScreen()
	s.registerInput()
	s.registerUI()
	return s
}

// Serve accepts connections on ln until Close; it returns nil after Close.
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		ln.Close()
		return nil
	}
	s.ln = ln
	s.mu.Unlock()
	for {
		c, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			return err
		}
		sess := s.newSession(c)
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			c.Close()
			continue
		}
		s.sessions[sess] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			sess.serve()
			s.mu.Lock()
			delete(s.sessions, sess)
			s.mu.Unlock()
		}()
	}
}

// Close stops accepting, ends the running operations, closes every
// connection and waits for them.
func (s *Server) Close() {
	s.stop()
	s.mu.Lock()
	s.closed = true
	ln := s.ln
	sessions := make([]*session, 0, len(s.sessions))
	for sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()
	if ln != nil {
		ln.Close()
	}
	for _, sess := range sessions {
		sess.close()
	}
	s.wg.Wait()
}

// filter selects the events a session receives.
type filter struct {
	hostID string
	kinds  map[string]bool
}

func (f *filter) match(ev events.Event) bool {
	if f.hostID != "" && ev.HostID != f.hostID {
		return false
	}
	return len(f.kinds) == 0 || f.kinds[ev.Kind]
}

// session is one local API connection.
type session struct {
	srv    *Server
	c      net.Conn
	ctx    context.Context
	cancel context.CancelFunc

	wmu sync.Mutex

	mu     sync.Mutex
	hello  bool
	caller model.Caller
	filt   *filter
	sub    *events.Sub
	wg     sync.WaitGroup
}

func (s *Server) newSession(c net.Conn) *session {
	ctx, cancel := context.WithCancel(context.Background())
	return &session{srv: s, c: c, ctx: ctx, cancel: cancel}
}

func (s *session) close() {
	s.cancel()
	_ = s.c.Close()
}

// Caller returns who the session's client is.
func (s *session) Caller() model.Caller {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.caller
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// errLineTooLong ends a connection whose request line exceeds
// maxRequestLine.
var errLineTooLong = errors.New("request line too long")

// readLine returns the next line of r with its newline, or the bytes before
// the end of r; a line longer than max is errLineTooLong. Nothing after the
// line is consumed beyond what r buffers.
func readLine(r *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(line)+len(chunk) > max {
			return nil, errLineTooLong
		}
		line = append(line, chunk...)
		switch {
		case err == nil:
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(line) > 0:
			return line, nil
		}
		return nil, err
	}
}

// serve reads requests until the connection ends. hello, events.subscribe
// and stream.attach run in order; other methods run concurrently. A
// stream.attach that succeeds hands the connection over to its stream,
// which runs until it ends. The AI control the client holds ends with the
// connection.
func (s *session) serve() {
	defer func() {
		s.close()
		s.wg.Wait()
		s.mu.Lock()
		sub := s.sub
		s.mu.Unlock()
		if sub != nil {
			sub.Close()
		}
		if c := s.srv.d.Control; c != nil {
			c.ClientClosed(s.Caller().ClientID)
		}
	}()
	br := bufio.NewReaderSize(s.c, 64<<10)
	for {
		raw, err := readLine(br, maxRequestLine)
		if err != nil {
			return
		}
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			s.reply(nil, nil, rpcErr(codeParse, apierr.Invalid("요청이 올바른 JSON이 아닙니다. 한 줄에 JSON-RPC 2.0 객체 하나를 보내세요")))
			continue
		}
		if req.JSONRPC != "2.0" || req.Method == "" {
			s.reply(req.ID, nil, rpcErr(codeInvalidRequest, apierr.Invalid(`요청에 "jsonrpc":"2.0"과 method가 있어야 합니다. 요청 형식을 확인하세요`)))
			continue
		}
		s.mu.Lock()
		greeted := s.hello
		s.mu.Unlock()
		switch {
		case req.Method == "stream.attach" && greeted:
			if s.attach(req, br) {
				return
			}
		case req.Method == "hello" || !greeted || req.Method == "events.subscribe":
			s.dispatch(req)
		default:
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.dispatch(req)
			}()
		}
	}
}

func (s *session) dispatch(req request) {
	s.mu.Lock()
	greeted := s.hello
	s.mu.Unlock()
	if !greeted && req.Method != "hello" {
		s.reply(req.ID, nil, rpcErr(codeServer, apierr.Invalid("첫 요청은 hello여야 합니다. hello{client, name, version}를 먼저 보내세요")))
		return
	}
	h, ok := s.srv.methods[req.Method]
	if !ok {
		s.reply(req.ID, nil, rpcErr(codeMethodNotFound, apierr.New(wire.CodeCapabilityUnsupported,
			fmt.Sprintf("이 데몬은 %s를 지원하지 않습니다. deskhand를 업데이트하거나 메서드 이름을 확인하세요", req.Method), false, "")))
		return
	}
	res, err := s.call(h, req)
	if err != nil {
		code := codeServer
		var pe *paramError
		if errors.As(err, &pe) {
			code = codeInvalidParams
		}
		s.reply(req.ID, nil, rpcErr(code, apierr.As(err)))
		return
	}
	s.reply(req.ID, res, nil)
}

// rawConn is the connection of a session after stream.attach: reads take
// what the session's reader buffered first.
type rawConn struct {
	r *bufio.Reader
	net.Conn
}

func (c rawConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// attachParams are the params of stream.attach that pick its handler.
type attachParams struct {
	Kind string `json:"kind"`
}

// attach serves stream.attach: once the requests in flight on the session
// have been answered, the handler of params.kind prepares the stream, the
// reply goes out and the stream runs on the connection with what br
// buffered. It reports whether the connection was handed over; a refused
// attach is answered with its error and the session goes on.
func (s *session) attach(req request, br *bufio.Reader) bool {
	s.mu.Lock()
	subscribed := s.sub != nil
	s.mu.Unlock()
	if subscribed {
		s.reply(req.ID, nil, rpcErr(codeServer, apierr.Invalid("events.subscribe를 부른 연결에서는 stream.attach를 쓸 수 없습니다. 새 연결에서 hello 다음 stream.attach를 부르세요")))
		return false
	}
	s.wg.Wait()
	var p attachParams
	if err := decode(req.Params, &p); err != nil {
		s.reply(req.ID, nil, rpcErr(codeInvalidParams, apierr.As(err)))
		return false
	}
	h, ok := s.srv.streams[p.Kind]
	if !ok {
		s.reply(req.ID, nil, rpcErr(codeInvalidParams, apierr.Invalid(fmt.Sprintf("stream.attach의 kind %q를 모릅니다. kind는 screen이나 pty로 주세요", p.Kind))))
		return false
	}
	result, run, err := s.callStream(h, req)
	if err != nil {
		code := codeServer
		var pe *paramError
		if errors.As(err, &pe) {
			code = codeInvalidParams
		}
		s.reply(req.ID, nil, rpcErr(code, apierr.As(err)))
		return false
	}
	s.reply(req.ID, result, nil)
	run(rawConn{r: br, Conn: s.c})
	return true
}

// callStream runs a stream handler, turning a panic into an INTERNAL error.
func (s *session) callStream(h streamHandler, req request) (result any, run func(io.ReadWriteCloser), err error) {
	defer func() {
		if r := recover(); r != nil {
			s.srv.d.Logger.Printf("stream.attach panicked: %v", r)
			result, run, err = nil, nil, apierr.Internal(fmt.Sprintf("stream.attach 처리 중 데몬 내부 오류가 났습니다. %s 로그를 확인하세요", paths.DaemonLog()))
		}
	}()
	return h(s.ctx, s, req.Params)
}

func (s *session) call(h handler, req request) (res any, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.srv.d.Logger.Printf("method %s panicked: %v", req.Method, r)
			res, err = nil, apierr.Internal(fmt.Sprintf("%s 처리 중 데몬 내부 오류가 났습니다. %s 로그를 확인하세요", req.Method, paths.DaemonLog()))
		}
	}()
	return h(s.ctx, s, req.Params)
}

// rpcError is the error member of a response.
type rpcError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
}

func rpcErr(code int, e *apierr.Error) *rpcError {
	data := map[string]any{"code": e.Code, "retryable": e.Retryable}
	if e.NextAction != "" {
		data["next_action"] = e.NextAction
	}
	return &rpcError{Code: code, Message: e.Message, Data: data}
}

// reply writes the response to the request with id. A request without an
// id is a notification and gets no response; only a parse error, whose id
// cannot be read, is answered with id null.
func (s *session) reply(id json.RawMessage, result any, e *rpcError) {
	if len(id) == 0 && (e == nil || e.Code != codeParse) {
		return
	}
	var idv any = json.RawMessage("null")
	if len(id) > 0 {
		idv = id
	}
	msg := map[string]any{"jsonrpc": "2.0", "id": idv}
	if e != nil {
		msg["error"] = e
	} else {
		if result == nil {
			result = map[string]any{}
		}
		msg["result"] = result
	}
	s.write(msg)
}

// write sends one JSON line; a failed write closes the connection.
func (s *session) write(v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		s.srv.d.Logger.Printf("encode a response: %v", err)
		return
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_ = s.c.SetWriteDeadline(time.Now().Add(writeTimeout))
	if _, err := s.c.Write(buf.Bytes()); err != nil {
		s.close()
	}
}

// subscribe sets the session's event filter, starting delivery on the
// first call.
func (s *session) subscribe(f *filter) {
	s.mu.Lock()
	s.filt = f
	start := s.sub == nil
	if start {
		s.sub = s.srv.d.Bus.Subscribe()
	}
	sub := s.sub
	s.mu.Unlock()
	if start {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.pump(sub)
		}()
	}
}

// pump writes the subscription's events that pass the filter, and an
// events.dropped notification after events were dropped.
func (s *session) pump(sub *events.Sub) {
	for {
		select {
		case <-s.ctx.Done():
			return
		case ev := <-sub.C:
			s.mu.Lock()
			f := s.filt
			s.mu.Unlock()
			if f != nil && f.match(ev) {
				s.write(map[string]any{"jsonrpc": "2.0", "method": "event", "params": ev.Params()})
			}
			if n := sub.TakeDropped(); n > 0 {
				s.write(map[string]any{"jsonrpc": "2.0", "method": "event", "params": map[string]any{
					"seq": 0, "ts": model.Time(time.Now()), "kind": events.EventsDropped, "count": n}})
			}
		}
	}
}

// paramError is an error decoding the params of a method.
type paramError struct{ e *apierr.Error }

func (e *paramError) Error() string { return e.e.Message }
func (e *paramError) Unwrap() error { return e.e }

// decode reads params into v; absent params leave v as it is.
func decode(params json.RawMessage, v any) error {
	if len(bytes.TrimSpace(params)) == 0 || string(bytes.TrimSpace(params)) == "null" {
		return nil
	}
	if err := json.Unmarshal(params, v); err != nil {
		return &paramError{e: apierr.Invalid(fmt.Sprintf("파라미터를 읽지 못했습니다(%v). docs/local-api.md의 파라미터 형식을 확인하세요", err))}
	}
	return nil
}

// newClientID returns a new client id.
func newClientID() string { return ids.New(ids.Client) }
