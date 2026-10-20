package jobs

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/transport"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// scriptAgent is a fake agent whose answers handle returns: the messages
// to send for one request, the answer and events in their order. A nil
// handle result answers "unknown op".
type scriptAgent struct {
	mu     sync.Mutex
	handle func(req proto.Message) []proto.Message
	ops    []string
}

func (a *scriptAgent) dial(context.Context, conn.Target, transport.Identity) (conn.Session, error) {
	d, g := net.Pipe()
	go a.serve(g)
	return &pipeSession{c: d}, nil
}

// requests returns the ops received so far.
func (a *scriptAgent) requests() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.ops...)
}

func (a *scriptAgent) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	var h wire.StreamHeader
	if wire.ReadHeader(br, &h) != nil {
		return
	}
	var wmu sync.Mutex
	send := func(m proto.Message) bool {
		b, _ := json.Marshal(m)
		wmu.Lock()
		defer wmu.Unlock()
		_, err := c.Write(append(b, '\n'))
		return err == nil
	}
	if !send(proto.Message{"t": "hello", "v": 1, "agent": "fake", "os": "windows", "caps": map[string]any{}, "session": map[string]any{}}) {
		return
	}
	r := proto.NewReader(br)
	if m, err := r.Next(); err != nil || m.Type() != "welcome" {
		return
	}
	for {
		m, err := r.Next()
		if err != nil {
			return
		}
		a.mu.Lock()
		a.ops = append(a.ops, m.String("op"))
		handle := a.handle
		a.mu.Unlock()
		var out []proto.Message
		if handle != nil {
			out = handle(m)
		}
		if out == nil {
			out = []proto.Message{{"t": "res", "ok": false, "error": "unknown op"}}
		}
		for _, o := range out {
			if o.Type() == "res" {
				o["id"] = m["id"]
			}
			if !send(o) {
				return
			}
		}
	}
}

// ok returns the answer of a request with fields.
func ok(fields proto.Message) proto.Message {
	m := proto.Message{"t": "res", "ok": true}
	for k, v := range fields {
		m[k] = v
	}
	return m
}

// agentEnv is a Registry whose host "pc" is served by a scriptAgent.
type agentEnv struct {
	r     *Registry
	st    *store.Store
	mgr   *conn.Manager
	host  *conn.Host
	agent *scriptAgent
	sub   *events.Sub
}

// newAgentEnv connects the Registry's host "pc" to agent and waits until it
// is ready. Agent events reach HandleAgentEvent and a ready host reaches
// Reconcile, as in the daemon.
func newAgentEnv(t *testing.T, agent *scriptAgent) *agentEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return connectAgentEnv(t, st, agent)
}

func connectAgentEnv(t *testing.T, st *store.Store, agent *scriptAgent) *agentEnv {
	t.Helper()
	bus := events.NewBus(st, nil)
	sub := bus.Subscribe()
	var r *Registry
	mgr := conn.NewManager(conn.Options{Dial: agent.dial, PingInterval: time.Hour,
		OnEvent: func(ev conn.Event) {
			if ev.Kind == conn.KindAgentEvent {
				r.HandleAgentEvent(ev.HostID, ev.Payload)
			}
		},
		OnReady: func(hostID string, _ uint64) { go r.Reconcile(hostID) },
	})
	t.Cleanup(mgr.Close)
	r = New(st, bus, mgr)
	t.Cleanup(r.Close)
	h := mgr.Add(conn.Target{HostID: "pc", Name: "pc"})
	h.Connect()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.WaitState(ctx, func(s conn.ConnState) bool { return s == conn.StateReady }); err != nil {
		t.Fatal(err)
	}
	return &agentEnv{r: r, st: st, mgr: mgr, host: h, agent: agent, sub: sub}
}
