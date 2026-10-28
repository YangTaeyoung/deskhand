package screen

import (
	"context"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/control"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func (r *rig) attachInput() *viewerClient {
	r.t.Helper()
	v, err := r.hub.AttachInput(context.Background(), "pc", testInputBinding(), human)
	if err != nil {
		r.t.Fatal(err)
	}
	daemonEnd, clientEnd := duplex()
	c := &viewerClient{t: r.t, end: clientEnd, frames: make(chan clientFrame, 32), ran: make(chan struct{})}
	go func() { defer close(c.ran); v.Run(daemonEnd) }()
	c.startReading()
	r.t.Cleanup(c.close)
	return c
}

func noMedia(t *testing.T, r *rig) {
	t.Helper()
	r.agent.mu.Lock()
	defer r.agent.mu.Unlock()
	if len(r.agent.videos) != 0 || len(r.agent.cursors) != 0 || r.agent.syncs != 0 {
		t.Fatalf("input viewer opened media: video=%d cursor=%d sync=%d", len(r.agent.videos), len(r.agent.cursors), r.agent.syncs)
	}
	r.hub.mu.Lock()
	defer r.hub.mu.Unlock()
	if len(r.hub.feeds) != 0 {
		t.Fatal("input viewer allocated a display feed")
	}
}

func TestInputViewerNoMediaAndDisconnectReleasesKeys(t *testing.T) {
	for _, hubClose := range []bool{false, true} {
		name := "disconnect"
		if hubClose {
			name = "Hub.Close"
		}
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			c := r.attachInput()
			noMedia(t, r)
			if len(r.hub.hostViewers("pc")) != 1 {
				t.Fatal("input viewer is missing from hub lifecycle")
			}
			c.send(wire.KindClientControl, map[string]any{"op": "keyframe"})
			c.send(wire.KindClientControl, map[string]any{"op": "preset", "preset": "sharp"})
			c.input(map[string]any{"k": "key", "sc": 0x1d, "down": true, "gen": 999, "seq": 999})
			eventually(t, "held key", func() bool { return r.agent.heldKeys()["sc:0x1d"] })
			event := r.agent.inputEvents()[0]
			if event.Gen != r.ctl.HumanGen("pc") || event.Seq != 1 {
				t.Fatalf("untrusted seq/gen: %+v", event)
			}
			if hubClose {
				r.hub.Close()
			} else {
				c.close()
			}
			eventually(t, "key release", func() bool { return len(r.agent.heldKeys()) == 0 })
			eventually(t, "last input upstream closed", func() bool {
				r.agent.mu.Lock()
				defer r.agent.mu.Unlock()
				return len(r.agent.inputs) == 1 && r.agent.inputs[0].ended
			})
			if len(r.hub.hostViewers("pc")) != 0 {
				t.Fatal("closed input viewer leaked")
			}
			evs := r.agent.inputEvents()
			if evs[len(evs)-1].K != "release_all" {
				t.Fatalf("missing explicit key release: %+v", evs)
			}
			noMedia(t, r)
		})
	}
}

func TestInputViewerAIAndAttachFailuresLeaveNoViewers(t *testing.T) {
	r := newRig(t)
	if _, err := r.hub.AttachInput(context.Background(), "unknown", testInputBinding(), aiA); apierr.CodeOf(err) != wire.CodePermissionDenied {
		t.Fatalf("AI: %v", err)
	}
	if _, err := r.hub.AttachInput(context.Background(), "unknown", testInputBinding(), human); apierr.CodeOf(err) != wire.CodeNotFound {
		t.Fatalf("host: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.hub.AttachInput(ctx, "pc", testInputBinding(), human); err != context.Canceled {
		t.Fatalf("cancelled attach: %v", err)
	}
	v, err := r.hub.AttachInput(context.Background(), "pc", testInputBinding(), human)
	if err != nil {
		t.Fatal(err)
	}
	// The local acceptance could not be delivered; Run has not started.
	v.Close()
	if len(r.hub.hostViewers("pc")) != 0 {
		t.Fatal("failed handshake leaked its viewer")
	}
	v, err = r.hub.AttachInput(context.Background(), "pc", testInputBinding(), human)
	if err != nil {
		t.Fatal(err)
	}
	daemonEnd, clientEnd := duplex()
	clientEnd.Close()
	v.Run(daemonEnd)
	if len(r.hub.hostViewers("pc")) != 0 {
		t.Fatal("closed handshake socket leaked its viewer")
	}
	r.hub.Close()
	if _, err := r.hub.AttachInput(context.Background(), "pc", testInputBinding(), human); err != errClosed {
		t.Fatalf("closed hub: %v", err)
	}
	noMedia(t, r)
}

func TestInputViewerOwnershipAndGenerationCheckedAfterOpen(t *testing.T) {
	for _, duringOpen := range []bool{false, true} {
		t.Run(map[bool]string{false: "AI already owns", true: "AI takes while opening"}[duringOpen], func(t *testing.T) {
			r := newRig(t)
			c := r.attachInput()
			takeAI := func() {
				if duringOpen {
					if _, err := r.ctl.Release(context.Background(), "pc", human); err != nil {
						t.Error(err)
					}
				}
				if _, err := r.ctl.Acquire(context.Background(), "pc", control.OwnerAI, aiA, false); err != nil {
					t.Error(err)
				}
			}
			if duringOpen {
				r.agent.mu.Lock()
				r.agent.inputHook = func(stage string) {
					if stage == "accepted" {
						takeAI()
					}
				}
				r.agent.mu.Unlock()
			} else {
				takeAI()
			}
			c.input(map[string]any{"k": "key", "sc": 0x1d, "down": true})
			if n := c.notice("control_denied"); n["owner"] != "ai" || n["owner_client"] != aiA.ClientName {
				t.Fatalf("denial: %v", n)
			}
			if len(r.agent.inputEvents()) != 0 {
				t.Fatal("input crossed AI ownership")
			}
			noMedia(t, r)
		})
	}
}

func TestInputViewerEventsAndOfflineInput(t *testing.T) {
	r := newRig(t)
	c := r.attachInput()
	r.bus.Publish("pc", "", events.ConnectionState, map[string]any{"state": "degraded"})
	if n := c.notice("connection_state"); n["state"] != "degraded" {
		t.Fatalf("connection notice: %v", n)
	}
	r.agent.setState(conn.StateOffline)
	c.input(map[string]any{"k": "key", "sc": 0x1d, "down": true})
	// An input-only viewer loses authorization when its connection is gone.
	eventually(t, "offline viewer revoked", func() bool { return len(r.hub.hostViewers("pc")) == 0 })
	if len(r.agent.inputEvents()) != 0 || r.ctl.Get("pc").Owner != "none" {
		t.Fatal("offline input acquired control or reached agent")
	}
	noMedia(t, r)
}

func TestInputViewerSharesLinkWithScreenViewer(t *testing.T) {
	r := newRig(t)
	screen := r.attach(0, PresetSharp, human)
	input := r.attachInput()
	input.input(map[string]any{"k": "move", "x": 1, "y": 2})
	eventually(t, "first input", func() bool { return len(r.agent.inputEvents()) == 1 })
	input.close()
	screen.input(map[string]any{"k": "move", "x": 3, "y": 4})
	eventually(t, "screen input", func() bool { return len(r.agent.inputEvents()) == 2 })
	r.agent.mu.Lock()
	defer r.agent.mu.Unlock()
	if len(r.agent.inputs) != 1 || r.agent.inputs[0].ended {
		t.Fatal("input detach closed another viewer's link")
	}
}

// No bus event is delivered here: queued input and detach must enforce the
// authoritative identity themselves, before the app can react.
func TestInputViewerRejectsChangedIdentityWithoutEvent(t *testing.T) {
	for _, field := range []string{"sid", "session_id", "generation"} {
		for _, action := range []string{"input", "release_all", "disconnect", "hub_close"} {
			t.Run(field+"/"+action, func(t *testing.T) {
				r := newRig(t)
				v, err := r.hub.AttachInput(context.Background(), "pc", testInputBinding(), human)
				if err != nil {
					t.Fatal(err)
				}
				v.input([]byte(`{"k":"key","sc":29,"down":true}`))
				eventually(t, "old key held", func() bool { return r.agent.heldKeys()["sc:0x1d"] })
				r.agent.mu.Lock()
				switch field {
				case "sid":
					r.agent.sessionInfo["sid"] = "S-1-5-21-2-1002"
				case "session_id":
					r.agent.sessionInfo["session_id"] = float64(8)
				case "generation":
					r.agent.connGen++
				}
				r.agent.mu.Unlock()
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
				if got := r.agent.inputEvents(); len(got) != 1 {
					t.Fatalf("old viewer forwarded into changed identity: %+v", got)
				}
				eventually(t, "old upstream closed", func() bool {
					r.agent.mu.Lock()
					defer r.agent.mu.Unlock()
					return len(r.agent.inputs) == 1 && r.agent.inputs[0].ended
				})
				eventually(t, "old held key released", func() bool { return len(r.agent.heldKeys()) == 0 })
				if len(r.hub.hostViewers("pc")) != 0 {
					t.Fatal("changed input viewer was not revoked")
				}
			})
		}
	}
}

func TestInputViewerIdentityChangesDuringOpen(t *testing.T) {
	for _, field := range []string{"sid", "session_id", "generation"} {
		t.Run(field, func(t *testing.T) {
			r := newRig(t)
			v, err := r.hub.AttachInput(context.Background(), "pc", testInputBinding(), human)
			if err != nil {
				t.Fatal(err)
			}
			r.agent.mu.Lock()
			r.agent.inputHook = func(stage string) {
				if stage != "accepted" {
					return
				}
				r.agent.mu.Lock()
				defer r.agent.mu.Unlock()
				switch field {
				case "sid":
					r.agent.sessionInfo["sid"] = "S-1-5-21-2-1002"
				case "session_id":
					r.agent.sessionInfo["session_id"] = float64(8)
				case "generation":
					r.agent.connGen++
				}
			}
			r.agent.mu.Unlock()
			v.input([]byte(`{"k":"key","sc":29,"down":true}`))
			if got := r.agent.inputEvents(); len(got) != 0 {
				t.Fatalf("input crossed identity change during open: %+v", got)
			}
			eventually(t, "revoked after open", func() bool { return len(r.hub.hostViewers("pc")) == 0 })
		})
	}
}

func testInputBinding() InputBinding {
	return InputBinding{SessionID: 7, SID: "S-1-5-21-1-1001", ConnectionGeneration: 1}
}

func TestHumanActivityIsNotForwardedOrTreatedAsAIControl(t *testing.T) {
	r := newRig(t)
	c := r.attachInput()
	c.input(map[string]any{"k": "human_activity", "down": true})
	c.input(map[string]any{"k": "human_activity", "down": false})
	c.input(map[string]any{"k": "move", "x": 1, "y": 2})
	eventually(t, "only real input reached agent", func() bool { return len(r.agent.inputEvents()) == 1 })
	if ev := r.agent.inputEvents()[0]; ev.K != "move" {
		t.Fatalf("activity forwarded to agent: %+v", ev)
	}
}

func TestDeniedInputDoesNotLeaveAKeyHeldInViewer(t *testing.T) {
	r := newRig(t)
	if _, err := r.ctl.Acquire(context.Background(), "pc", control.OwnerAI, aiA, false); err != nil {
		t.Fatal(err)
	}
	c := r.attachInput()
	c.input(map[string]any{"k": "key", "sc": 30, "down": true})
	c.notice("control_denied")
	viewers := r.hub.hostViewers("pc")
	if len(viewers) != 1 || viewers[0].holds() {
		t.Fatalf("denied input remains held: %v", viewers)
	}
}

func TestNewWriterGenerationDoesNotRetainOldHeldKeys(t *testing.T) {
	r := newRig(t)
	c := r.attachInput()
	c.input(map[string]any{"k": "key", "sc": 30, "down": true})
	eventually(t, "held key sent", func() bool { return len(r.agent.heldKeys()) > 0 })
	if _, err := r.ctl.Stop(context.Background(), "pc", human); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ctl.ResumeAI(context.Background(), "pc", human); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ctl.Acquire(context.Background(), "pc", control.OwnerHuman, human, false); err != nil {
		t.Fatal(err)
	}
	c.input(map[string]any{"k": "move", "x": 1, "y": 2})
	eventually(t, "new generation input", func() bool { return len(r.agent.inputEvents()) >= 2 })
	viewers := r.hub.hostViewers("pc")
	if len(viewers) != 1 || viewers[0].holds() {
		t.Fatal("old generation key remains held")
	}
}

func TestIdleViewerReleaseAllDoesNotReleaseAnotherViewersHeldKey(t *testing.T) {
	r := newRig(t)
	a := r.attachInput()
	other := human
	other.ClientID = "other-viewer"
	v, err := r.hub.AttachInput(context.Background(), "pc", testInputBinding(), other)
	if err != nil {
		t.Fatal(err)
	}
	daemonEnd, clientEnd := duplex()
	b := &viewerClient{t: t, end: clientEnd, frames: make(chan clientFrame, 32), ran: make(chan struct{})}
	go func() { defer close(b.ran); v.Run(daemonEnd) }()
	b.startReading()
	t.Cleanup(b.close)
	b.input(map[string]any{"k": "key", "sc": 30, "down": true})
	eventually(t, "other viewer held key", func() bool { return len(r.agent.heldKeys()) == 1 })
	a.input(map[string]any{"k": "release_all"})
	time.Sleep(150 * time.Millisecond)
	if len(r.agent.heldKeys()) != 1 {
		t.Fatal("idle viewer released another viewer's key")
	}
}

func TestStaleViewerReleaseAllDoesNotReleaseCurrentGenerationKeys(t *testing.T) {
	r := newRig(t)
	a := r.attachInput()
	a.input(map[string]any{"k": "key", "sc": 30, "down": true})
	eventually(t, "first held key", func() bool { return len(r.agent.heldKeys()) == 1 })
	if _, err := r.ctl.Stop(context.Background(), "pc", human); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ctl.ResumeAI(context.Background(), "pc", human); err != nil {
		t.Fatal(err)
	}
	b := r.attachInput()
	b.input(map[string]any{"k": "key", "sc": 31, "down": true})
	eventually(t, "new held key", func() bool { return r.agent.heldKeys()["sc:0x1f"] })
	a.input(map[string]any{"k": "release_all"})
	time.Sleep(150 * time.Millisecond)
	if !r.agent.heldKeys()["sc:0x1f"] {
		t.Fatal("stale viewer released current writer's key")
	}
}

func TestResyncedInputCanReleaseItsHeldKey(t *testing.T) {
	r := newRig(t)
	c := r.attachInput()
	r.agent.mu.Lock()
	r.agent.inputHook = func(stage string) {
		if stage == "open" {
			r.agent.mu.Lock()
			r.agent.raiseLocked(10)
			r.agent.inputHook = nil
			r.agent.mu.Unlock()
		}
	}
	r.agent.mu.Unlock()
	c.input(map[string]any{"k": "key", "sc": 30, "down": true})
	eventually(t, "resynced key down", func() bool { return r.agent.heldKeys()["sc:0x1e"] })
	c.input(map[string]any{"k": "release_all"})
	eventually(t, "resynced key released", func() bool { return len(r.agent.heldKeys()) == 0 })
}

func TestSameClientViewersKeepIndependentPhysicalActivity(t *testing.T) {
	r := newRig(t)
	a := r.attachInput()
	r.attachInput()
	a.input(map[string]any{"k": "move", "x": 1, "y": 2})
	eventually(t, "human owns control", func() bool { return r.ctl.Get("pc").Owner == string(control.OwnerHuman) })
	viewers := r.hub.hostViewers("pc")
	viewers[0].input([]byte(`{"k":"human_activity","down":true}`))
	viewers[1].input([]byte(`{"k":"human_activity","down":false}`))
	time.Sleep(5200 * time.Millisecond)
	state := r.ctl.Get("pc")
	got, err := r.ctl.ReleaseIfHuman(context.Background(), "pc", human, state.Gen)
	if err != nil || got.Owner != string(control.OwnerHuman) {
		t.Fatalf("other viewer cleared held input: %+v %v", got, err)
	}
	viewers[1].Close()
	got, err = r.ctl.ReleaseIfHuman(context.Background(), "pc", human, state.Gen)
	if err != nil || got.Owner != string(control.OwnerHuman) {
		t.Fatalf("other viewer detach cleared held input: %+v %v", got, err)
	}
}
