package observe

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/control"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// fakeAgent answers display.list with two displays, the left one at a
// negative origin, and fails screen.capture with capErr.
type fakeAgent struct {
	mu      sync.Mutex
	connGen uint64
	capErr  error
}

func (a *fakeAgent) Call(_ context.Context, op string, _ proto.Message, opts conn.CallOpts) (proto.Message, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if opts.Info != nil {
		opts.Info.Generation, opts.Info.Sent, opts.Info.Answered = a.connGen, true, true
	}
	switch op {
	case "display.list":
		return proto.Message{"displays": []any{
			map[string]any{"index": float64(0), "x": float64(0), "y": float64(0), "width": float64(2560), "height": float64(1440)},
			map[string]any{"index": float64(1), "x": float64(-1920), "y": float64(-200), "width": float64(1920), "height": float64(1080)},
		}}, nil
	case "screen.capture":
		return nil, a.capErr
	}
	return nil, apierr.New(wire.CodeCapabilityUnsupported, "no "+op, false, "")
}

func (a *fakeAgent) State() conn.ConnState { return conn.StateReady }

func (a *fakeAgent) Generation() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.connGen
}

type oneAgent struct{ a *fakeAgent }

func (o oneAgent) Agent(hostID string) Agent {
	if hostID != "pc" {
		return nil
	}
	return o.a
}

// fakeControl records the sends and acks every event of a stream on
// connection connGen.
type fakeControl struct {
	mu      sync.Mutex
	connGen uint64
	sends   [][]wire.InputEvent
	opts    []control.SendOpts
}

func (f *fakeControl) Send(_ context.Context, _ string, _ model.Caller, evs []wire.InputEvent, opt control.SendOpts) ([]wire.InputAck, error) {
	f.mu.Lock()
	cg := f.connGen
	f.opts = append(f.opts, opt)
	f.mu.Unlock()
	if opt.Check != nil {
		if err := opt.Check(cg); err != nil {
			return nil, err
		}
	}
	f.mu.Lock()
	f.sends = append(f.sends, evs)
	f.mu.Unlock()
	acks := make([]wire.InputAck, len(evs))
	for i := range evs {
		acks[i] = wire.InputAck{Seq: uint64(i + 1), OK: true}
	}
	return acks, nil
}

var person = model.Caller{Actor: model.ActorHuman, ClientID: "c-cli", ClientName: "deskhand"}

func TestQAInputMapsTheVirtualDesktopAndSendsAIInput(t *testing.T) {
	a := &fakeAgent{connGen: 3}
	ctl := &fakeControl{connGen: 3}
	s := New(Options{Agents: oneAgent{a}, Control: ctl, Dir: t.TempDir()})
	bg := context.Background()

	res, err := s.Click(bg, "pc", person, -100, 50, "", 2)
	if err != nil || res["display"] != 1 || res["display_x"] != 1820 || res["display_y"] != 250 {
		t.Fatalf("click at (-100, 50): %v, %v", res, err)
	}
	if evs := ctl.sends[0]; len(evs) != 5 || evs[0].K != "move" || evs[0].Display != 1 || evs[0].X != 1820 || evs[0].Y != 250 || evs[1].B != "left" {
		t.Fatalf("events %+v", evs)
	}
	if _, err := s.Type(bg, "pc", person, "안녕", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Key(bg, "pc", person, "ctrl+s"); err != nil {
		t.Fatal(err)
	}
	for i, o := range ctl.opts {
		if !o.AsAI {
			t.Fatalf("QA input %d did not go as AI input", i)
		}
	}
	if _, err := s.Click(bg, "pc", person, 5000, 50, "", 1); apierr.CodeOf(err) != wire.CodeInvalidArgument {
		t.Fatalf("a point on no display: %v", err)
	}
	ctl.mu.Lock()
	ctl.connGen = 4
	ctl.mu.Unlock()
	sends := len(ctl.sends)
	if _, err := s.Click(bg, "pc", person, 10, 10, "", 1); apierr.CodeOf(err) != wire.CodeStaleTarget || len(ctl.sends) != sends {
		t.Fatalf("a click on a connection after the display list: %v", err)
	}
}

func TestClassifyTellsDonePartialAndNotStarted(t *testing.T) {
	click := []wire.InputEvent{{K: "move"}, {K: "button", Down: true}, {K: "button"}}
	ok := wire.InputAck{OK: true}
	elev := wire.InputAck{Code: wire.CodeElevationRequired, Error: "blocked"}
	cases := []struct {
		name    string
		evs     []wire.InputEvent
		acks    []wire.InputAck
		err     error
		status  string
		outcome string
		done    int
		next    string
	}{
		{"all acked", click, []wire.InputAck{ok, ok, ok}, nil, model.StatusSucceeded, model.OutcomeDone, 3, ""},
		{"the move ran, the press failed", click, []wire.InputAck{ok, elev, elev}, apierr.New(wire.CodeElevationRequired, "blocked", false, ""),
			model.StatusFailed, model.OutcomePartial, 1, apierr.NextCheckState},
		{"nothing ran", click, []wire.InputAck{elev, elev, elev}, apierr.New(wire.CodeElevationRequired, "blocked", false, ""),
			model.StatusFailed, model.OutcomeNotStarted, 0, ""},
		{"refused before sending", click, nil, apierr.New(wire.CodeControlHeldByHuman, "held", false, apierr.NextHumanRelease),
			model.StatusFailed, model.OutcomeNotStarted, 0, apierr.NextHumanRelease},
		{"control changed between events", click, []wire.InputAck{ok}, apierr.New(wire.CodeControlHeldByHuman, "held", false, apierr.NextHumanRelease),
			model.StatusFailed, model.OutcomePartial, 1, apierr.NextCheckState},
		{"ack lost", click, []wire.InputAck{ok}, apierr.New(wire.CodeExecutionOutcomeUnknown, "lost", false, apierr.NextCheckState),
			model.StatusUnknown, model.OutcomeUnknown, 1, apierr.NextCheckState},
		{"a text that may have typed a part", []wire.InputEvent{{K: "text"}}, []wire.InputAck{{Code: wire.CodeTimeout}},
			apierr.New(wire.CodeTimeout, "typed 3 of 9", false, ""), model.StatusFailed, model.OutcomePartial, 0, apierr.NextCheckState},
		{"a text the agent refused", []wire.InputEvent{{K: "text"}}, []wire.InputAck{{Code: wire.CodeInvalidArgument}},
			apierr.New(wire.CodeInvalidArgument, "too long", false, ""), model.StatusFailed, model.OutcomeNotStarted, 0, ""},
	}
	for _, c := range cases {
		r := classify(c.evs, c.acks, c.err)
		next := ""
		if r.Err != nil {
			next = r.Err.NextAction
		}
		if r.Status != c.status || r.Action.Outcome != c.outcome || r.Done != c.done || r.Total != len(c.evs) || next != c.next {
			t.Fatalf("%s: %+v (next %q)", c.name, r, next)
		}
		executed := r.Action.Executed
		switch c.outcome {
		case model.OutcomeDone:
			if executed == nil || !*executed {
				t.Fatalf("%s: executed %v", c.name, executed)
			}
		case model.OutcomeUnknown:
			if executed != nil {
				t.Fatalf("%s: executed %v", c.name, *executed)
			}
		default:
			if executed == nil || *executed {
				t.Fatalf("%s: executed %v", c.name, executed)
			}
		}
	}
}

func TestSettleFallsBackToWindowEventsWithoutAScreen(t *testing.T) {
	a := &fakeAgent{connGen: 1, capErr: apierr.New(wire.CodeSessionLocked, "the PC is locked (session_locked)", false, "")}
	s := New(Options{Agents: oneAgent{a}, Control: &fakeControl{}, Dir: t.TempDir(), SettleInterval: 20 * time.Millisecond})
	stop := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			select {
			case <-stop:
				return
			case <-time.After(15 * time.Millisecond):
			}
			s.HandleConnEvent(conn.Event{HostID: "pc", Kind: conn.KindAgentEvent, Payload: proto.Message{"name": "window.title"}})
		}
	}()
	start := time.Now()
	o, err := s.Observe(context.Background(), "pc", ObserveParams{Level: LevelNone, Settle: 2 * time.Second}, nil)
	close(stop)
	if err != nil {
		t.Fatal(err)
	}
	if o.Settled == nil || !*o.Settled || o.Changed != nil {
		t.Fatalf("observation %+v", o)
	}
	if took := time.Since(start); took < 5*15*time.Millisecond {
		t.Fatalf("settled after %s while window events still came", took)
	}
	if len(o.Errors) == 0 || o.Errors[0].Part != "screen" || o.Errors[0].Code != wire.CodeSessionLocked {
		t.Fatalf("errors %+v", o.Errors)
	}
}
