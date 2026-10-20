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

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/transport"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// pipeSession is one fake agent connection over net.Pipe.
type pipeSession struct {
	c    net.Conn
	once sync.Once
}

func (s *pipeSession) OpenStream(context.Context) (conn.Stream, error) { return s.c, nil }
func (s *pipeSession) Close(string) error {
	s.once.Do(func() { s.c.Close() })
	return nil
}
func (s *pipeSession) Err() error { return nil }

// killAgent answers exec.kill with the job.exited event before its answer
// when exitFirst is set, after it otherwise.
type killAgent struct {
	exitFirst bool
}

func (a *killAgent) dial(context.Context, conn.Target, transport.Identity) (conn.Session, error) {
	d, g := net.Pipe()
	go a.serve(g)
	return &pipeSession{c: d}, nil
}

func (a *killAgent) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	var h wire.StreamHeader
	if wire.ReadHeader(br, &h) != nil {
		return
	}
	send := func(m proto.Message) bool {
		b, _ := json.Marshal(m)
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
		res := proto.Message{"t": "res", "id": m["id"], "ok": false, "error": "unknown op"}
		if m.String("op") == "exec.kill" {
			res = proto.Message{"t": "res", "id": m["id"], "ok": true, "state": "exited"}
			ev := proto.Message{"t": "ev", "name": "job.exited", "job": m.String("job"), "code": -1}
			if a.exitFirst {
				send(ev)
				send(res)
			} else {
				send(res)
				send(ev)
			}
			continue
		}
		if !send(res) {
			return
		}
	}
}

func TestCancelWinsOverExitInEitherOrder(t *testing.T) {
	for _, exitFirst := range []bool{true, false} {
		st, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
		if err != nil {
			t.Fatal(err)
		}
		bus := events.NewBus(st, nil)
		sub := bus.Subscribe()
		var r *Registry
		agent := &killAgent{exitFirst: exitFirst}
		handled := make(chan struct{}, 1)
		mgr := conn.NewManager(conn.Options{Dial: agent.dial, PingInterval: time.Hour, OnEvent: func(ev conn.Event) {
			if ev.Kind == conn.KindAgentEvent {
				r.HandleAgentEvent(ev.HostID, ev.Payload)
				handled <- struct{}{}
			}
		}})
		r = New(st, bus, mgr)
		h := mgr.Add(conn.Target{HostID: "pc", Name: "pc"})
		h.Connect()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.WaitState(ctx, func(s conn.ConnState) bool { return s == conn.StateReady }); err != nil {
			t.Fatal(err)
		}
		j, _ := r.Create("pc", KindExec, "r-1", "sleep 30", nil)
		if _, err := r.SetRemote(j.JobID, "j1", map[string]any{"job": "j1", "pid": 4}); err != nil {
			t.Fatal(err)
		}
		got, err := r.Cancel(ctx, j.JobID, nil)
		if err != nil || got.Status != model.StatusCancelled {
			t.Fatalf("exit first %v: cancel %+v, %v", exitFirst, got, err)
		}
		select {
		case <-handled:
		case <-ctx.Done():
			t.Fatalf("exit first %v: the job.exited event was not handled", exitFirst)
		}
		if cur, _ := r.current(j.JobID); cur.Status != model.StatusCancelled {
			t.Fatalf("exit first %v: job after the exit event %+v", exitFirst, cur)
		}
		for {
			select {
			case ev := <-sub.C:
				if ev.Kind == events.JobUpdated && ev.Fields["job"].(model.Job).Status == model.StatusFailed {
					t.Fatalf("exit first %v: the job was reported failed", exitFirst)
				}
				continue
			default:
			}
			break
		}
		if _, err := r.Cancel(ctx, j.JobID, nil); err == nil {
			t.Fatal("a cancelled job was cancelled again")
		}
		cancel()
		mgr.Close()
		st.Close()
	}
}

func TestFinishKeepsFinishedJob(t *testing.T) {
	r, _ := newRegistry(t)
	j, _ := r.Create("pc", KindExec, "r-1", "x", nil)
	if _, err := r.Finish(j.JobID, model.StatusCancelled, map[string]any{"state": "cancelled"}); err != nil {
		t.Fatal(err)
	}
	got, err := r.Finish(j.JobID, model.StatusFailed, map[string]any{"code": -1})
	if err != nil || got.Status != model.StatusCancelled || string(got.Result) != `{"state":"cancelled"}` {
		t.Fatalf("second finish %+v, %v", got, err)
	}
	if _, err := r.Finish("j-none", model.StatusFailed, nil); apierr.CodeOf(err) != wire.CodeNotFound {
		t.Fatalf("finish of a missing job: %v", err)
	}
}
