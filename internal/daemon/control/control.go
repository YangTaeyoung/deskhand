// Package control arbitrates who may drive each host's input: nobody, the
// person at this Mac or one AI client (docs/design.md §5.1). It keeps each
// host's control state and writer generation in the store, announces every
// change (control.changed, a timeline entry, control.set_writer to the
// connected agent) and carries the AI input stream to the agent.
package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Owner is who may drive a host's input.
type Owner string

// Owners.
const (
	OwnerNone  Owner = model.OwnerNone
	OwnerHuman Owner = model.OwnerHuman
	OwnerAI    Owner = model.OwnerAI
)

// ClientInfo is the local API client that asks: its actor, client id and
// hello name.
type ClientInfo = model.Caller

const (
	// DefaultLeaseTTL is how long an AI owner keeps control without input or
	// UI operations.
	DefaultLeaseTTL = 60 * time.Second
	// defaultPushTimeout bounds a control.set_writer call.
	defaultPushTimeout = 3 * time.Second
)

// Agent is the connection to a host's agent.
type Agent interface {
	Call(ctx context.Context, op string, params proto.Message, opts conn.CallOpts) (proto.Message, error)
	OpenStream(ctx context.Context, hdr wire.StreamHeader) (conn.DataStream, uint64, error)
	State() conn.ConnState
	Generation() uint64
}

// Agents finds the agent connection of a host.
type Agents interface {
	// Agent returns the connection of hostID, or nil when there is none.
	Agent(hostID string) Agent
}

// AgentsFrom returns the Agents of the daemon's connection manager.
func AgentsFrom(m *conn.Manager) Agents { return connAgents{m: m} }

type connAgents struct{ m *conn.Manager }

func (c connAgents) Agent(hostID string) Agent {
	if h := c.m.Get(hostID); h != nil {
		return h
	}
	return nil
}

// Options configure a Manager.
type Options struct {
	Store  *store.Store
	Bus    *events.Bus
	Agents Agents
	// LeaseTTL is how long an AI owner keeps control without input or UI
	// operations (DefaultLeaseTTL when 0).
	LeaseTTL time.Duration
	// PushTimeout bounds a control.set_writer call (3 s when 0).
	PushTimeout time.Duration
	Logger      *log.Logger
}

// state is the control state of a host. clientID and clientName name the
// owner's client; leaseUntil is the end of an AI owner's lease.
type state struct {
	owner      Owner
	clientID   string
	clientName string
	gen        uint64
	aiPaused   bool
	leaseUntil time.Time
}

// sameWriter reports whether a and b have the same owner, the same AI
// client and the same pause: a change of any of them is a new writer
// generation.
func (a state) sameWriter(b state) bool {
	return a.owner == b.owner && a.aiPaused == b.aiPaused && (a.owner != OwnerAI || a.clientID == b.clientID)
}

// leased reports whether s is an AI owner whose lease runs at now.
func (s state) leased(now time.Time) bool { return s.owner == OwnerAI && now.Before(s.leaseUntil) }

func (s state) model() model.ControlState {
	return model.ControlState{Owner: string(s.owner), OwnerClient: s.clientName, OwnerClientID: s.clientID, Gen: s.gen, AIPaused: s.aiPaused}
}

// fields are the ControlState fields of a control.changed event.
func (s state) fields() map[string]any {
	out := map[string]any{"owner": string(s.owner), "gen": s.gen, "ai_paused": s.aiPaused}
	if s.clientName != "" {
		out["owner_client"] = s.clientName
	}
	if s.clientID != "" {
		out["owner_client_id"] = s.clientID
	}
	return out
}

// owned returns s owned by owner for client c.
func (s state) owned(owner Owner, c ClientInfo) state {
	s.owner, s.clientID, s.clientName = owner, c.ClientID, c.ClientName
	return s
}

// host is what the manager keeps of one host.
// acked is the highest writer generation the agent has taken on its
// current connection; pushing is set while the pusher runs, and ackCh is
// closed whenever acked or pushing changes.
type host struct {
	lastHumanInput    time.Time
	humanPhysicalHeld map[string]bool
	humanWireHeld     map[string]bool
	st                state
	timer             *time.Timer
	link              *aiLink
	acked             uint64
	pushing           bool
	ackCh             chan struct{}
}

// Manager owns the control state of every host.
type Manager struct {
	opt         Options
	lg          *log.Logger
	ttl         time.Duration
	pushTimeout time.Duration

	base context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup

	// emitMu orders the announcements as the changes.
	emitMu sync.Mutex

	mu     sync.Mutex
	hosts  map[string]*host
	closed bool
}

// New returns a Manager; Load reads the stored states.
func New(opt Options) *Manager {
	lg := opt.Logger
	if lg == nil {
		lg = log.New(io.Discard, "", 0)
	}
	ttl := opt.LeaseTTL
	if ttl <= 0 {
		ttl = DefaultLeaseTTL
	}
	pt := opt.PushTimeout
	if pt <= 0 {
		pt = defaultPushTimeout
	}
	m := &Manager{opt: opt, lg: lg, ttl: ttl, pushTimeout: pt, hosts: map[string]*host{}}
	m.base, m.stop = context.WithCancel(context.Background())
	return m
}

// Load reads the stored states and releases the AI control a stored state
// holds: owner none, gen+1.
func (m *Manager) Load() error {
	rows, err := m.opt.Store.ListControls()
	if err != nil {
		return err
	}
	var leased []string
	m.mu.Lock()
	for _, r := range rows {
		m.hosts[r.HostID] = &host{st: state{owner: Owner(r.Owner), clientID: r.OwnerClient, clientName: r.OwnerName, gen: r.Gen,
			aiPaused: r.AIPaused, leaseUntil: r.LeaseUntil}}
		if Owner(r.Owner) == OwnerAI {
			leased = append(leased, r.HostID)
		}
	}
	m.mu.Unlock()
	for _, id := range leased {
		_, _ = m.change(context.Background(), id, model.System, "데몬이 다시 시작해 AI 제어권을 놓았습니다", false, func(cur state, _ time.Time) (state, error) {
			if cur.owner != OwnerAI {
				return cur, nil
			}
			return cur.owned(OwnerNone, model.System), nil
		})
	}
	return nil
}

// Close stops the lease timers and the AI input streams and waits for the
// pushes in flight.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	var links []*aiLink
	for _, h := range m.hosts {
		if h.timer != nil {
			h.timer.Stop()
			h.timer = nil
		}
		if h.link != nil {
			links = append(links, h.link)
		}
	}
	m.mu.Unlock()
	m.stop()
	for _, lk := range links {
		lk.abort()
	}
	m.wg.Wait()
}

// hostLocked returns the host of hostID, adding it; m.mu is held.
func (m *Manager) hostLocked(hostID string) *host {
	h := m.hosts[hostID]
	if h == nil {
		h = &host{st: state{owner: OwnerNone}}
		m.hosts[hostID] = h
	}
	return h
}

// current returns the state of hostID.
func (m *Manager) current(hostID string) state {
	m.mu.Lock()
	defer m.mu.Unlock()
	if h := m.hosts[hostID]; h != nil {
		return h.st
	}
	return state{owner: OwnerNone}
}

// Get returns the control state of hostID.
func (m *Manager) Get(hostID string) model.ControlState { return m.current(hostID).model() }

// HumanGen returns the writer generation of hostID: the gen of the human
// input and of the operations of people.
func (m *Manager) HumanGen(hostID string) uint64 { return m.current(hostID).gen }

// changeFn returns the state after a change from the state before it at
// now, or the error that refuses the change.
type changeFn func(cur state, now time.Time) (state, error)

// errClosed refuses changes after Close.
var errClosed = apierr.New(wire.CodeInternal, "Deskhand 데몬이 끝나는 중입니다. 데몬을 다시 시작한 뒤 다시 시도하세요", true, apierr.NextRetry)

// apply runs fn on the state of hostID. A change of the writer (owner, AI
// client or pause) raises gen by one; a changed state is stored, and a
// change of the writer is announced with control.changed and a timeline
// entry of c with summary. It returns the state after fn and whether the
// writer changed.
func (m *Manager) apply(hostID string, c ClientInfo, summary string, fn changeFn) (state, bool, error) {
	m.emitMu.Lock()
	defer m.emitMu.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return state{owner: OwnerNone}, false, errClosed
	}
	h := m.hostLocked(hostID)
	now := time.Now()
	next, err := fn(h.st, now)
	if err != nil {
		cur := h.st
		m.mu.Unlock()
		return cur, false, err
	}
	changed := !next.sameWriter(h.st)
	next.gen = h.st.gen
	if changed {
		next.gen++
	}
	if next.owner != OwnerAI {
		next.leaseUntil = time.Time{}
	}
	if next != h.st {
		if err := m.opt.Store.PutControl(row(hostID, next, now)); err != nil {
			cur := h.st
			m.mu.Unlock()
			m.lg.Printf("control: store the state of %s: %v", hostID, err)
			return cur, false, apierr.Internal(fmt.Sprintf("제어권 상태를 저장하지 못했습니다(%v). 데몬 로그를 확인하세요", err))
		}
		h.st = next
		if changed {
			// Old writer keys are released on a generation change. Local
			// physical holds remain tracked until their viewer reports release.
			clear(h.humanWireHeld)
		}
	}
	m.scheduleLocked(hostID, h)
	m.mu.Unlock()
	if changed {
		m.opt.Bus.Publish(hostID, "", events.ControlChanged, next.fields())
		if summary != "" {
			_, _ = m.opt.Bus.Record(c.Entry(hostID, model.TimelineControl, summary, model.TimelineOK, ""))
		}
	}
	return next, changed, nil
}

func row(hostID string, s state, now time.Time) store.Control {
	return store.Control{HostID: hostID, Owner: string(s.owner), OwnerClient: s.clientID, OwnerName: s.clientName, Gen: s.gen,
		AIPaused: s.aiPaused, LeaseUntil: s.leaseUntil, UpdatedAt: now}
}

// change applies fn and, when the writer changed, starts sending the new
// writer generation to the agent and cuts the AI input stream of an older
// generation; wait makes it wait until the agent holds the new generation
// (waitAck).
func (m *Manager) change(ctx context.Context, hostID string, c ClientInfo, summary string, wait bool, fn changeFn) (model.ControlState, error) {
	next, changed, err := m.apply(hostID, c, summary, fn)
	if err != nil {
		return next.model(), err
	}
	if changed {
		m.goPush(hostID)
		m.cut(hostID, next.gen)
		if wait {
			m.waitAck(ctx, hostID, next.gen)
		}
	}
	return next.model(), nil
}

// scheduleLocked runs the lease timer of an AI owner and stops it for any
// other owner; m.mu is held.
func (m *Manager) scheduleLocked(hostID string, h *host) {
	if h.st.owner != OwnerAI {
		if h.timer != nil {
			h.timer.Stop()
			h.timer = nil
		}
		return
	}
	if h.timer == nil {
		h.timer = time.AfterFunc(time.Until(h.st.leaseUntil), func() { m.expire(hostID) })
	}
}

// expire ends the lease of hostID's AI owner once it has run out, and runs
// the timer again for a lease renewed since it was set.
func (m *Manager) expire(hostID string) {
	m.mu.Lock()
	h := m.hosts[hostID]
	if m.closed || h == nil {
		m.mu.Unlock()
		return
	}
	h.timer = nil
	if left := time.Until(h.st.leaseUntil); h.st.owner == OwnerAI && left > 0 {
		h.timer = time.AfterFunc(left, func() { m.expire(hostID) })
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	summary := fmt.Sprintf("AI 제어권이 만료됐습니다(%s 동안 입력 없음)", m.ttl)
	_, _ = m.change(m.base, hostID, model.System, summary, false, func(cur state, now time.Time) (state, error) {
		if cur.owner != OwnerAI || cur.leased(now) {
			return cur, nil
		}
		return cur.owned(OwnerNone, model.System), nil
	})
}

// goPush runs the pusher of hostID unless it runs: it sends the current
// writer generation to the agent until the agent has taken it, while the
// agent is connected.
func (m *Manager) goPush(hostID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	h := m.hostLocked(hostID)
	if h.pushing {
		return
	}
	h.pushing = true
	m.wg.Add(1)
	go m.pushLoop(hostID)
}

// pushLoop is the pusher of hostID. A failed control.set_writer is sent
// again after a wait that doubles from pushRetryMin up to pushRetryMax.
func (m *Manager) pushLoop(hostID string) {
	defer m.wg.Done()
	wait := pushRetryMin
	for {
		m.mu.Lock()
		h := m.hostLocked(hostID)
		gen := h.st.gen
		if m.closed || gen <= h.acked {
			h.pushing = false
			h.broadcastLocked()
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
		a := m.agent(hostID)
		if a == nil || !connected(a.State()) {
			m.stopPushing(hostID)
			return
		}
		err := m.setWriter(a, hostID, gen)
		if err == nil {
			wait = pushRetryMin
			continue
		}
		if m.base.Err() != nil {
			m.stopPushing(hostID)
			return
		}
		m.lg.Printf("control: send writer generation %d to %s: %v; trying again in %s", gen, hostID, err, wait)
		select {
		case <-m.base.Done():
			m.stopPushing(hostID)
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, pushRetryMax)
	}
}

const (
	// pushRetryMin and pushRetryMax bound the wait before a failed
	// control.set_writer is sent again.
	pushRetryMin = 100 * time.Millisecond
	pushRetryMax = 5 * time.Second
)

func connected(st conn.ConnState) bool { return st == conn.StateReady || st == conn.StateDegraded }

// stopPushing marks the pusher of hostID ended.
func (m *Manager) stopPushing(hostID string) {
	m.mu.Lock()
	h := m.hostLocked(hostID)
	h.pushing = false
	h.broadcastLocked()
	m.mu.Unlock()
}

// setWriter sends control.set_writer{gen} to a once. A higher generation
// the agent answers becomes the host's; the generation the agent then holds
// is recorded as taken. An agent without writer generations takes any.
func (m *Manager) setWriter(a Agent, hostID string, gen uint64) error {
	ctx, cancel := context.WithTimeout(m.base, m.pushTimeout)
	defer cancel()
	res, err := a.Call(ctx, "control.set_writer", proto.Message{"gen": gen}, conn.CallOpts{})
	return m.took(hostID, gen, res, err)
}

// took records the answer res, err of control.set_writer{gen} on hostID.
func (m *Manager) took(hostID string, gen uint64, res proto.Message, err error) error {
	if err != nil {
		if apierr.CodeOf(err) == wire.CodeCapabilityUnsupported {
			m.ack(hostID, gen)
			return nil
		}
		return err
	}
	got, ok := genOf(res["gen"])
	if !ok || got < gen {
		got = gen
	}
	if got > gen {
		m.adopt(hostID, got)
	}
	m.ack(hostID, got)
	return nil
}

// ack records that the agent of hostID holds writer generation gen.
func (m *Manager) ack(hostID string, gen uint64) {
	m.mu.Lock()
	h := m.hostLocked(hostID)
	if gen > h.acked {
		h.acked = gen
	}
	h.broadcastLocked()
	m.mu.Unlock()
}

// broadcastLocked wakes the waiters of the host's pushes; m.mu is held.
func (h *host) broadcastLocked() {
	if h.ackCh != nil {
		close(h.ackCh)
	}
	h.ackCh = make(chan struct{})
}

// waitAck waits until the agent of hostID holds gen, the pusher ends, ctx
// ends or PushTimeout passes.
func (m *Manager) waitAck(ctx context.Context, hostID string, gen uint64) {
	timer := time.NewTimer(m.pushTimeout)
	defer timer.Stop()
	for {
		m.mu.Lock()
		h := m.hosts[hostID]
		if m.closed || h == nil || h.acked >= gen || !h.pushing {
			m.mu.Unlock()
			return
		}
		if h.ackCh == nil {
			h.ackCh = make(chan struct{})
		}
		ch := h.ackCh
		m.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return
		case <-timer.C:
			return
		}
	}
}

// PrepareConn sends the writer generation of hostID on a new connection of
// its agent before the connection is ready (conn.Options.Prepare), again
// until the agent holds the current one. A higher generation the agent
// answers becomes the host's; an agent without writer generations passes.
// An error is the failed call.
func (m *Manager) PrepareConn(ctx context.Context, hostID string, call conn.PrepareCall) error {
	m.mu.Lock()
	m.hostLocked(hostID).acked = 0
	m.mu.Unlock()
	for {
		gen := m.HumanGen(hostID)
		res, err := call(ctx, "control.set_writer", proto.Message{"gen": gen})
		if err := m.took(hostID, gen, res, err); err != nil {
			return err
		}
		m.mu.Lock()
		h := m.hostLocked(hostID)
		done := h.st.gen <= h.acked
		m.mu.Unlock()
		if done {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// Resync sends the current writer generation of hostID to its connected
// agent once, taking a higher one the agent answers, and reports whether
// the host's generation is now above tried, the generation an operation
// the agent refused carried.
func (m *Manager) Resync(hostID string, tried uint64) bool {
	a := m.agent(hostID)
	if a == nil || !connected(a.State()) {
		return false
	}
	gen := m.HumanGen(hostID)
	if err := m.setWriter(a, hostID, gen); err != nil {
		m.lg.Printf("control: send writer generation %d to %s: %v", gen, hostID, err)
		return false
	}
	return m.HumanGen(hostID) > tried
}

// adopt raises the writer generation of hostID to the agent's higher gen.
func (m *Manager) adopt(hostID string, gen uint64) {
	m.emitMu.Lock()
	m.mu.Lock()
	h := m.hostLocked(hostID)
	if m.closed || gen <= h.st.gen {
		m.mu.Unlock()
		m.emitMu.Unlock()
		return
	}
	next := h.st
	next.gen = gen
	if err := m.opt.Store.PutControl(row(hostID, next, time.Now())); err != nil {
		m.mu.Unlock()
		m.emitMu.Unlock()
		m.lg.Printf("control: store the writer generation %d of %s: %v", gen, hostID, err)
		return
	}
	h.st = next
	clear(h.humanWireHeld)
	m.mu.Unlock()
	m.lg.Printf("control: %s's agent is at writer generation %d; the host takes it", hostID, gen)
	m.opt.Bus.Publish(hostID, "", events.ControlChanged, next.fields())
	m.emitMu.Unlock()
	m.cut(hostID, gen)
}

// genOf reads a writer generation from an agent answer or a parameter.
func genOf(v any) (uint64, bool) {
	switch n := v.(type) {
	case float64:
		if n >= 0 {
			return uint64(n), true
		}
	case uint64:
		return n, true
	case int:
		if n >= 0 {
			return uint64(n), true
		}
	case int64:
		if n >= 0 {
			return uint64(n), true
		}
	}
	return 0, false
}

func (m *Manager) agent(hostID string) Agent {
	if m.opt.Agents == nil {
		return nil
	}
	return m.opt.Agents.Agent(hostID)
}

// HostReady sends the writer generation of hostID to its agent, which has
// just connected, when the agent does not hold it yet, and waits for that.
func (m *Manager) HostReady(hostID string) {
	m.goPush(hostID)
	m.waitAck(m.base, hostID, m.HumanGen(hostID))
}

// ClientClosed releases the AI control the client clientID holds: its
// connection has ended.
func (m *Manager) ClientClosed(clientID string) {
	if clientID == "" {
		return
	}
	m.mu.Lock()
	var owned []string
	for id, h := range m.hosts {
		if h.st.owner == OwnerAI && h.st.clientID == clientID {
			owned = append(owned, id)
		}
	}
	m.mu.Unlock()
	for _, id := range owned {
		_, _ = m.change(m.base, id, model.System, "AI 클라이언트의 연결이 끊겨 제어권을 놓았습니다", false, func(cur state, _ time.Time) (state, error) {
			if cur.owner != OwnerAI || cur.clientID != clientID {
				return cur, nil
			}
			return cur.owned(OwnerNone, model.System), nil
		})
	}
}

// Acquire gives control of hostID to client c as owner as. A person takes it
// from an AI owner only with force; an AI client gets it when nobody holds
// it or it holds it already, and starts or renews its lease.
func (m *Manager) Acquire(ctx context.Context, hostID string, as Owner, c ClientInfo, force bool) (model.ControlState, error) {
	var fn changeFn
	var summary string
	switch as {
	case OwnerHuman:
		if c.Actor == model.ActorAI {
			return m.Get(hostID), apierr.New(wire.CodePermissionDenied, "AI 클라이언트는 사람 제어권을 가져올 수 없습니다. as를 ai로 바꿔 요청하세요", false, apierr.NextFixParams)
		}
		summary = "제어권을 가져왔습니다"
		fn = func(cur state, now time.Time) (state, error) {
			switch {
			case cur.owner == OwnerHuman:
				return cur, nil
			case cur.leased(now) && !force:
				return cur, needForce(cur)
			}
			return cur.owned(OwnerHuman, c), nil
		}
	case OwnerAI:
		if force {
			return m.Get(hostID), apierr.Invalid("force는 사람이 제어권을 가져올 때만 씁니다. force를 빼고 다시 요청하세요")
		}
		summary = "제어권을 얻었습니다"
		fn = m.aiFn(c)
	default:
		return m.Get(hostID), apierr.Invalid(`as는 "human"이나 "ai"여야 합니다. as를 고쳐 다시 요청하세요`)
	}
	st, err := m.change(ctx, hostID, c, summary, true, fn)
	if err != nil {
		var e *apierr.Error
		if errors.As(err, &e) && e.Code != wire.CodeInternal {
			_, _ = m.opt.Bus.Record(c.Entry(hostID, model.TimelineControl, "제어권 요청 거절: "+e.Message, model.TimelineDenied, ""))
		}
	}
	return st, err
}

// Release gives up the control of hostID. An AI client cannot release a
// person's control or another AI client's lease.
func (m *Manager) Release(ctx context.Context, hostID string, c ClientInfo) (model.ControlState, error) {
	return m.change(ctx, hostID, c, "제어권을 놓았습니다", true, func(cur state, now time.Time) (state, error) {
		switch {
		case cur.owner == OwnerNone:
			return cur, nil
		case cur.owner == OwnerHuman && c.Actor == model.ActorAI:
			return cur, heldByHuman()
		case cur.leased(now) && c.Actor == model.ActorAI && cur.clientID != c.ClientID:
			return cur, heldByAI(cur, m.ttl)
		}
		return cur.owned(OwnerNone, model.System), nil
	})
}

// NoteHumanInput records a viewer's local physical hold state, including deferred modifiers.
func (m *Manager) NoteHumanInput(hostID, clientID string, held bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.hostLocked(hostID)
	if h.humanPhysicalHeld == nil {
		h.humanPhysicalHeld = make(map[string]bool)
	}
	if held {
		h.humanPhysicalHeld[clientID] = true
	} else {
		delete(h.humanPhysicalHeld, clientID)
	}
	h.lastHumanInput = time.Now()
}

// TouchHumanInput marks activity before arbitration without changing held keys.
func (m *Manager) TouchHumanInput(hostID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hostLocked(hostID).lastHumanInput = time.Now()
}

// NoteHumanWire records forwarded input and keys or buttons held by a viewer.
func (m *Manager) NoteHumanWire(hostID, clientID string, held bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.hostLocked(hostID)
	if h.humanWireHeld == nil {
		h.humanWireHeld = make(map[string]bool)
	}
	if held {
		h.humanWireHeld[clientID] = true
	} else {
		delete(h.humanWireHeld, clientID)
	}
	h.lastHumanInput = time.Now()
}

func (m *Manager) ForgetHumanInput(hostID, clientID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.hostLocked(hostID)
	if h.humanPhysicalHeld[clientID] || h.humanWireHeld[clientID] {
		h.lastHumanInput = time.Now()
	}
	delete(h.humanPhysicalHeld, clientID)
	delete(h.humanWireHeld, clientID)
}

// ReleaseIfHuman releases only unchanged, unpaused human ownership.
// Stale idle timers must never release an AI lease or override an emergency stop.
func (m *Manager) ReleaseIfHuman(ctx context.Context, hostID string, c ClientInfo, expected uint64) (model.ControlState, error) {
	if c.Actor == model.ActorAI {
		return m.Get(hostID), apierr.New(wire.CodePermissionDenied, "conditional release requires a human client", false, "")
	}
	return m.change(ctx, hostID, c, "사용자 입력이 없어 제어권을 놓았습니다", true, func(cur state, _ time.Time) (state, error) {
		h := m.hostLocked(hostID) // change invokes this callback while holding m.mu.
		if cur.owner != OwnerHuman || cur.gen != expected || cur.aiPaused ||
			len(h.humanPhysicalHeld) != 0 || len(h.humanWireHeld) != 0 ||
			(!h.lastHumanInput.IsZero() && time.Since(h.lastHumanInput) < 5*time.Second) {
			return cur, nil
		}
		return cur.owned(OwnerNone, model.System), nil
	})
}

// Stop is the emergency stop: AI input of hostID stays paused until
// ResumeAI. A person who stops it holds control; an AI client that stops it
// leaves a person's control as it is and any other owner none. It applies
// whether or not the host is connected.
func (m *Manager) Stop(ctx context.Context, hostID string, c ClientInfo) (model.ControlState, error) {
	return m.change(ctx, hostID, c, "긴급 정지 · AI 입력을 멈췄습니다", true, func(cur state, _ time.Time) (state, error) {
		next := cur
		switch {
		case cur.owner == OwnerHuman:
		case c.Actor == model.ActorAI:
			next = cur.owned(OwnerNone, model.System)
		default:
			next = cur.owned(OwnerHuman, c)
		}
		next.aiPaused = true
		return next, nil
	})
}

// ResumeAI ends the AI pause of hostID and leaves it without an owner; only
// a person can.
func (m *Manager) ResumeAI(ctx context.Context, hostID string, c ClientInfo) (model.ControlState, error) {
	if c.Actor == model.ActorAI {
		return m.Get(hostID), apierr.New(wire.CodePermissionDenied, "AI 클라이언트는 긴급 정지를 풀 수 없습니다. 사람이 Deskhand에서 AI에게 넘기기를 눌러야 합니다", false, apierr.NextHumanResumeAI)
	}
	return m.change(ctx, hostID, c, "AI에게 제어권을 넘겼습니다", true, func(cur state, _ time.Time) (state, error) {
		next := cur.owned(OwnerNone, model.System)
		next.aiPaused = false
		return next, nil
	})
}

// aiFn is the change that gives client c AI control and starts or renews its
// lease, or refuses it.
func (m *Manager) aiFn(c ClientInfo) changeFn {
	return func(cur state, now time.Time) (state, error) {
		if err := m.aiRefusal(cur, c, now); err != nil {
			return cur, err
		}
		next := cur
		if cur.owner != OwnerAI || cur.clientID != c.ClientID {
			next = cur.owned(OwnerAI, c)
		}
		next.leaseUntil = now.Add(m.ttl)
		return next, nil
	}
}

// aiRefusal returns why client c may not drive a host in state s at now, or
// nil.
func (m *Manager) aiRefusal(s state, c ClientInfo, now time.Time) error {
	switch {
	case s.aiPaused:
		return aiPaused()
	case s.owner == OwnerHuman:
		return heldByHuman()
	case s.leased(now) && s.clientID != c.ClientID:
		return heldByAI(s, m.ttl)
	}
	return nil
}

// CheckAI is called before each AI input or UI operation of client c on
// hostID: it gives c AI control when nobody holds it, renews its lease and
// returns the writer generation the input carries. A person's control or
// the AI pause is CONTROL_HELD_BY_HUMAN, another AI client's lease
// CONTROL_PREEMPTED.
func (m *Manager) CheckAI(hostID string, c ClientInfo) (uint64, error) {
	st, err := m.change(m.base, hostID, c, "제어권을 얻었습니다", false, m.aiFn(c))
	return st.Gen, err
}

// CheckHuman is called before a person's input on hostID: it gives the
// person control when nobody holds it and returns the writer generation
// the input carries. An AI owner's lease is CONTROL_PREEMPTED.
func (m *Manager) CheckHuman(hostID string, c ClientInfo) (uint64, error) {
	st, err := m.change(m.base, hostID, c, "제어권을 가져왔습니다", false, func(cur state, now time.Time) (state, error) {
		switch {
		case cur.owner == OwnerHuman:
			return cur, nil
		case cur.leased(now):
			return cur, apierr.New(wire.CodeControlPreempted,
				fmt.Sprintf("AI 클라이언트(%s)가 조작 중입니다. 제어권을 가져온 뒤 조작하세요", clientLabel(cur)), false, "")
		}
		return cur.owned(OwnerHuman, c), nil
	})
	return st.Gen, err
}

// WriterGen returns the writer generation a UI operation of client c on
// hostID carries: an AI client's goes through CheckAI, a person's and the
// daemon's is the current one.
func (m *Manager) WriterGen(hostID string, c ClientInfo) (uint64, error) {
	if c.Actor == model.ActorAI {
		return m.CheckAI(hostID, c)
	}
	return m.HumanGen(hostID), nil
}

// WriterCheck returns nil while a UI operation of client c on hostID that
// carries writer generation gen may go out: an AI client's while StillAI,
// a person's and the daemon's while gen is the current one.
func (m *Manager) WriterCheck(hostID string, c ClientInfo, gen uint64) error {
	if c.Actor == model.ActorAI {
		return m.StillAI(hostID, c, gen)
	}
	if m.HumanGen(hostID) != gen {
		return apierr.New(wire.CodeControlPreempted, "보내기 전에 제어권이 바뀌어 보내지 않았습니다. 제어권 상태를 확인한 뒤 다시 시도하세요", true, apierr.NextRetry)
	}
	return nil
}

// StillAI returns nil while client c holds AI control of hostID at
// generation gen, and why it no longer does otherwise.
func (m *Manager) StillAI(hostID string, c ClientInfo, gen uint64) error {
	cur := m.current(hostID)
	now := time.Now()
	if cur.gen == gen && cur.owner == OwnerAI && cur.clientID == c.ClientID {
		return nil
	}
	if err := m.aiRefusal(cur, c, now); err != nil {
		return err
	}
	return apierr.New(wire.CodeControlPreempted, "보내는 사이 제어권이 바뀌어 남은 입력을 보내지 않았습니다. 화면을 다시 확인한 뒤 다시 시도하세요", false, apierr.NextCheckState)
}

func clientLabel(s state) string {
	if s.clientName != "" {
		return s.clientName
	}
	if s.clientID != "" {
		return s.clientID
	}
	return "이름 없음"
}

func heldByHuman() *apierr.Error {
	return apierr.New(wire.CodeControlHeldByHuman, "사람이 조작 중입니다. 사람이 제어권을 놓을 때까지 기다리세요", false, apierr.NextHumanRelease)
}

func aiPaused() *apierr.Error {
	return apierr.New(wire.CodeControlHeldByHuman, "긴급 정지로 AI 입력이 멈춰 있습니다. 사람이 Deskhand에서 AI에게 넘기기를 누를 때까지 기다리세요", false, apierr.NextHumanResumeAI)
}

func heldByAI(s state, ttl time.Duration) *apierr.Error {
	return apierr.New(wire.CodeControlPreempted,
		fmt.Sprintf("다른 AI 클라이언트(%s)가 이 PC를 조작 중입니다. 그 클라이언트가 제어권을 놓거나 %s 동안 입력하지 않아 풀린 뒤 다시 시도하세요", clientLabel(s), ttl),
		true, apierr.NextRetry)
}

func needForce(s state) *apierr.Error {
	return apierr.Invalid(fmt.Sprintf("AI 클라이언트(%s)가 조작 중입니다. 가져오려면 force를 켜고 다시 요청하세요", clientLabel(s)))
}
