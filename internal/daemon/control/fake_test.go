package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// errReset is the error of a pipe direction the other side reset.
var errReset = errors.New("stream reset")

// pipeEnd is one end of an in-memory stream whose directions end apart;
// beforeWrite, when set, runs before each write.
type pipeEnd struct {
	r           *io.PipeReader
	w           *io.PipeWriter
	beforeWrite func()
}

func (p pipeEnd) Read(b []byte) (int, error) { return p.r.Read(b) }
func (p pipeEnd) Write(b []byte) (int, error) {
	if p.beforeWrite != nil {
		p.beforeWrite()
	}
	return p.w.Write(b)
}
func (p pipeEnd) CloseWrite() error                { return p.w.Close() }
func (p pipeEnd) CancelWrite()                     { p.w.CloseWithError(errReset) }
func (p pipeEnd) CancelRead()                      { p.r.CloseWithError(errReset) }
func (p pipeEnd) SetReadDeadline(time.Time) error  { return nil }
func (p pipeEnd) SetWriteDeadline(time.Time) error { return nil }
func (p pipeEnd) Close() error {
	p.w.Close()
	p.r.Close()
	return nil
}

// duplex returns the daemon's and the agent's end of a new stream.
func duplex() (daemon, agent pipeEnd) {
	toAgent, fromDaemon := io.Pipe()
	toDaemon, fromAgent := io.Pipe()
	return pipeEnd{r: toDaemon, w: fromDaemon}, pipeEnd{r: toAgent, w: fromAgent}
}

// fakeStream is what an input stream of the fake agent received.
type fakeStream struct {
	hdrGen   uint64
	events   []wire.InputEvent
	typed    strings.Builder
	releases []uint64
	acks     []wire.InputAck
}

// fakeAgent answers control.set_writer and serves input streams with the
// writer generation rules of docs/protocol.md §3.1 and §6: a text is typed
// one rune at a time and stops once the writer generation rises past its
// gen.
type fakeAgent struct {
	mu        sync.Mutex
	state     conn.ConnState
	connGen   uint64
	writer    uint64
	setWriter []uint64
	held      map[string]bool
	streams   []*fakeStream
	charDelay time.Duration
	// ops are the rpc ops the agent received.
	ops []string
	// failSetWriter is how many more control.set_writer calls fail.
	failSetWriter int
	// stall, while set, holds every write of the daemon on an input stream
	// until it is closed.
	stall chan struct{}
}

func newFakeAgent() *fakeAgent {
	return &fakeAgent{state: conn.StateReady, connGen: 1, held: map[string]bool{}, charDelay: 2 * time.Millisecond}
}

func (a *fakeAgent) State() conn.ConnState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *fakeAgent) Generation() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.connGen
}

func (a *fakeAgent) setState(st conn.ConnState) {
	a.mu.Lock()
	a.state = st
	if st == conn.StateReady {
		a.connGen++
	}
	a.mu.Unlock()
}

// restart makes the agent a new process: writer generation 0, offline.
func (a *fakeAgent) restart() {
	a.mu.Lock()
	a.state, a.writer, a.held = conn.StateReconnecting, 0, map[string]bool{}
	a.mu.Unlock()
}

func offlineErr() error {
	return apierr.New(wire.CodeOffline, "pc is offline", true, apierr.NextHostConnect)
}

func (a *fakeAgent) Call(ctx context.Context, op string, params proto.Message, _ conn.CallOpts) (proto.Message, error) {
	a.mu.Lock()
	st := a.state
	a.mu.Unlock()
	if st != conn.StateReady && st != conn.StateDegraded {
		return nil, offlineErr()
	}
	return a.call(ctx, op, params)
}

// call answers a request whatever the connection state: the PrepareCall of
// a connection that is being prepared.
func (a *fakeAgent) call(_ context.Context, op string, params proto.Message) (proto.Message, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ops = append(a.ops, op)
	if op == "control.set_writer" && a.failSetWriter > 0 {
		a.failSetWriter--
		return nil, apierr.New(wire.CodeTimeout, "set_writer timed out", true, apierr.NextRetry)
	}
	if op != "control.set_writer" {
		return nil, apierr.New(wire.CodeCapabilityUnsupported, "unknown op '"+op+"'", false, "")
	}
	g, ok := genOf(params["gen"])
	if !ok {
		return nil, apierr.Invalid("control.set_writer needs gen")
	}
	a.setWriter = append(a.setWriter, g)
	a.raiseLocked(g)
	return proto.Message{"gen": float64(a.writer)}, nil
}

// raiseLocked raises the writer generation to gen, releasing every held
// key; a.mu is held.
func (a *fakeAgent) raiseLocked(gen uint64) {
	if gen > a.writer {
		a.writer = gen
		a.held = map[string]bool{}
	}
}

func (a *fakeAgent) OpenStream(_ context.Context, hdr wire.StreamHeader) (conn.DataStream, uint64, error) {
	a.mu.Lock()
	if a.state != conn.StateReady && a.state != conn.StateDegraded {
		a.mu.Unlock()
		return nil, 0, offlineErr()
	}
	cg := a.connGen
	a.mu.Unlock()
	if hdr.S != "input" {
		return nil, 0, fmt.Errorf("the fake agent has no %s stream", hdr.S)
	}
	d, ag := duplex()
	d.beforeWrite = a.waitStall
	go a.serveInput(ag, hdr)
	return d, cg, nil
}

func (a *fakeAgent) serveInput(st pipeEnd, hdr wire.StreamHeader) {
	defer st.Close()
	a.mu.Lock()
	if hdr.Gen < a.writer {
		cur := a.writer
		a.mu.Unlock()
		_ = wire.WriteHeader(st, map[string]any{"ok": false, "code": wire.CodeControlPreempted,
			"error": fmt.Sprintf("writer generation %d is older than the current %d", hdr.Gen, cur)})
		return
	}
	a.raiseLocked(hdr.Gen)
	fs := &fakeStream{hdrGen: hdr.Gen}
	a.streams = append(a.streams, fs)
	cur := a.writer
	a.mu.Unlock()
	if wire.WriteHeader(st, map[string]any{"ok": true, "gen": cur}) != nil {
		return
	}
	queue := make(chan wire.InputEvent, 256)
	go func() {
		defer close(queue)
		for {
			kind, payload, err := wire.ReadFrame(st, 4<<20)
			if err != nil {
				return
			}
			if kind != wire.KindClientInput {
				continue
			}
			var ev wire.InputEvent
			if json.Unmarshal(payload, &ev) != nil {
				continue
			}
			a.mu.Lock()
			fs.events = append(fs.events, ev)
			a.raiseLocked(ev.Gen)
			a.mu.Unlock()
			queue <- ev
		}
	}()
	for ev := range queue {
		ack := a.apply(fs, ev)
		a.mu.Lock()
		fs.acks = append(fs.acks, ack)
		a.mu.Unlock()
		b, _ := json.Marshal(ack)
		if wire.WriteFrame(st, wire.KindInputAck, b) != nil {
			for range queue {
			}
			return
		}
	}
}

func preemptedAck(seq uint64, msg string) wire.InputAck {
	return wire.InputAck{Seq: seq, Code: wire.CodeControlPreempted, Error: msg}
}

func (a *fakeAgent) apply(fs *fakeStream, ev wire.InputEvent) wire.InputAck {
	a.mu.Lock()
	if ev.Gen < a.writer {
		a.mu.Unlock()
		return preemptedAck(ev.Seq, "older writer generation")
	}
	delay := a.charDelay
	a.mu.Unlock()
	switch ev.K {
	case "text":
		n := 0
		for _, r := range ev.Text {
			time.Sleep(delay)
			a.mu.Lock()
			if a.writer > ev.Gen {
				a.mu.Unlock()
				return preemptedAck(ev.Seq, fmt.Sprintf("preempted after typing %d characters", n))
			}
			fs.typed.WriteRune(r)
			a.mu.Unlock()
			n++
		}
	case "key":
		a.mu.Lock()
		k := fmt.Sprintf("sc:%#x", ev.SC)
		if ev.Down {
			a.held[k] = true
		} else {
			delete(a.held, k)
		}
		a.mu.Unlock()
	case "release_all":
		a.mu.Lock()
		a.held = map[string]bool{}
		fs.releases = append(fs.releases, ev.Gen)
		a.mu.Unlock()
	}
	return wire.InputAck{Seq: ev.Seq, OK: true, DoneMonoUS: 1}
}

func (a *fakeAgent) writerGen() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writer
}

func (a *fakeAgent) pushes() []uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]uint64(nil), a.setWriter...)
}

func (a *fakeAgent) heldKeys() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for k := range a.held {
		out = append(out, k)
	}
	return out
}

func (a *fakeAgent) stream(i int) (hdrGen uint64, evs []wire.InputEvent, typed string, releases []uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i >= len(a.streams) {
		return 0, nil, "", nil
	}
	s := a.streams[i]
	return s.hdrGen, append([]wire.InputEvent(nil), s.events...), s.typed.String(), append([]uint64(nil), s.releases...)
}

func (a *fakeAgent) streamCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.streams)
}

// waitStall waits while a stall is set.
func (a *fakeAgent) waitStall() {
	a.mu.Lock()
	st := a.stall
	a.mu.Unlock()
	if st != nil {
		<-st
	}
}

// setWriterCalls counts the control.set_writer calls, failed ones too.
func (a *fakeAgent) setWriterCalls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, op := range a.ops {
		if op == "control.set_writer" {
			n++
		}
	}
	return n
}

// fakeAgents serves one host, "pc".
type fakeAgents struct{ a *fakeAgent }

func (f fakeAgents) Agent(hostID string) Agent {
	if hostID != "pc" || f.a == nil {
		return nil
	}
	return f.a
}

// rig is a Manager over a temporary store with the fake agent of "pc".
type rig struct {
	t     *testing.T
	path  string
	st    *store.Store
	bus   *events.Bus
	sub   *events.Sub
	agent *fakeAgent
	m     *Manager
	ttl   time.Duration
}

func newRig(t *testing.T, ttl time.Duration) *rig {
	t.Helper()
	r := &rig{t: t, path: filepath.Join(t.TempDir(), "deskhand.db"), agent: newFakeAgent(), ttl: ttl}
	r.open()
	t.Cleanup(r.close)
	return r
}

// open opens the store and starts a Manager on it, as a daemon start does.
func (r *rig) open() {
	r.t.Helper()
	st, err := store.Open(r.path)
	if err != nil {
		r.t.Fatal(err)
	}
	r.st = st
	r.bus = events.NewBus(st, nil)
	r.sub = r.bus.Subscribe()
	r.m = New(Options{Store: st, Bus: r.bus, Agents: fakeAgents{r.agent}, LeaseTTL: r.ttl})
	if err := r.m.Load(); err != nil {
		r.t.Fatal(err)
	}
}

// close stops the Manager and the store, as a daemon stop does.
func (r *rig) close() {
	if r.m != nil {
		r.m.Close()
		r.sub.Close()
		r.st.Close()
		r.m = nil
	}
}

// changes returns the states of the control.changed events published so
// far, in order.
func (r *rig) changes() []model.ControlState {
	r.t.Helper()
	var out []model.ControlState
	for {
		select {
		case ev := <-r.sub.C:
			if ev.Kind != events.ControlChanged {
				continue
			}
			out = append(out, stateOfFields(ev.Fields))
		default:
			return out
		}
	}
}

// nextChange waits up to d for the next control.changed event.
func (r *rig) nextChange(d time.Duration) (model.ControlState, bool) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case ev := <-r.sub.C:
			if ev.Kind == events.ControlChanged {
				return stateOfFields(ev.Fields), true
			}
		case <-timer.C:
			return model.ControlState{}, false
		}
	}
}

func stateOfFields(f map[string]any) model.ControlState {
	s := model.ControlState{}
	s.Owner, _ = f["owner"].(string)
	s.OwnerClient, _ = f["owner_client"].(string)
	s.OwnerClientID, _ = f["owner_client_id"].(string)
	s.Gen, _ = genOf(f["gen"])
	s.AIPaused, _ = f["ai_paused"].(bool)
	return s
}

// eventually waits up to 5 s for cond.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

var (
	human  = model.Caller{Actor: model.ActorHuman, ClientID: "c-app", ClientName: "Deskhand"}
	human2 = model.Caller{Actor: model.ActorHuman, ClientID: "c-cli", ClientName: "deskhand-cli"}
	aiA    = model.Caller{Actor: model.ActorAI, ClientID: "c-mcp-a", ClientName: "deskhand-mcp/claude-code"}
	aiB    = model.Caller{Actor: model.ActorAI, ClientID: "c-mcp-b", ClientName: "deskhand-mcp/other"}
)

// codeOf returns the code and next action of err.
func codeOf(err error) (string, string) {
	var e *apierr.Error
	if errors.As(err, &e) {
		return e.Code, e.NextAction
	}
	return "", ""
}
