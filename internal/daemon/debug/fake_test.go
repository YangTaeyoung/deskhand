package debug

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	agenttunnel "github.com/YangTaeyoung/deskhand/internal/agent/tunnel"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/daemon/tunnel"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// cdpServer is a fake CDP endpoint: it answers /json/version and /json/list
// over HTTP and speaks the Chrome DevTools Protocol over a WebSocket.
type cdpServer struct {
	t       *testing.T
	ln      net.Listener
	srv     *http.Server
	browser string
	wsPathS string
	targets []jsonTarget
	pages   []page

	respond func(sessionID, method string, params json.RawMessage) (any, *CDPError)

	mu    sync.Mutex
	conns map[*ServerConn]bool
	cmds  map[string]int
	// versionHeld makes /json/version answer 503 until it is closed.
	versionHeld chan struct{}
}

// holdVersion makes /json/version answer 503 until the returned function
// runs.
func (s *cdpServer) holdVersion() func() {
	held := make(chan struct{})
	s.mu.Lock()
	s.versionHeld = held
	s.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(held) }) }
}

// commandCount returns how many times method was received.
func (s *cdpServer) commandCount(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmds[method]
}

// page is a renderer the browser endpoint auto-attaches.
type page struct {
	sessionID string
	targetID  string
	typ       string
	url       string
	title     string
}

func newCDPServer(t *testing.T, browser string) *cdpServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &cdpServer{t: t, ln: ln, browser: browser, wsPathS: "/ws", conns: map[*ServerConn]bool{}, cmds: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		held := s.versionHeld
		s.mu.Unlock()
		if held != nil {
			select {
			case <-held:
			default:
				http.Error(w, "not listening yet", http.StatusServiceUnavailable)
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Browser": s.browser, "Protocol-Version": "1.3",
			"webSocketDebuggerUrl": s.wsURL(s.wsPathS)})
	})
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(s.targets)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns[c] = true
		s.mu.Unlock()
		go s.read(c)
	})
	s.srv = &http.Server{Handler: mux}
	go s.srv.Serve(ln)
	t.Cleanup(func() { s.srv.Close() })
	return s
}

func (s *cdpServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *cdpServer) wsURL(path string) string {
	return fmt.Sprintf("ws://127.0.0.1:%d%s", s.port(), path)
}

// withNodeTarget adds the single Node inspector target of /json/list.
func (s *cdpServer) withNodeTarget(id string) *cdpServer {
	s.targets = []jsonTarget{{ID: id, Type: "node", Title: "node", URL: "file:///app/main.js",
		WebSocketDebuggerURL: s.wsURL("/" + id)}}
	return s
}

// withPage adds a renderer auto-attached on Target.setAutoAttach.
func (s *cdpServer) withPage(p page) *cdpServer {
	s.pages = append(s.pages, p)
	return s
}

func (s *cdpServer) read(c *ServerConn) {
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		c.Close()
	}()
	for {
		msg, err := c.ReadMessage()
		if err != nil {
			return
		}
		var m struct {
			ID        int64           `json:"id"`
			SessionID string          `json:"sessionId"`
			Method    string          `json:"method"`
			Params    json.RawMessage `json:"params"`
		}
		if json.Unmarshal(msg, &m) != nil {
			continue
		}
		result, cdpErr := s.handle(c, m.SessionID, m.Method, m.Params)
		reply := map[string]any{"id": m.ID}
		if m.SessionID != "" {
			reply["sessionId"] = m.SessionID
		}
		if cdpErr != nil {
			reply["error"] = cdpErr
		} else {
			if result == nil {
				result = map[string]any{}
			}
			reply["result"] = result
		}
		b, _ := json.Marshal(reply)
		_ = c.WriteText(b)
		if m.Method == "Target.setAutoAttach" {
			for _, p := range s.pages {
				s.emit(c, "", "Target.attachedToTarget", map[string]any{"sessionId": p.sessionID, "waitingForDebugger": true,
					"targetInfo": map[string]any{"targetId": p.targetID, "type": p.typ, "title": p.title, "url": p.url, "attached": true}})
			}
		}
	}
}

func (s *cdpServer) handle(c *ServerConn, sessionID, method string, params json.RawMessage) (any, *CDPError) {
	s.mu.Lock()
	s.cmds[method]++
	s.mu.Unlock()
	if s.respond != nil {
		if r, e := s.respond(sessionID, method, params); r != nil || e != nil {
			return r, e
		}
	}
	switch method {
	case "Runtime.enable", "Debugger.enable", "Log.enable", "Debugger.setPauseOnExceptions",
		"Debugger.setBreakpointsActive", "Runtime.runIfWaitingForDebugger", "Debugger.pause", "Debugger.resume",
		"Debugger.stepOver", "Debugger.stepInto", "Debugger.stepOut", "Runtime.releaseObjectGroup",
		"Runtime.terminateExecution", "Debugger.removeBreakpoint", "Target.setAutoAttach":
		return map[string]any{}, nil
	case "Debugger.setBreakpointByUrl":
		return map[string]any{"breakpointId": "cdp-1", "locations": []any{}}, nil
	}
	return map[string]any{}, nil
}

// emit pushes an event to a connection.
func (s *cdpServer) emit(c *ServerConn, sessionID, method string, params any) {
	msg := map[string]any{"method": method, "params": params}
	if sessionID != "" {
		msg["sessionId"] = sessionID
	}
	b, _ := json.Marshal(msg)
	_ = c.WriteText(b)
}

// emitAll pushes an event to every live connection.
func (s *cdpServer) emitAll(sessionID, method string, params any) {
	s.mu.Lock()
	conns := make([]*ServerConn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		s.emit(c, sessionID, method, params)
	}
}

func (s *cdpServer) connCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// closedPort returns a loopback port with nothing listening.
func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return p
}

// fakeAgent carries the daemon's tunnels to the fake CDP servers over
// loopback and answers the agent ops a launch and attach send.
type fakeAgent struct {
	mu      sync.Mutex
	state   conn.ConnState
	agentL  net.Listener
	ports   Ports
	treeDer func() (bool, *int)
	kills   []int
	calls   []string
}

func newFakeAgent(t *testing.T, p1, p2 int) *fakeAgent {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	a := &fakeAgent{state: conn.StateReady, agentL: l, ports: Ports{Inspect: p1, RemoteDebugging: p2},
		treeDer: func() (bool, *int) { return true, nil }}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				br := bufio.NewReader(c)
				var hdr wire.StreamHeader
				if wire.ReadHeader(br, &hdr) != nil {
					c.Close()
					return
				}
				agenttunnel.Serve(context.Background(), agentStream{tcpStream{c.(*net.TCPConn)}}, hdr)
				c.Close()
			}()
		}
	}()
	return a
}

func (a *fakeAgent) ID() string          { return "h-fake" }
func (a *fakeAgent) Target() conn.Target { return conn.Target{Name: "fake"} }
func (a *fakeAgent) State() conn.ConnState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}
func (a *fakeAgent) Hello() conn.Hello {
	return conn.Hello{Caps: map[string]conn.Capability{"tunnel": {State: "ready"}}}
}
func (a *fakeAgent) OpenStream(ctx context.Context, hdr wire.StreamHeader) (conn.DataStream, uint64, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", a.agentL.Addr().String())
	if err != nil {
		return nil, 0, err
	}
	if err := wire.WriteHeader(c, hdr); err != nil {
		c.Close()
		return nil, 0, err
	}
	return tcpStream{c.(*net.TCPConn)}, 1, nil
}

func (a *fakeAgent) Call(ctx context.Context, op string, params proto.Message, opts conn.CallOpts) (proto.Message, error) {
	a.mu.Lock()
	a.calls = append(a.calls, op)
	a.mu.Unlock()
	switch op {
	case "debug.electron.launch":
		return proto.Message{"pid": 4321, "ports": map[string]any{"inspect": a.ports.Inspect, "remote_debugging": a.ports.RemoteDebugging}}, nil
	case "debug.attach.pid":
		return proto.Message{"inspect_port": a.ports.Inspect, "remote_debugging_port": a.ports.RemoteDebugging, "inspector_opened": true}, nil
	case "debug.tree":
		alive, code := a.treeDer()
		out := proto.Message{"alive": alive}
		if code != nil {
			out["exit_code"] = *code
		}
		return out, nil
	case "proc.kill":
		a.mu.Lock()
		a.kills = append(a.kills, params.Int("pid"))
		a.mu.Unlock()
		return proto.Message{"killed": 1}, nil
	}
	return proto.Message{}, nil
}

// tcpStream is a TCP connection as a data stream of both ends.
type tcpStream struct{ *net.TCPConn }

func (s tcpStream) CloseWrite() error { return s.TCPConn.CloseWrite() }
func (s tcpStream) CancelWrite()      { s.TCPConn.Close() }
func (s tcpStream) CancelRead()       { s.TCPConn.CloseRead() }

// agentStream is the agent's end of a tunnel stream.
type agentStream struct{ tcpStream }

func (s agentStream) Close() error { return s.TCPConn.CloseWrite() }

// fakeHosts resolves a single fake agent.
type fakeHosts struct{ a *fakeAgent }

func (h fakeHosts) Conn(string) (Agent, HostInfo, error) {
	return h.a, HostInfo{HostID: "h-fake", Name: "fake"}, nil
}
func (h fakeHosts) SessionID(string) string { return "" }

// debugEnv is a Manager over a real tunnel manager and a fake agent.
type debugEnv struct {
	m    *Manager
	a    *fakeAgent
	tuns *tunnel.Manager
	sub  *events.Sub
}

func newDebugEnv(t *testing.T, p1, p2 int) *debugEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bus := events.NewBus(st, nil)
	tuns := tunnel.New(nil)
	t.Cleanup(tuns.Shutdown)
	a := newFakeAgent(t, p1, p2)
	m := New(Deps{Tunnels: tuns, Hosts: fakeHosts{a: a}, Bus: bus,
		Timeouts: Timeouts{Probe: 2 * time.Second, Poll: 20 * time.Millisecond, Call: 2 * time.Second, Evaluate: 2 * time.Second, Retain: time.Minute}})
	t.Cleanup(m.Shutdown)
	sub := bus.Subscribe()
	t.Cleanup(sub.Close)
	return &debugEnv{m: m, a: a, tuns: tuns, sub: sub}
}

// waitSession waits for a debug.session_changed of the session debugID
// whose session ok accepts.
func (e *debugEnv) waitSession(t *testing.T, debugID string, ok func(Session) bool) Session {
	t.Helper()
	deadline := time.After(3 * e.m.tmo.Probe)
	for {
		select {
		case ev := <-e.sub.C:
			if ev.Kind != evSessionChanged {
				continue
			}
			if s, _ := ev.Fields["session"].(Session); s.DebugID == debugID && ok(s) {
				return s
			}
		case <-deadline:
			t.Fatalf("no debug.session_changed of %s matched", debugID)
		}
	}
}

// waitEvent waits for a debugger event of kind with a matching debug_id.
func (e *debugEnv) waitEvent(t *testing.T, kind string) events.Event {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-e.sub.C:
			if ev.Kind == kind {
				return ev
			}
		case <-deadline:
			t.Fatalf("event %s did not arrive", kind)
		}
	}
}
