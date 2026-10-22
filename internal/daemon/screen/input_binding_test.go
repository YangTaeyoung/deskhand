package screen

import (
	"context"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestInputAttachVerifiesAuthoritativeIdentity(t *testing.T) {
	for _, field := range []string{"sid", "session_id", "generation", "unknown_session", "unknown_sid", "invalid_session", "offline", "unpaired"} {
		t.Run(field, func(t *testing.T) {
			r := newRig(t)
			b := testInputBinding()
			r.agent.mu.Lock()
			switch field {
			case "sid":
				b.SID = "S-1-5-21-2-1002"
			case "session_id":
				b.SessionID++
			case "generation":
				b.ConnectionGeneration++
			case "unknown_session":
				r.agent.sessionInfo = nil
			case "unknown_sid":
				delete(r.agent.sessionInfo, "sid")
			case "invalid_session":
				r.agent.sessionInfo["session_id"] = 7.5
			case "offline":
				r.agent.state = conn.StateOffline
			case "unpaired":
				r.agent.state = conn.StateBlocked
			}
			r.agent.mu.Unlock()
			v, err := r.hub.AttachInput(context.Background(), "pc", b, human)
			if v != nil {
				v.Close()
				t.Fatal("unverified attach accepted")
			}
			if apierr.CodeOf(err) != wire.CodeSessionChanged {
				t.Fatalf("attach: %v", err)
			}
			if len(r.hub.hostViewers("pc")) != 0 {
				t.Fatal("failed attach leaked viewer")
			}
			noMedia(t, r)
		})
	}
}

func TestInputAttachPreservesUnsignedIdentityAndSessionZero(t *testing.T) {
	for _, id := range []uint32{0, ^uint32(0)} {
		r := newRig(t)
		b := testInputBinding()
		b.SessionID, b.ConnectionGeneration = id, ^uint64(0)
		r.agent.mu.Lock()
		r.agent.connGen = b.ConnectionGeneration
		r.agent.sessionInfo["session_id"] = float64(id)
		r.agent.mu.Unlock()
		v, err := r.hub.AttachInput(context.Background(), "pc", b, human)
		if err != nil {
			t.Fatal(err)
		}
		v.input([]byte(`{"k":"move","x":1}`))
		eventually(t, "unsigned bound input", func() bool { return len(r.agent.inputEvents()) == 1 })
		v.Close()
	}
}

func changeInputIdentity(a *fakeAgent, field string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch field {
	case "sid":
		a.sessionInfo["sid"] = "S-1-5-21-2-1002"
	case "session_id":
		a.sessionInfo["session_id"] = float64(8)
	case "generation":
		a.connGen++
	case "offline":
		a.state = conn.StateOffline
	}
}

func TestInputIdentityEventsRevokeIdleViewersAndReleaseOldKeys(t *testing.T) {
	for _, field := range []string{"sid", "session_id", "generation", "offline"} {
		t.Run(field, func(t *testing.T) {
			r := newRig(t)
			c := r.attachInput()
			c.input(map[string]any{"k": "key", "sc": 29, "down": true})
			eventually(t, "old key", func() bool { return r.agent.heldKeys()["sc:0x1d"] })
			changeInputIdentity(r.agent, field)
			kind := events.SessionChanged
			if field == "generation" || field == "offline" {
				kind = events.ConnectionState
			}
			// The event payload is deliberately stale. Only the paired agent's
			// current SessionInfo + Generation may authorize an existing viewer.
			r.bus.Publish("pc", "", kind, map[string]any{"state": "ready", "session": proto.Message{"session_id": 7, "sid": testInputBinding().SID}})
			eventually(t, "idle input revoked", func() bool { return len(r.hub.hostViewers("pc")) == 0 })
			eventually(t, "old stream released keys", func() bool { return len(r.agent.heldKeys()) == 0 })
			if got := r.agent.inputEvents(); len(got) != 1 {
				t.Fatalf("revocation forwarded release: %+v", got)
			}
			c.close()
		})
	}
}

func TestInputSessionStatusEventKeepsSameBindingAndLegacyScreen(t *testing.T) {
	r := newRig(t)
	legacy := r.attach(0, PresetSharp, human)
	c := r.attachInput()
	r.bus.Publish("pc", "", events.SessionChanged, map[string]any{"session": proto.Message{"locked": true}})
	// Ordered notice makes the preceding session event observable.
	r.bus.Publish("pc", "", events.ControlChanged, map[string]any{"owner": "none"})
	c.notice("owner_changed")
	if len(r.hub.hostViewers("pc")) != 2 {
		t.Fatal("status-only event revoked viewer")
	}
	c.input(map[string]any{"k": "move", "x": 1})
	eventually(t, "same binding input", func() bool { return len(r.agent.inputEvents()) == 1 })
	changeInputIdentity(r.agent, "generation")
	r.bus.Publish("pc", "", events.ConnectionState, map[string]any{"state": "ready"})
	eventually(t, "only legacy viewer survives", func() bool { return len(r.hub.hostViewers("pc")) == 1 })
	legacy.input(map[string]any{"k": "move", "x": 2})
	eventually(t, "legacy input still reconnects", func() bool { return len(r.agent.inputEvents()) == 2 })
}

func TestStaleInputViewerCannotReleaseOrCloseReplacementLink(t *testing.T) {
	for _, field := range []string{"sid", "session_id", "generation"} {
		for _, action := range []string{"input", "release_all", "disconnect"} {
			t.Run(field+"/"+action, func(t *testing.T) {
				r := newRig(t)
				old, err := r.hub.AttachInput(context.Background(), "pc", testInputBinding(), human)
				if err != nil {
					t.Fatal(err)
				}
				old.input([]byte(`{"k":"key","sc":29,"down":true}`))
				eventually(t, "old held key", func() bool { return r.agent.heldKeys()["sc:0x1d"] })
				changeInputIdentity(r.agent, field)
				b, ok := currentInputBinding(r.agent)
				if !ok {
					t.Fatal("new binding unavailable")
				}
				next, err := r.hub.AttachInput(context.Background(), "pc", b, human)
				if err != nil {
					t.Fatal(err)
				}
				next.input([]byte(`{"k":"key","sc":30,"down":true}`))
				eventually(t, "replacement key held", func() bool { return r.agent.heldKeys()["sc:0x1e"] })
				switch action {
				case "input":
					old.input([]byte(`{"k":"key","sc":31,"down":true}`))
				case "release_all":
					old.input([]byte(`{"k":"release_all"}`))
				case "disconnect":
					old.Close()
				}
				if !r.agent.heldKeys()["sc:0x1e"] {
					t.Fatal("old viewer released replacement keys")
				}
				r.agent.mu.Lock()
				if len(r.agent.inputs) != 2 || r.agent.inputs[1].ended || len(r.agent.inputs[1].events) != 1 {
					t.Error("old viewer wrote to or closed replacement stream")
				}
				r.agent.mu.Unlock()
				eventually(t, "old key released", func() bool { return !r.agent.heldKeys()["sc:0x1d"] })
				next.Close()
			})
		}
	}
}

// queuedInputControl signals after arbitration but before the input link's
// mutex. Tests hold that mutex to queue the actual forward/release operation.
type queuedInputControl struct {
	Control
	entered chan struct{}
}

func (c queuedInputControl) signal() {
	select {
	case c.entered <- struct{}{}:
	default:
	}
}
func (c queuedInputControl) Get(host string) model.ControlState {
	s := c.Control.Get(host)
	c.signal()
	return s
}
func (c queuedInputControl) CheckHuman(host string, caller model.Caller) (uint64, error) {
	gen, err := c.Control.CheckHuman(host, caller)
	c.signal()
	return gen, err
}

func TestQueuedInputAndReleaseRecheckBinding(t *testing.T) {
	for _, field := range []string{"sid", "session_id", "generation"} {
		for _, action := range []string{"input", "release_all", "disconnect", "hub_close"} {
			t.Run(field+"/"+action, func(t *testing.T) {
				r := newRig(t)
				v, err := r.hub.AttachInput(context.Background(), "pc", testInputBinding(), human)
				if err != nil {
					t.Fatal(err)
				}
				v.input([]byte(`{"k":"key","sc":29,"down":true}`))
				eventually(t, "first held input", func() bool { return r.agent.heldKeys()["sc:0x1d"] })
				entered := make(chan struct{}, 1)
				r.hub.opt.Control = queuedInputControl{Control: r.ctl, entered: entered}
				l := r.hub.link("pc")
				l.mu.Lock()
				done := make(chan struct{})
				go func() {
					defer close(done)
					switch action {
					case "input":
						v.input([]byte(`{"k":"key","sc":30,"down":true}`))
					case "release_all":
						v.input([]byte(`{"k":"release_all"}`))
					case "disconnect":
						v.Close()
					case "hub_close":
						r.hub.Close()
					}
				}()
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					l.mu.Unlock()
					t.Fatal("input did not reach forwarding queue")
				}
				changeInputIdentity(r.agent, field)
				l.mu.Unlock()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("queued operation did not finish")
				}
				eventually(t, "old input stream ended", func() bool {
					r.agent.mu.Lock()
					defer r.agent.mu.Unlock()
					return len(r.agent.inputs) == 1 && r.agent.inputs[0].ended
				})
				if got := r.agent.inputEvents(); len(got) != 1 {
					t.Fatalf("queued event migrated: %+v", got)
				}
				eventually(t, "old keys released", func() bool { return len(r.agent.heldKeys()) == 0 })
				if len(r.hub.hostViewers("pc")) != 0 {
					t.Fatal("queued operation left stale viewer authorized")
				}
			})
		}
	}
}

// inputRaceAgent can reconnect inside OpenStream, or change identity at the
// write deadline immediately before a frame is forwarded on an existing link.
type inputRaceAgent struct {
	*fakeAgent
	beforeOpen         func()
	beforeWrite        func()
	returnedGeneration *uint64
}

func (a *inputRaceAgent) OpenStream(ctx context.Context, h wire.StreamHeader) (conn.DataStream, uint64, error) {
	if a.beforeOpen != nil {
		a.beforeOpen()
	}
	st, gen, err := a.fakeAgent.OpenStream(ctx, h)
	if err != nil {
		return st, gen, err
	}
	if a.returnedGeneration != nil {
		gen = *a.returnedGeneration
	}
	return inputRaceStream{DataStream: st, beforeWrite: a.beforeWrite}, gen, nil
}

type inputRaceStream struct {
	conn.DataStream
	beforeWrite func()
}

func (s inputRaceStream) SetWriteDeadline(t time.Time) error {
	if s.beforeWrite != nil {
		s.beforeWrite()
	}
	return s.DataStream.SetWriteDeadline(t)
}

type inputRaceAgents struct{ a Agent }

func (a inputRaceAgents) Agent(string) Agent { return a.a }

func TestInputForwardChecksOpenedAndCurrentGeneration(t *testing.T) {
	for _, stage := range []string{"reconnect_inside_open", "wrong_opened_generation", "before_write"} {
		t.Run(stage, func(t *testing.T) {
			r := newRig(t)
			a := &inputRaceAgent{fakeAgent: r.agent}
			r.hub.opt.Agents = inputRaceAgents{a: a}
			v, err := r.hub.AttachInput(context.Background(), "pc", testInputBinding(), human)
			if err != nil {
				t.Fatal(err)
			}
			switch stage {
			case "reconnect_inside_open":
				a.beforeOpen = func() { changeInputIdentity(r.agent, "generation") }
			case "wrong_opened_generation":
				gen := uint64(2)
				a.returnedGeneration = &gen
			case "before_write":
				a.beforeWrite = func() { changeInputIdentity(r.agent, "generation") }
			}
			v.input([]byte(`{"k":"key","sc":29,"down":true}`))
			eventually(t, "empty raced stream ended", func() bool {
				r.agent.mu.Lock()
				defer r.agent.mu.Unlock()
				return len(r.agent.inputs) == 1 && r.agent.inputs[0].ended
			})
			if got := r.agent.inputEvents(); len(got) != 0 {
				t.Fatalf("input crossed generation race: %+v", got)
			}
			if len(r.hub.hostViewers("pc")) != 0 {
				t.Fatal("raced viewer not revoked")
			}
		})
	}
}

func TestHubCloseAndRevocationWhileInputOpenIsPending(t *testing.T) {
	r := newRig(t)
	v, err := r.hub.AttachInput(context.Background(), "pc", testInputBinding(), human)
	if err != nil {
		t.Fatal(err)
	}
	accepted, proceed := make(chan struct{}), make(chan struct{})
	r.agent.mu.Lock()
	r.agent.inputHook = func(stage string) {
		if stage == "accepted" {
			close(accepted)
			<-proceed
		}
	}
	r.agent.mu.Unlock()
	sent := make(chan struct{})
	go func() { defer close(sent); v.input([]byte(`{"k":"key","sc":29,"down":true}`)) }()
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		close(proceed)
		t.Fatal("upstream did not reach acceptance")
	}
	changeInputIdentity(r.agent, "generation")
	revoked, closed := make(chan struct{}), make(chan struct{})
	go func() { defer close(revoked); r.hub.event(events.Event{HostID: "pc", Kind: events.ConnectionState}) }()
	go func() { defer close(closed); r.hub.Close() }()
	close(proceed)
	for _, done := range []chan struct{}{sent, revoked, closed} {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("close/revocation deadlocked with pending open")
		}
	}
	if got := r.agent.inputEvents(); len(got) != 0 {
		t.Fatalf("pending input forwarded during close: %+v", got)
	}
	if len(r.hub.hostViewers("pc")) != 0 {
		t.Fatal("viewer survived close/revocation")
	}
}
