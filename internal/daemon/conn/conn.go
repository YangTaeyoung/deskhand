// Package conn owns the daemon's connections to host agents: it connects
// over QUIC, reconnects with backoff, counts connection generations, sends
// rpc requests with request ids and settles requests whose answer was lost
// through req.status (docs/protocol.md §1, §3; docs/design.md §5.2).
package conn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/ids"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/transport"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// ConnState is the connection state of a host.
type ConnState string

// Connection states.
const (
	StateOffline      ConnState = "offline"
	StateConnecting   ConnState = "connecting"
	StateReady        ConnState = "ready"
	StateDegraded     ConnState = "degraded"
	StateReconnecting ConnState = "reconnecting"
	StateBlocked      ConnState = "blocked"
)

// Event kinds a Manager reports.
const (
	// KindConnectionState carries state, connection_generation and, after a
	// failure, reason.
	KindConnectionState = "connection.state"
	// KindAgentEvent carries an ev message of the agent: name and its fields.
	KindAgentEvent = "agent.event"
)

// Event is a change a Manager reports.
type Event struct {
	HostID  string
	Kind    string
	Payload proto.Message
}

// Request states written to the RequestLog.
const (
	RequestSent    = "sent"
	RequestDone    = "done"
	RequestUnknown = "unknown"
	// RequestNotSent is a recorded request that no connection took.
	RequestNotSent = "not_sent"
)

// RequestLog records the requests sent with a request id.
type RequestLog interface {
	RequestSent(rid, hostID, op string) error
	SetRequestState(rid, state string, result json.RawMessage) error
}

// Journaled reports whether op changes state on the PC (wire.Journaled);
// requests of such an op carry a rid and are never sent twice.
func Journaled(op string) bool { return wire.Journaled(op) }

// NewRID returns a new request id: "r-" and a ULID.
func NewRID() string { return ids.New(ids.Request) }

// Capability is the state of one agent capability; Shells lists the shells
// of the pty capability.
type Capability struct {
	State  string   `json:"state"`
	Reason string   `json:"reason,omitempty"`
	Shells []string `json:"shells,omitempty"`
}

// Hello is what the agent's hello announced.
type Hello struct {
	Agent    string
	OS       string
	Arch     string
	Host     string
	BootID   string
	Caps     map[string]Capability
	Session  proto.Message
	Displays []any
}

func parseHello(m proto.Message) Hello {
	h := Hello{Agent: m.String("agent"), OS: m.String("os"), Arch: m.String("arch"), Host: m.String("host"), BootID: m.String("boot_id")}
	h.Caps = parseCaps(m["caps"])
	if s, ok := m["session"].(map[string]any); ok {
		h.Session = proto.Message(s)
	}
	if d, ok := m["displays"].([]any); ok {
		h.Displays = d
	}
	return h
}

func parseCaps(v any) map[string]Capability {
	out := map[string]Capability{}
	obj, ok := v.(map[string]any)
	if !ok {
		return out
	}
	for k, c := range obj {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		state, _ := cm["state"].(string)
		reason, _ := cm["reason"].(string)
		var shells []string
		if list, ok := cm["shells"].([]any); ok {
			for _, v := range list {
				if sh, ok := v.(string); ok {
					shells = append(shells, sh)
				}
			}
		}
		out[k] = Capability{State: state, Reason: reason, Shells: shells}
	}
	return out
}

// Options configure a Manager.
type Options struct {
	// Identity is the controller certificate presented to agents.
	Identity transport.Identity
	// Controller is the controller name sent in the welcome.
	Controller string
	// Dial connects to an agent; nil means DialQUIC.
	Dial Dialer
	// Requests records requests with a rid; nil records nothing.
	Requests RequestLog
	// OnEvent receives state changes and agent events; it runs on the
	// connection's goroutines and must not block.
	OnEvent func(Event)
	// OnReady runs when a host connected, with its new generation.
	OnReady func(hostID string, gen uint64)
	// Prepare runs on each new connection before it becomes ready, with
	// call sending requests on that connection; the agent's events wait
	// until the connection is ready. An error ends the connection, which is
	// then retried like one that failed. nil skips it.
	Prepare func(ctx context.Context, hostID string, call PrepareCall) error
	Backoff BackoffConfig
	// Sleep waits between reconnect attempts; nil waits on a timer.
	Sleep func(ctx context.Context, d time.Duration) error
	// DropAfterSend names an op whose first request's answer is discarded
	// by closing the connection when it arrives (the test hook
	// DESKHAND_TEST_DROP_AFTER_SEND).
	DropAfterSend string
	// ReadyWait bounds how long a call waits for a connecting host (10 s
	// when 0).
	ReadyWait time.Duration
	// ResolveWait bounds how long a call whose answer was lost waits to
	// settle its outcome (30 s when 0).
	ResolveWait time.Duration
	// PingInterval is the time between liveness pings (15 s when 0);
	// PingTimeout bounds their answer (5 s when 0).
	PingInterval time.Duration
	PingTimeout  time.Duration
	Logger       *log.Logger
}

const dialTimeout = 10 * time.Second

// prepareTimeout bounds Options.Prepare.
const prepareTimeout = 10 * time.Second

// PrepareCall sends op with params on the connection that Options.Prepare
// prepares and returns the fields of its answer; an answer with ok false is
// an *apierr.Error.
type PrepareCall func(ctx context.Context, op string, params proto.Message) (proto.Message, error)

// Manager owns the hosts' connections.
type Manager struct {
	opt    Options
	ctx    context.Context
	cancel context.CancelFunc

	mu    sync.Mutex
	hosts map[string]*Host

	dropped atomic.Bool
}

// NewManager returns a Manager without hosts.
func NewManager(opt Options) *Manager {
	if opt.Dial == nil {
		opt.Dial = DialQUIC
	}
	if opt.Sleep == nil {
		opt.Sleep = sleepCtx
	}
	if opt.ReadyWait <= 0 {
		opt.ReadyWait = 10 * time.Second
	}
	if opt.ResolveWait <= 0 {
		opt.ResolveWait = 30 * time.Second
	}
	if opt.PingInterval <= 0 {
		opt.PingInterval = 15 * time.Second
	}
	if opt.PingTimeout <= 0 {
		opt.PingTimeout = 5 * time.Second
	}
	if opt.Logger == nil {
		opt.Logger = log.New(io.Discard, "", 0)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{opt: opt, ctx: ctx, cancel: cancel, hosts: map[string]*Host{}}
}

// Add registers the host of t, or updates the target of the host already
// registered with t.HostID, and returns it; it does not connect.
func (m *Manager) Add(t Target) *Host {
	m.mu.Lock()
	defer m.mu.Unlock()
	if h, ok := m.hosts[t.HostID]; ok {
		h.SetTarget(t)
		return h
	}
	h := &Host{m: m, id: t.HostID, target: t, state: StateOffline, changed: make(chan struct{})}
	m.hosts[t.HostID] = h
	return h
}

// Get returns the host with hostID, or nil.
func (m *Manager) Get(hostID string) *Host {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hosts[hostID]
}

// Remove disconnects the host and forgets it.
func (m *Manager) Remove(hostID string) {
	m.mu.Lock()
	h := m.hosts[hostID]
	delete(m.hosts, hostID)
	m.mu.Unlock()
	if h != nil {
		h.Disconnect()
	}
}

// Close disconnects every host.
func (m *Manager) Close() {
	m.mu.Lock()
	hosts := make([]*Host, 0, len(m.hosts))
	for _, h := range m.hosts {
		hosts = append(hosts, h)
	}
	m.mu.Unlock()
	for _, h := range hosts {
		h.Disconnect()
	}
	m.cancel()
}

func (m *Manager) takeDrop(op string) bool {
	return m.opt.DropAfterSend != "" && op == m.opt.DropAfterSend && m.dropped.CompareAndSwap(false, true)
}

func (m *Manager) emit(ev Event) {
	if m.opt.OnEvent != nil {
		m.opt.OnEvent(ev)
	}
}

// Host is one host's connection.
type Host struct {
	m  *Manager
	id string

	// emitMu orders the state events as the state changes.
	emitMu sync.Mutex

	mu       sync.Mutex
	target   Target
	state    ConnState
	gen      uint64
	hello    Hello
	session  proto.Message
	lastErr  error
	lastSeen time.Time
	rpc      *rpcClient
	changed  chan struct{}
	cancel   context.CancelFunc
	loopDone chan struct{}
	pinging  bool
	// blockKind is BlockFingerprint or BlockRejected while blocked.
	blockKind string
}

// Reasons of a blocked host.
const (
	// BlockFingerprint: the agent's certificate is not the paired one.
	BlockFingerprint = "fingerprint"
	// BlockRejected: the agent refused this controller's certificate.
	BlockRejected = "rejected"
)

// BlockReason returns BlockFingerprint or BlockRejected while the host is
// blocked, and "" otherwise.
func (h *Host) BlockReason() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state != StateBlocked {
		return ""
	}
	return h.blockKind
}

// ID returns the host id.
func (h *Host) ID() string { return h.id }

// Target returns where the host's agent is reached.
func (h *Host) Target() Target {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.target
}

// SetTarget replaces the target; the next connection uses it.
func (h *Host) SetTarget(t Target) {
	h.mu.Lock()
	h.target = t
	h.mu.Unlock()
}

// State returns the connection state.
func (h *Host) State() ConnState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state
}

// Generation returns the number of connections made so far; it grows by
// one with every connection.
func (h *Host) Generation() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.gen
}

// Hello returns what the agent announced on the last connection.
func (h *Host) Hello() Hello {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hello
}

// SessionInfo returns the session.info answer of the last connection, or
// nil when the agent did not give one.
func (h *Host) SessionInfo() proto.Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.session
}

// LastError returns why the host is not connected, or nil.
func (h *Host) LastError() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastErr
}

// LastSeen returns when the host last connected or answered.
func (h *Host) LastSeen() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastSeen
}

// WaitState waits until pred accepts the state or ctx ends.
func (h *Host) WaitState(ctx context.Context, pred func(ConnState) bool) error {
	for {
		h.mu.Lock()
		st, ch := h.state, h.changed
		h.mu.Unlock()
		if pred(st) {
			return nil
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// setStateLocked changes the state and returns the event to emit, or nil
// when nothing changed. h.mu is held.
func (h *Host) setStateLocked(st ConnState, err error) *Event {
	if h.state == st && errText(h.lastErr) == errText(err) {
		return nil
	}
	h.state, h.lastErr = st, err
	close(h.changed)
	h.changed = make(chan struct{})
	p := proto.Message{"state": string(st), "connection_generation": h.gen}
	if err != nil {
		p["reason"] = apierr.As(err).Message
	}
	return &Event{HostID: h.id, Kind: KindConnectionState, Payload: p}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// change runs fn with h.mu held and emits the state event fn returns, in
// the order of the changes.
func (h *Host) change(fn func() *Event) {
	h.emitMu.Lock()
	defer h.emitMu.Unlock()
	h.mu.Lock()
	ev := fn()
	h.mu.Unlock()
	if ev != nil {
		h.m.emit(*ev)
	}
}

func (h *Host) setState(st ConnState, err error) {
	h.change(func() *Event { return h.setStateLocked(st, err) })
}

// Connect starts connecting unless a connection loop runs. A blocked host
// gets one more attempt.
func (h *Host) Connect() {
	h.change(func() *Event {
		if h.loopDone != nil {
			return nil
		}
		ctx, cancel := context.WithCancel(h.m.ctx)
		done := make(chan struct{})
		h.cancel, h.loopDone = cancel, done
		go h.loop(ctx, done)
		return h.setStateLocked(StateConnecting, nil)
	})
}

// Disconnect stops the connection loop, closes the connection and leaves
// the host offline.
func (h *Host) Disconnect() {
	h.mu.Lock()
	cancel, done, rpc := h.cancel, h.loopDone, h.rpc
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if rpc != nil {
		rpc.shutdown(errors.New("disconnected"))
	}
	if done != nil {
		<-done
	}
	h.setState(StateOffline, nil)
}

// loop connects, serves the connection until it ends and reconnects with
// backoff until ctx ends or the host is blocked. Once a connection of the
// loop was ready, a dial that nothing answers (noAnswer) waits the first
// backoff wait again instead of a longer one.
func (h *Host) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	defer h.endLoop(done)
	b := newBackoff(h.m.opt.Backoff)
	wasReady := false
	for {
		rpc, hello, err := h.establish(ctx)
		if ctx.Err() != nil {
			if rpc != nil {
				rpc.shutdown(ctx.Err())
			}
			return
		}
		var be *blockError
		if errors.As(err, &be) {
			h.change(func() *Event {
				if h.loopDone == done {
					h.cancel()
					h.cancel, h.loopDone = nil, nil
				}
				h.blockKind = BlockRejected
				if be.mismatch {
					h.blockKind = BlockFingerprint
				}
				return h.setStateLocked(StateBlocked, be.apiError(h.target.Name))
			})
			return
		}
		if err != nil {
			h.m.opt.Logger.Printf("host %s: connect: %v", h.id, err)
			h.setState(StateReconnecting, unreachable(h.Target().Name, err))
			if wasReady && noAnswer(err) {
				b.Reset()
			}
			if h.m.opt.Sleep(ctx, b.Next()) != nil {
				return
			}
			continue
		}
		go rpc.run()
		if err := h.prepare(ctx, rpc); err != nil {
			rpc.shutdown(err)
			if ctx.Err() != nil {
				return
			}
			h.m.opt.Logger.Printf("host %s: prepare the connection: %v", h.id, err)
			h.setState(StateReconnecting, lost(h.Target().Name))
			if h.m.opt.Sleep(ctx, b.Next()) != nil {
				return
			}
			continue
		}
		b.Reset()
		wasReady = true
		h.becomeReady(rpc, hello)
		h.serve(ctx, rpc)
		if ctx.Err() != nil {
			return
		}
		h.m.opt.Logger.Printf("host %s: connection ended: %v", h.id, rpc.Err())
		h.setState(StateReconnecting, lost(h.Target().Name))
		if h.m.opt.Sleep(ctx, b.Next()) != nil {
			return
		}
	}
}

// endLoop forgets the loop that done belongs to.
func (h *Host) endLoop(done chan struct{}) {
	h.mu.Lock()
	if h.loopDone == done {
		h.cancel()
		h.cancel, h.loopDone = nil, nil
	}
	h.mu.Unlock()
}

// blockError is a connection the certificates refuse.
type blockError struct {
	mismatch bool
	err      error
}

func (e *blockError) Error() string { return e.err.Error() }
func (e *blockError) Unwrap() error { return e.err }

func (e *blockError) apiError(name string) *apierr.Error {
	if e.mismatch {
		return apierr.New(wire.CodeAuthRequired,
			fmt.Sprintf("%s의 인증서가 페어링 때와 다릅니다. 재설치했다면 deskhand host repair %s을 실행하세요", name, name), false, apierr.NextHostRepair)
	}
	return apierr.New(wire.CodeAuthRequired,
		fmt.Sprintf("%s PC가 이 Mac을 더 이상 신뢰하지 않습니다. deskhand host repair %s을 실행하세요", name, name), false, apierr.NextHostRepair)
}

func classify(err error, sess Session) error {
	switch {
	case errors.Is(err, transport.ErrPeerMismatch):
		return &blockError{mismatch: true, err: err}
	case rejected(err, sess):
		return &blockError{err: err}
	}
	return err
}

func unreachable(name string, err error) *apierr.Error {
	return apierr.New(wire.CodeOffline,
		fmt.Sprintf("'%s'에 연결하지 못했습니다(%v). PC가 켜져 있고 같은 네트워크에 있는지 확인한 뒤 다음 명령을 실행하세요: deskhand host doctor %q", name, err, name),
		true, apierr.NextHostDoctor)
}

func lost(name string) *apierr.Error {
	return apierr.New(wire.CodeOffline, fmt.Sprintf("%s PC와의 연결이 끊겨 다시 연결하는 중입니다. 잠시 뒤 다시 시도하세요", name), true, apierr.NextRetry)
}

func offline(name string) *apierr.Error {
	return apierr.New(wire.CodeOffline, fmt.Sprintf("%s PC 연결이 끊겨 있습니다. deskhand host connect %s로 다시 연결하세요", name, name), true, apierr.NextHostConnect)
}

// establish dials the target, opens the rpc stream and completes the
// hello/welcome handshake.
func (h *Host) establish(ctx context.Context) (*rpcClient, Hello, error) {
	t := h.Target()
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	sess, err := h.m.opt.Dial(dctx, t, h.m.opt.Identity)
	if err != nil {
		return nil, Hello{}, classify(err, nil)
	}
	st, err := sess.OpenStream(dctx)
	if err != nil {
		cerr := classify(err, sess)
		_ = sess.Close("no rpc stream")
		return nil, Hello{}, cerr
	}
	msg, r, err := handshake(st, h.m.opt.Controller)
	if err != nil {
		cerr := classify(err, sess)
		_ = st.Close()
		_ = sess.Close("handshake failed")
		return nil, Hello{}, cerr
	}
	rpc := newRPCClient(st, r, h.onAgentEvent, func() { _ = sess.Close("rpc stream closed") })
	rpc.sess = sess
	rpc.holdEvents()
	return rpc, parseHello(msg), nil
}

// prepare runs Options.Prepare on rpc, whose events are held.
func (h *Host) prepare(ctx context.Context, rpc *rpcClient) error {
	if h.m.opt.Prepare == nil {
		return nil
	}
	pctx, cancel := context.WithTimeout(ctx, prepareTimeout)
	defer cancel()
	return h.m.opt.Prepare(pctx, h.id, func(ctx context.Context, op string, params proto.Message) (proto.Message, error) {
		res, err := rpc.call(ctx, buildRequest(op, params, ""), false)
		if err != nil {
			return nil, err
		}
		return answer(res)
	})
}

// becomeReady makes rpc, which is being read with its events held, the
// host's connection with a new generation and then lets its events go: the
// agent's events of the connection come after its ready event and apply to
// its hello.
func (h *Host) becomeReady(rpc *rpcClient, hello Hello) {
	var gen uint64
	h.change(func() *Event {
		h.gen++
		gen = h.gen
		h.rpc, h.hello, h.session, h.lastSeen = rpc, hello, nil, time.Now()
		return h.setStateLocked(StateReady, nil)
	})
	rpc.openEvents()
	if h.m.opt.OnReady != nil {
		h.m.opt.OnReady(h.id, gen)
	}
	go h.fetchSession(rpc)
}

// fetchSession records the session.info answer of rpc's agent.
func (h *Host) fetchSession(rpc *rpcClient) {
	ctx, cancel := context.WithTimeout(h.m.ctx, 5*time.Second)
	defer cancel()
	res, err := rpc.call(ctx, proto.Message{"t": "req", "op": "session.info"}, false)
	if err != nil || res["ok"] != true {
		return
	}
	h.mu.Lock()
	if h.rpc == rpc {
		h.session = fields(res)
	}
	h.mu.Unlock()
}

// serve waits until rpc ends or ctx ends and pings the agent meanwhile.
func (h *Host) serve(ctx context.Context, rpc *rpcClient) {
	tick := time.NewTicker(h.m.opt.PingInterval)
	defer tick.Stop()
	defer func() {
		h.mu.Lock()
		if h.rpc == rpc {
			h.rpc = nil
		}
		h.mu.Unlock()
	}()
	for {
		select {
		case <-rpc.Done():
			return
		case <-ctx.Done():
			rpc.shutdown(ctx.Err())
			return
		case <-tick.C:
			h.mu.Lock()
			busy := h.pinging
			h.pinging = true
			h.mu.Unlock()
			if !busy {
				go h.ping(rpc)
			}
		}
	}
}

// ping marks the host degraded when the agent does not answer a ping within
// PingTimeout, and ready again when it does.
func (h *Host) ping(rpc *rpcClient) {
	defer func() {
		h.mu.Lock()
		h.pinging = false
		h.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(h.m.ctx, h.m.opt.PingTimeout)
	defer cancel()
	_, err := rpc.call(ctx, proto.Message{"t": "req", "op": "ping"}, false)
	name := h.Target().Name
	h.change(func() *Event {
		if h.rpc != rpc {
			return nil
		}
		switch {
		case err == nil:
			h.lastSeen = time.Now()
			if h.state == StateDegraded {
				return h.setStateLocked(StateReady, nil)
			}
		case errors.Is(err, context.DeadlineExceeded) && h.state == StateReady:
			return h.setStateLocked(StateDegraded, apierr.New(wire.CodeTimeout,
				fmt.Sprintf("%s PC의 에이전트가 %s 동안 응답하지 않습니다. 잠시 기다리거나 deskhand host doctor %s을 실행하세요", name, h.m.opt.PingTimeout, name),
				true, apierr.NextHostDoctor))
		}
		return nil
	})
}

// onAgentEvent keeps the capabilities and the session current and reports
// the event.
func (h *Host) onAgentEvent(m proto.Message) {
	name := m.String("name")
	payload := proto.Message{}
	for k, v := range m {
		if k != "t" {
			payload[k] = v
		}
	}
	h.mu.Lock()
	switch name {
	case "caps.changed":
		h.hello.Caps = parseCaps(m["caps"])
	case "session.changed":
		s := proto.Message{}
		for k, v := range h.session {
			s[k] = v
		}
		for k, v := range payload {
			if k != "name" {
				s[k] = v
			}
		}
		h.session = s
	}
	h.mu.Unlock()
	h.m.emit(Event{HostID: h.id, Kind: KindAgentEvent, Payload: payload})
}

// waitReady returns the connection once the host is ready or degraded,
// waiting at most ReadyWait while it connects. A connection that has ended
// is not returned; the wait goes on until the state changes. The end of
// ctx or of ReadyWait is an *apierr.Error that names the next step.
func (h *Host) waitReady(ctx context.Context) (*rpcClient, uint64, error) {
	wctx, cancel := context.WithTimeout(ctx, h.m.opt.ReadyWait)
	defer cancel()
	for {
		h.mu.Lock()
		st, rpc, gen, ch, lastErr, name := h.state, h.rpc, h.gen, h.changed, h.lastErr, h.target.Name
		h.mu.Unlock()
		switch st {
		case StateReady, StateDegraded:
			if rpc != nil && !rpc.closed() {
				return rpc, gen, nil
			}
		case StateBlocked:
			return nil, 0, lastErr
		case StateOffline:
			return nil, 0, offline(name)
		}
		select {
		case <-ch:
		case <-wctx.Done():
			if lastErr != nil {
				return nil, 0, lastErr
			}
			if st == StateConnecting {
				return nil, 0, apierr.New(wire.CodeOffline, fmt.Sprintf("%s PC에 아직 연결하는 중입니다. 잠시 뒤 다시 시도하세요", name), true, apierr.NextRetry)
			}
			return nil, 0, lost(name)
		}
	}
}

// CallOpts tune a Call. RID is the request id to send; empty means a new
// one for a journaled op and none for other ops. Info, when set, receives
// what happened to the request. Ready, when set, runs once the connection is
// ready, right before the request is written; its error refuses the request
// unsent.
type CallOpts struct {
	RID   string
	Info  *CallInfo
	Ready func() error
}

// CallInfo describes a request: its rid, the generation of the connection
// it went out on, whether it was written to a connection, whether that
// connection ended before the answer, whether req.status then settled the
// outcome and whether an answer (direct or settled) arrived.
type CallInfo struct {
	RID         string
	Generation  uint64
	Sent        bool
	Interrupted bool
	Resolved    bool
	Answered    bool
}

// Call sends op with params to the agent and returns the fields of its
// answer without t, id and ok; an answer with ok false is an *apierr.Error.
// A request without a rid whose connection ends is sent again once on the
// next connection. A request with a rid is never sent twice: when its
// connection ends before the answer, the request is recorded unknown and
// settled with req.status after the reconnect; an outcome that stays
// unknown is the error EXECUTION_OUTCOME_UNKNOWN.
func (h *Host) Call(ctx context.Context, op string, params proto.Message, opts CallOpts) (proto.Message, error) {
	rid := opts.RID
	if rid == "" && Journaled(op) {
		rid = NewRID()
	}
	req := buildRequest(op, params, rid)
	info := opts.Info
	if info == nil {
		info = &CallInfo{}
	}
	info.RID = rid
	if _, err := encodeRequest(req, math.MaxInt64); err != nil {
		return nil, err
	}
	log := h.m.opt.Requests
	recorded := false
	retried := false
	for {
		rpc, gen, err := h.waitReady(ctx)
		if err != nil {
			if recorded && log != nil {
				_ = log.SetRequestState(rid, RequestNotSent, nil)
			}
			return nil, err
		}
		info.Generation = gen
		if opts.Ready != nil {
			if err := opts.Ready(); err != nil {
				if recorded && log != nil {
					_ = log.SetRequestState(rid, RequestNotSent, nil)
				}
				return nil, err
			}
		}
		if rid != "" && !recorded && log != nil {
			if err := log.RequestSent(rid, h.id, op); err != nil {
				return nil, apierr.Internal(fmt.Sprintf("요청 기록을 저장하지 못했습니다(%v). 데몬 로그를 확인하세요", err))
			}
		}
		recorded = recorded || rid != ""
		sentBefore := info.Sent
		info.Sent = true
		res, err := rpc.call(ctx, req, h.m.takeDrop(op))
		switch {
		case err == nil:
			h.touch()
			info.Answered = true
			if rid != "" && log != nil {
				_ = log.SetRequestState(rid, RequestDone, encode(res))
			}
			return answer(res)
		case errors.Is(err, errNotSent):
			info.Sent = sentBefore
			continue
		case IsRequestError(err):
			info.Sent = sentBefore
			if recorded && log != nil {
				_ = log.SetRequestState(rid, RequestNotSent, nil)
			}
			return nil, err
		case errors.Is(err, errConnLost):
			if rid == "" {
				if retried {
					return nil, lost(h.Target().Name)
				}
				retried = true
				continue
			}
			info.Interrupted = true
			if log != nil {
				_ = log.SetRequestState(rid, RequestUnknown, nil)
			}
			return h.resolve(ctx, rid, info)
		case ctx.Err() != nil:
			if rid != "" {
				if log != nil {
					_ = log.SetRequestState(rid, RequestUnknown, nil)
				}
				return nil, apierr.New(wire.CodeTimeout,
					fmt.Sprintf("요청 %s의 응답을 기다리는 동안 시간이 다 됐습니다. jobs.get이나 PC 상태로 결과를 확인하세요", rid), false, apierr.NextCheckState)
			}
			return nil, apierr.New(wire.CodeTimeout, fmt.Sprintf("%s의 응답을 기다리는 동안 시간이 다 됐습니다. 다시 시도하세요", op), true, apierr.NextRetry)
		default:
			return nil, err
		}
	}
}

// Resolve settles the outcome of the request rid, which went out on an
// earlier connection or an earlier run of the daemon and whose answer is not
// known, with req.status as Call does when a connection ends before the
// answer: it returns the fields of the answer or its error, and the error
// EXECUTION_OUTCOME_UNKNOWN when the agent has no record of rid or the
// outcome stays unknown for ResolveWait.
func (h *Host) Resolve(ctx context.Context, rid string) (proto.Message, error) {
	return h.resolve(ctx, rid, &CallInfo{RID: rid})
}

// OpenStream opens a data stream with the header hdr on the host's
// connection once it is ready or degraded, waiting for it as Call does, and
// returns the stream and the generation of its connection. A connection
// that ends before the header is written is an OFFLINE error.
func (h *Host) OpenStream(ctx context.Context, hdr wire.StreamHeader) (DataStream, uint64, error) {
	rpc, gen, err := h.waitReady(ctx)
	if err != nil {
		return nil, 0, err
	}
	if rpc.sess == nil {
		return nil, gen, apierr.Internal("이 연결은 데이터 스트림을 열 수 없습니다. 데몬 로그를 확인하세요")
	}
	st, err := rpc.sess.OpenStream(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, gen, ctx.Err()
		}
		return nil, gen, lost(h.Target().Name)
	}
	ds, ok := st.(DataStream)
	if !ok {
		_ = st.Close()
		return nil, gen, apierr.Internal("이 연결은 데이터 스트림을 열 수 없습니다. 데몬 로그를 확인하세요")
	}
	_ = ds.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err := wire.WriteHeader(ds, hdr); err != nil {
		ds.CancelWrite()
		ds.CancelRead()
		return nil, gen, lost(h.Target().Name)
	}
	_ = ds.SetWriteDeadline(time.Time{})
	return ds, gen, nil
}

// buildRequest returns the req message of op with params and rid; params
// cannot set t, id, op or rid.
func buildRequest(op string, params proto.Message, rid string) proto.Message {
	req := proto.Message{}
	for k, v := range params {
		req[k] = v
	}
	req["t"], req["op"] = "req", op
	delete(req, "id")
	delete(req, "rid")
	if rid != "" {
		req["rid"] = rid
	}
	return req
}

// CheckRequest returns the *requestError of the request that Call would
// send for op, params and rid when it cannot be encoded or is too long, and
// nil otherwise.
func CheckRequest(op string, params proto.Message, rid string) error {
	_, err := encodeRequest(buildRequest(op, params, rid), math.MaxInt64)
	return err
}

func (h *Host) touch() {
	h.mu.Lock()
	h.lastSeen = time.Now()
	h.mu.Unlock()
}

// resolve settles the outcome of rid with req.status on the next
// connections, waiting through connection failures until ResolveWait ends.
func (h *Host) resolve(ctx context.Context, rid string, info *CallInfo) (proto.Message, error) {
	rctx, cancel := context.WithTimeout(ctx, h.m.opt.ResolveWait)
	defer cancel()
	log := h.m.opt.Requests
	for rctx.Err() == nil {
		h.mu.Lock()
		changed := h.changed
		h.mu.Unlock()
		rpc, _, err := h.waitReady(rctx)
		if err != nil {
			select {
			case <-changed:
			case <-rctx.Done():
			}
			continue
		}
		res, err := rpc.call(rctx, proto.Message{"t": "req", "op": "req.status", "rid": rid}, false)
		if errors.Is(err, errNotSent) || errors.Is(err, errConnLost) {
			continue
		}
		if err != nil || res["ok"] == false && res.String("state") == "" {
			break
		}
		switch res.String("state") {
		case "done":
			delete(res, "state")
			info.Resolved, info.Answered = true, true
			if log != nil {
				_ = log.SetRequestState(rid, RequestDone, encode(res))
			}
			return answer(res)
		case "running":
			if sleepCtx(rctx, resolvePoll) != nil {
				return nil, outcomeUnknown(rid)
			}
			continue
		}
		break
	}
	return nil, outcomeUnknown(rid)
}

// resolvePoll is the time between two req.status calls while the request
// still runs.
const resolvePoll = 250 * time.Millisecond

func outcomeUnknown(rid string) *apierr.Error {
	return apierr.New(wire.CodeExecutionOutcomeUnknown,
		fmt.Sprintf("요청 %s의 결과를 확인하지 못했습니다. PC 상태를 먼저 확인하고 필요하면 새 요청으로 다시 실행하세요", rid), false, apierr.NextCheckState)
}

func encode(m proto.Message) json.RawMessage {
	b, err := json.Marshal(fields(m))
	if err != nil {
		return nil
	}
	return b
}

// fields returns m without t, id and ok.
func fields(m proto.Message) proto.Message {
	out := make(proto.Message, len(m))
	for k, v := range m {
		switch k {
		case "t", "id", "ok":
		default:
			out[k] = v
		}
	}
	return out
}

// answer returns the fields of res, or its error when ok is false.
func answer(res proto.Message) (proto.Message, error) {
	if res["ok"] == false {
		return nil, agentError(res)
	}
	return fields(res), nil
}

// agentError turns an answer with ok false into an *apierr.Error; an answer
// without a code gets one from its text. The next action of
// CAPABILITY_UNSUPPORTED is host_repair only for a code taken from the text,
// which names an op the agent does not know.
func agentError(res proto.Message) *apierr.Error {
	code, _ := res["code"].(string)
	msg := res.String("error")
	if msg == "" {
		msg = "에이전트가 이유를 밝히지 않고 요청을 거절했습니다. PC의 에이전트 로그를 확인하세요"
	}
	retry, _ := res["retryable"].(bool)
	inferred := code == ""
	if inferred {
		code = inferCode(msg)
	}
	next := ""
	switch {
	case code == wire.CodeExecutionOutcomeUnknown, code == wire.CodeInProgress:
		next = apierr.NextCheckState
	case code == wire.CodeCapabilityUnsupported && inferred:
		next = apierr.NextHostRepair
	}
	return apierr.New(code, msg, retry, next)
}

// inferCode picks the code of an agent error that has none.
func inferCode(msg string) string {
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "unknown op"):
		return wire.CodeCapabilityUnsupported
	case strings.Contains(low, "does not exist"), strings.Contains(low, "not found"), strings.Contains(low, "no such"):
		return wire.CodeNotFound
	case strings.Contains(low, "denied"), strings.Contains(low, "permission"):
		return wire.CodePermissionDenied
	}
	return wire.CodeInvalidArgument
}
