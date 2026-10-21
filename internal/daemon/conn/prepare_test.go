package conn

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestPrepareRunsBeforeReadyAndAWaitingCallFollowsIt(t *testing.T) {
	f := newFakeAgent(t)
	var h *Host
	answered := make(chan error, 1)
	var duringPrepare []string
	m, _, _ := testManager(t, f, func(o *Options) {
		o.Prepare = func(ctx context.Context, hostID string, call PrepareCall) error {
			if hostID != target.HostID || h.State() == StateReady {
				t.Errorf("prepare of %s in state %s", hostID, h.State())
			}
			go func() {
				_, err := h.Call(context.Background(), "fs.list", proto.Message{"path": "C:/"}, CallOpts{})
				answered <- err
			}()
			res, err := call(ctx, "control.set_writer", proto.Message{"gen": 7})
			if err != nil || res["gen"] != float64(7) {
				t.Errorf("set_writer during prepare: %v, %v", res, err)
			}
			time.Sleep(100 * time.Millisecond)
			for _, r := range f.requests("fs.list") {
				duringPrepare = append(duringPrepare, r.String("op"))
			}
			return nil
		}
	})
	h = m.Add(target)
	h.Connect()
	waitState(t, h, StateReady)
	if err := <-answered; err != nil {
		t.Fatalf("the waiting call: %v", err)
	}
	if len(duringPrepare) != 0 {
		t.Fatalf("requests written before the connection was ready: %v", duringPrepare)
	}
	f.mu.Lock()
	var ops []string
	for _, r := range f.received {
		ops = append(ops, r.String("op"))
	}
	f.mu.Unlock()
	if i, j := slices.Index(ops, "control.set_writer"), slices.Index(ops, "fs.list"); i < 0 || j < i {
		t.Fatalf("request order %v", ops)
	}
}

func TestFailedPrepareEndsTheConnectionAndReconnects(t *testing.T) {
	f := newFakeAgent(t)
	var tries atomic.Int32
	m, _, evs := testManager(t, f, func(o *Options) {
		o.Prepare = func(ctx context.Context, _ string, call PrepareCall) error {
			if tries.Add(1) == 1 {
				return errors.New("the agent did not take the writer generation")
			}
			_, err := call(ctx, "control.set_writer", proto.Message{"gen": 1})
			return err
		}
	})
	h := connected(t, m)
	if tries.Load() != 2 || f.dialCount() != 2 || h.Generation() != 1 {
		t.Fatalf("prepare tries %d, dials %d, generation %d", tries.Load(), f.dialCount(), h.Generation())
	}
	st := evs.states()
	if first := slices.Index(st, "ready"); first < 0 || !slices.Contains(st[:first], "reconnecting") {
		t.Fatalf("states %v: the failed prepare did not reconnect before the first ready", st)
	}
}

func TestAgentEventsDuringPrepareFollowReady(t *testing.T) {
	f := newFakeAgent(t)
	f.capsAfterWelcome = true
	m, _, evs := testManager(t, f, func(o *Options) {
		o.Prepare = func(ctx context.Context, _ string, call PrepareCall) error {
			time.Sleep(50 * time.Millisecond)
			_, err := call(ctx, "control.set_writer", proto.Message{"gen": 1})
			return err
		}
	})
	h := connected(t, m)
	deadline := time.Now().Add(5 * time.Second)
	for h.Hello().Caps["exec"].State != "unavailable" {
		if time.Now().After(deadline) {
			t.Fatalf("caps after caps.changed %+v", h.Hello().Caps)
		}
		time.Sleep(time.Millisecond)
	}
	evs.mu.Lock()
	defer evs.mu.Unlock()
	ready, agent := -1, -1
	for j, ev := range evs.evs {
		if ev.Kind == KindConnectionState && ev.Payload.String("state") == "ready" && ready < 0 {
			ready = j
		}
		if ev.Kind == KindAgentEvent && agent < 0 {
			agent = j
		}
	}
	if ready < 0 || agent < ready {
		t.Fatalf("ready event at %d, first agent event at %d", ready, agent)
	}
}

func TestReadyCheckRefusesTheRequestUnsent(t *testing.T) {
	f := newFakeAgent(t)
	m, l, _ := testManager(t, f, nil)
	h := connected(t, m)
	refused := apierr.New(wire.CodeControlPreempted, "the writer changed", false, "")
	info := CallInfo{}
	_, err := h.Call(context.Background(), "exec.run", proto.Message{"argv": []string{"echo"}}, CallOpts{Info: &info,
		Ready: func() error { return refused }})
	if !errors.Is(err, refused) || info.Sent || f.execCount() != 0 || len(f.requests("exec.run")) != 0 {
		t.Fatalf("err %v, info %+v, execs %d", err, info, f.execCount())
	}
	if got := l.history(info.RID); len(got) != 0 && !slices.Equal(got, []string{"sent", "not_sent"}) {
		t.Fatalf("request log %v", got)
	}
	if _, err := h.Call(context.Background(), "exec.run", proto.Message{"argv": []string{"echo"}}, CallOpts{Ready: func() error { return nil }}); err != nil {
		t.Fatal(err)
	}
}
