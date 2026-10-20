package conn

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/transport"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// pipeSession is the daemon side of a fake agent connection: one stream
// over net.Pipe.
type pipeSession struct {
	c      net.Conn
	once   sync.Once
	opened bool
	mu     sync.Mutex
}

func (s *pipeSession) OpenStream(context.Context) (Stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opened {
		return nil, errors.New("the fake session has one stream")
	}
	s.opened = true
	return s.c, nil
}

func (s *pipeSession) Close(string) error {
	s.once.Do(func() { s.c.Close() })
	return nil
}

func (s *pipeSession) Err() error { return nil }

// fakeAgent answers the rpc protocol of docs/protocol.md §3 over pipes.
type fakeAgent struct {
	t *testing.T

	mu       sync.Mutex
	dials    int
	execs    int
	journal  map[string]proto.Message
	received []proto.Message
	// dialErr returns the error of the n-th dial (from 1), or nil.
	dialErr func(n int) error
	// dropBefore reports whether the agent closes the connection after
	// running req instead of answering it.
	dropBefore func(req proto.Message) bool
	// forget keeps the agent from journaling answers.
	forget bool
	// noPong keeps the agent from answering ping.
	noPong bool
	conns  []net.Conn
}

func newFakeAgent(t *testing.T) *fakeAgent {
	f := &fakeAgent{t: t, journal: map[string]proto.Message{}}
	t.Cleanup(func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, c := range f.conns {
			c.Close()
		}
	})
	return f
}

func (f *fakeAgent) dial(_ context.Context, _ Target, _ transport.Identity) (Session, error) {
	f.mu.Lock()
	f.dials++
	n := f.dials
	dialErr := f.dialErr
	f.mu.Unlock()
	if dialErr != nil {
		if err := dialErr(n); err != nil {
			return nil, err
		}
	}
	a, b := net.Pipe()
	f.mu.Lock()
	f.conns = append(f.conns, a, b)
	f.mu.Unlock()
	go f.serve(b)
	return &pipeSession{c: a}, nil
}

func (f *fakeAgent) dialCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dials
}

func (f *fakeAgent) execCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.execs
}

func (f *fakeAgent) requests(op string) []proto.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []proto.Message
	for _, m := range f.received {
		if m.String("op") == op {
			out = append(out, m)
		}
	}
	return out
}

func send(c net.Conn, m proto.Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = c.Write(append(b, '\n'))
	return err
}

func (f *fakeAgent) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	var h wire.StreamHeader
	if err := wire.ReadHeader(br, &h); err != nil || h.S != "rpc" {
		return
	}
	hello := proto.Message{"t": "hello", "v": 1, "agent": "fake-1", "os": "windows", "arch": "amd64", "host": "PC",
		"caps":    map[string]any{"exec": map[string]any{"state": "ready"}},
		"session": map[string]any{"session_id": 1, "user": "bob", "connect_state": "active"}, "displays": []any{}, "boot_id": "b1"}
	if send(c, hello) != nil {
		return
	}
	r := proto.NewReader(br)
	w, err := r.Next()
	if err != nil || w.Type() != "welcome" {
		return
	}
	for {
		m, err := r.Next()
		if err != nil {
			return
		}
		if m.Type() != "req" {
			continue
		}
		f.mu.Lock()
		f.received = append(f.received, m)
		f.mu.Unlock()
		res, drop := f.handle(m)
		if drop {
			return
		}
		if res == nil {
			continue
		}
		res["t"], res["id"] = "res", m["id"]
		if send(c, res) != nil {
			return
		}
	}
}

func (f *fakeAgent) handle(m proto.Message) (proto.Message, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rid := m.String("rid")
	switch m.String("op") {
	case "ping":
		if f.noPong {
			return nil, false
		}
		return proto.Message{"ok": true, "pong": true}, false
	case "req.status":
		stored, ok := f.journal[rid]
		if !ok {
			return proto.Message{"ok": true, "state": "unknown"}, false
		}
		out := proto.Message{"state": "done"}
		for k, v := range stored {
			out[k] = v
		}
		return out, false
	case "exec.run":
		if stored, ok := f.journal[rid]; ok && rid != "" {
			out := proto.Message{"replayed": true}
			for k, v := range stored {
				out[k] = v
			}
			return out, false
		}
		f.execs++
		res := proto.Message{"ok": true, "code": 0, "stdout": "hi\n", "stderr": "", "timed_out": false, "truncated": false}
		if rid != "" && !f.forget {
			f.journal[rid] = res
		}
		if f.dropBefore != nil && f.dropBefore(m) {
			return nil, true
		}
		return res, false
	case "fs.list":
		return proto.Message{"ok": true, "entries": []any{}, "truncated": false}, false
	case "fail.coded":
		return proto.Message{"ok": false, "code": "STALE_TARGET", "error": "element e1 no longer exists; call ui.tree again", "retryable": false}, false
	}
	return proto.Message{"ok": false, "error": "unknown op '" + m.String("op") + "'; update deskhand-agent"}, false
}

// memLog is a RequestLog kept in memory.
type memLog struct {
	mu     sync.Mutex
	states map[string][]string
	ops    map[string]string
}

func newMemLog() *memLog { return &memLog{states: map[string][]string{}, ops: map[string]string{}} }

func (l *memLog) RequestSent(rid, _, op string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ops[rid] = op
	l.states[rid] = append(l.states[rid], "sent")
	return nil
}

func (l *memLog) SetRequestState(rid, state string, _ json.RawMessage) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.states[rid] = append(l.states[rid], state)
	return nil
}

func (l *memLog) history(rid string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.states[rid]...)
}

// eventLog records the events of a manager.
type eventLog struct {
	mu  sync.Mutex
	evs []Event
}

func (e *eventLog) add(ev Event) {
	e.mu.Lock()
	e.evs = append(e.evs, ev)
	e.mu.Unlock()
}

func (e *eventLog) states() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, ev := range e.evs {
		if ev.Kind == KindConnectionState {
			out = append(out, ev.Payload.String("state"))
		}
	}
	return out
}

func testManager(t *testing.T, f *fakeAgent, edit func(*Options)) (*Manager, *memLog, *eventLog) {
	t.Helper()
	l, evs := newMemLog(), &eventLog{}
	opt := Options{
		Controller:   "test-mac",
		Dial:         f.dial,
		Requests:     l,
		OnEvent:      evs.add,
		Backoff:      BackoffConfig{Initial: 5 * time.Millisecond, Max: 20 * time.Millisecond},
		ReadyWait:    5 * time.Second,
		ResolveWait:  5 * time.Second,
		PingInterval: time.Hour,
	}
	if edit != nil {
		edit(&opt)
	}
	m := NewManager(opt)
	t.Cleanup(m.Close)
	return m, l, evs
}

func waitState(t *testing.T, h *Host, want ConnState) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.WaitState(ctx, func(s ConnState) bool { return s == want }); err != nil {
		t.Fatalf("state %s, want %s: %v", h.State(), want, err)
	}
}

var _ io.ReadWriteCloser = (net.Conn)(nil)
