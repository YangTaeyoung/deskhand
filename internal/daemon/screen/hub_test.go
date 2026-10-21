package screen

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/control"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestTwoViewersShareOneAgentStream(t *testing.T) {
	r := newRig(t)
	a := r.attach(0, PresetSharp, human)
	b := r.attach(0, PresetSharp, human2)
	if n := r.agent.videoCount(); n != 1 {
		t.Fatalf("%d agent video streams for two viewers of one display, want 1", n)
	}
	v := r.agent.video(0)
	v.au(true, 1)
	v.au(false, 2)
	for name, c := range map[string]*viewerClient{"a": a, "b": b} {
		if au := c.nextAU(); au.Seq != 1 || !au.Key {
			t.Fatalf("viewer %s: first access unit seq %d key %v, want the keyframe 1", name, au.Seq, au.Key)
		}
		if au := c.nextAU(); au.Seq != 2 {
			t.Fatalf("viewer %s: second access unit seq %d, want 2", name, au.Seq)
		}
	}
	a.close()
	v.au(false, 3)
	if au := b.nextAU(); au.Seq != 3 {
		t.Fatalf("viewer b after a left: access unit seq %d, want 3", au.Seq)
	}
	if v.isEnded() || r.agent.videoCount() != 1 {
		t.Fatalf("the agent stream ended (%v) or another opened (%d) while viewer b watches", v.isEnded(), r.agent.videoCount())
	}
}

func TestNewViewerGetsKeyframe(t *testing.T) {
	r := newRig(t)
	a := r.attach(0, PresetBalanced, human)
	v := r.agent.video(0)
	v.au(true, 1)
	v.au(false, 2)
	if au := a.nextAU(); au.Seq != 1 {
		t.Fatalf("viewer a: seq %d", au.Seq)
	}
	a.nextAU()
	v.drainControls()
	b := r.attach(0, PresetBalanced, human2)
	v.control(t, wire.ControlKeyframe)
	if f := b.next(); f.kind != wire.KindVideoInfo || !bytes.Equal(f.payload, infoPayload(1920, 1080, 0)) {
		t.Fatalf("the new viewer's first frame is %#x %s, want the stream's info", f.kind, f.payload)
	}
	v.au(false, 3)
	v.au(true, 4)
	if au := b.nextAU(); au.Seq != 4 || !au.Key {
		t.Fatalf("the new viewer's first access unit is seq %d key %v, want the keyframe 4", au.Seq, au.Key)
	}
	if au := a.nextAU(); au.Seq != 3 {
		t.Fatalf("viewer a: seq %d, want 3", au.Seq)
	}
}

func TestPausedForwardedAndResumeStartsWithKeyframe(t *testing.T) {
	r := newRig(t)
	a := r.attach(0, PresetSharp, human)
	v := r.agent.video(0)
	v.au(true, 1)
	a.nextAU()
	v.paused("secure_desktop")
	if f := a.nextOf(wire.KindVideoPaused); string(f.payload) != `{"reason":"secure_desktop"}` {
		t.Fatalf("paused frame %s", f.payload)
	}
	b := r.attach(0, PresetSharp, human2)
	if f := b.next(); f.kind != wire.KindVideoInfo {
		t.Fatalf("the viewer that attached while paused got %#x first, want info", f.kind)
	}
	if f := b.next(); f.kind != wire.KindVideoPaused || string(f.payload) != `{"reason":"secure_desktop"}` {
		t.Fatalf("the viewer that attached while paused got %#x %s second, want the paused frame", f.kind, f.payload)
	}
	v.info()
	v.au(false, 2)
	v.au(true, 3)
	for name, c := range map[string]*viewerClient{"a": a, "b": b} {
		c.nextOf(wire.KindVideoInfo)
		if au := c.nextAU(); au.Seq != 3 || !au.Key {
			t.Fatalf("viewer %s: first access unit after the pause is seq %d key %v, want the keyframe 3", name, au.Seq, au.Key)
		}
	}
}

func TestHumanInputBlockedWhileAIOwns(t *testing.T) {
	r := newRig(t)
	if _, err := r.ctl.Acquire(context.Background(), "pc", control.OwnerAI, aiA, false); err != nil {
		t.Fatal(err)
	}
	a := r.attach(0, PresetSharp, human)
	a.input(map[string]any{"seq": 1, "gen": 0, "k": "key", "sc": 0x1E, "ext": false, "down": true})
	n := a.notice("control_denied")
	if n["owner"] != "ai" || n["owner_client"] != aiA.ClientName {
		t.Fatalf("control_denied %v", n)
	}
	time.Sleep(50 * time.Millisecond)
	if evs := r.agent.inputEvents(); len(evs) != 0 {
		t.Fatalf("the agent got %d input events while the AI holds control: %+v", len(evs), evs)
	}
	if s := r.ctl.Get("pc"); s.Owner != "ai" {
		t.Fatalf("control after the denied input %+v", s)
	}
}

func TestHumanInputAutoAcquires(t *testing.T) {
	r := newRig(t)
	a := r.attach(0, PresetSharp, human)
	a.input(map[string]any{"seq": 7, "gen": 0, "k": "button", "b": "left", "down": true})
	eventually(t, "the click at the agent", func() bool { return len(r.agent.inputEvents()) == 1 })
	s := r.ctl.Get("pc")
	if s.Owner != "human" || s.OwnerClient != human.ClientName || s.Gen == 0 {
		t.Fatalf("control after the first human input %+v, want the viewer's human control", s)
	}
	ev := r.agent.inputEvents()[0]
	if ev.K != "button" || ev.B != "left" || !ev.Down || ev.Gen != s.Gen || ev.Seq != 1 {
		t.Fatalf("event at the agent %+v, want the click with gen %d and the stream's seq 1", ev, s.Gen)
	}
	eventually(t, "the ack", func() bool { return len(r.agent.inputAcks()) == 1 })
	if ack := r.agent.inputAcks()[0]; !ack.OK {
		t.Fatalf("ack %+v", ack)
	}
}

func TestHumanInputAfterTakeoverCarriesTheNewGen(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	a := r.attach(0, PresetSharp, human)
	a.input(map[string]any{"seq": 1, "gen": 0, "k": "move", "x": 10, "y": 20, "display": 0})
	eventually(t, "the first move", func() bool { return len(r.agent.inputEvents()) == 1 })
	if _, err := r.ctl.Release(ctx, "pc", human); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ctl.Acquire(ctx, "pc", control.OwnerAI, aiA, false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ctl.Acquire(ctx, "pc", control.OwnerHuman, cli, true); err != nil {
		t.Fatal(err)
	}
	gen := r.ctl.HumanGen("pc")
	a.input(map[string]any{"seq": 2, "gen": 0, "k": "move", "x": 11, "y": 21, "display": 0})
	eventually(t, "the move after the takeover", func() bool { return len(r.agent.inputAcks()) == 2 })
	evs, acks := r.agent.inputEvents(), r.agent.inputAcks()
	if evs[1].Gen != gen || !acks[1].OK {
		t.Fatalf("event %+v ack %+v after the takeover, want gen %d accepted", evs[1], acks[1], gen)
	}
}

func TestViewerDetachReleasesKeys(t *testing.T) {
	r := newRig(t)
	a := r.attach(0, PresetSharp, human)
	b := r.attach(0, PresetSharp, human2)
	a.input(map[string]any{"seq": 1, "gen": 0, "k": "key", "sc": 0x1D, "ext": false, "down": true})
	eventually(t, "the held key", func() bool { return r.agent.heldKeys()["sc:0x1d"] })
	a.close()
	eventually(t, "the release", func() bool { return len(r.agent.heldKeys()) == 0 })
	evs := r.agent.inputEvents()
	last := evs[len(evs)-1]
	if last.K != "release_all" || last.Gen != r.ctl.HumanGen("pc") {
		t.Fatalf("last event at the agent %+v, want release_all of gen %d", last, r.ctl.HumanGen("pc"))
	}
	v := r.agent.video(0)
	v.au(true, 1)
	if au := b.nextAU(); au.Seq != 1 {
		t.Fatalf("the other viewer: seq %d", au.Seq)
	}
}

func TestLatencyNoticeUsesClockOffset(t *testing.T) {
	clock := newFakeClock(5_000_000)
	const offset = 7_000_000_000
	// Each time.sync sample is (wait before the agent reads its clock, wait
	// after); only the third has a symmetric and the smallest round trip.
	samples := [][2]int64{{9000, 1000}, {500, 4500}, {800, 800}, {6000, 0}, {2000, 9000}}
	r := newRig(t, func(o *Options) {
		o.Clock = clock.now
		o.LatencyEvery = 30 * time.Millisecond
	})
	i := 0
	r.agent.mu.Lock()
	r.agent.timeSync = func() uint64 {
		s := samples[i%len(samples)]
		i++
		clock.advance(s[0])
		mono := clock.now() + offset
		clock.advance(s[1])
		return uint64(mono)
	}
	r.agent.mu.Unlock()
	a := r.attach(0, PresetSharp, human)
	v := r.agent.video(0)
	eventually(t, "five time.sync samples", func() bool { return r.agent.syncCount() >= 5 })
	got := make(chan map[string]any, 1)
	go func() {
		defer close(got)
		for f := range a.frames {
			var m map[string]any
			if f.kind == wire.KindNotice && json.Unmarshal(f.payload, &m) == nil && m["type"] == "latency" {
				got <- m
				return
			}
		}
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	timeout := time.After(5 * time.Second)
	seq := uint64(1)
	for {
		select {
		case n := <-got:
			if ms, _ := n["capture_to_receive_ms"].(float64); math.Abs(ms-20) > 1e-6 {
				t.Fatalf("capture_to_receive_ms %v, want 20", n["capture_to_receive_ms"])
			}
			if ms, _ := n["clock_offset_ms"].(float64); math.Abs(ms-offset/1000) > 1e-6 {
				t.Fatalf("clock_offset_ms %v, want %d", n["clock_offset_ms"], offset/1000)
			}
			if r.agent.syncCount() != 5 {
				t.Fatalf("%d time.sync calls on one connection, want 5", r.agent.syncCount())
			}
			return
		case <-tick.C:
			v.auAt(seq == 1, seq, uint64(clock.now()+offset-20_000))
			seq++
		case <-timeout:
			t.Fatal("no latency notice within 5 s")
		}
	}
}

func TestIdleAgentStreamClosedAfterGrace(t *testing.T) {
	const grace = 300 * time.Millisecond
	r := newRig(t, func(o *Options) { o.Grace = grace })
	a := r.attach(0, PresetSharp, human)
	v := r.agent.video(0)
	a.close()
	b := r.attach(0, PresetSharp, human2)
	if n := r.agent.videoCount(); n != 1 || v.isEnded() {
		t.Fatalf("a viewer that attached within the grace got %d agent streams (first ended %v), want the first one", n, v.isEnded())
	}
	b.close()
	left := time.Now()
	select {
	case <-v.ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent stream without viewers was not closed")
	}
	if d := time.Since(left); d < grace-50*time.Millisecond {
		t.Fatalf("the agent stream was closed %s after its last viewer left, before the %s grace", d, grace)
	}
	c := r.attach(0, PresetSharp, human)
	if n := r.agent.videoCount(); n != 2 {
		t.Fatalf("%d agent streams after a viewer attached past the grace, want a second one", n)
	}
	r.agent.video(1).au(true, 1)
	if au := c.nextAU(); au.Seq != 1 {
		t.Fatalf("seq %d", au.Seq)
	}
}

func TestSlowViewerDoesNotStallTheStreamOrOtherViewers(t *testing.T) {
	r := newRig(t, func(o *Options) { o.QueueFrames = 8 })
	slow := r.attachSlow(0, PresetSharp, human)
	v := r.agent.video(0)
	fast := r.attach(0, PresetSharp, human2)
	v.control(t, wire.ControlKeyframe)
	for seq := uint64(1); seq <= 100; seq++ {
		v.au(seq == 1, seq)
		if au := fast.nextAU(); au.Seq != seq {
			t.Fatalf("the reading viewer got seq %d, want %d", au.Seq, seq)
		}
	}
	slow.startReading()
	v.control(t, wire.ControlKeyframe)
	v.au(true, 101)
	prev := uint64(0)
	for {
		au := slow.nextAU()
		if au.Seq == prev+1 {
			prev = au.Seq
			continue
		}
		if au.Seq != 101 || !au.Key {
			t.Fatalf("the slow viewer resumed at seq %d key %v after seq %d, want the keyframe 101", au.Seq, au.Key, prev)
		}
		break
	}
	if prev == 0 || prev >= 100 {
		t.Fatalf("the slow viewer got seq 1–%d before its gap", prev)
	}
}

func TestOwnerChangedPushedToEveryViewer(t *testing.T) {
	r := newRig(t)
	a := r.attach(0, PresetSharp, human)
	b := r.attach(1, PresetSaver, human2)
	st, err := r.ctl.Acquire(context.Background(), "pc", control.OwnerHuman, cli, false)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*viewerClient{"a": a, "b": b} {
		n := c.notice("owner_changed")
		if n["owner"] != "human" || n["owner_client"] != cli.ClientName || n["owner_client_id"] != cli.ClientID ||
			n["gen"] != float64(st.Gen) || n["ai_paused"] != false {
			t.Fatalf("viewer %s: owner_changed %v, want %+v", name, n, st)
		}
	}
}

func TestPresetsOpenAtTheBestAndApplyTheRestWithConfig(t *testing.T) {
	r := newRig(t)
	a := r.attach(0, PresetSaver, human)
	v := r.agent.video(0)
	if v.hdr.MaxWidth != 1920 || v.hdr.Bitrate != 4_000_000 {
		t.Fatalf("saver stream header max_width %d bitrate %d", v.hdr.MaxWidth, v.hdr.Bitrate)
	}
	b := r.attach(0, PresetSharp, human2)
	if c := v.control(t, wire.ControlBitrate); c.BPS != 20_000_000 {
		t.Fatalf("bitrate for a sharp viewer %d", c.BPS)
	}
	if c := v.control(t, wire.ControlConfig); c.MaxWidth != 0 {
		t.Fatalf("config for a sharp viewer max_width %d", c.MaxWidth)
	}
	b.close()
	if c := v.control(t, wire.ControlBitrate); c.BPS != 4_000_000 {
		t.Fatalf("bitrate after the sharp viewer left %d", c.BPS)
	}
	if c := v.control(t, wire.ControlConfig); c.MaxWidth != 1920 {
		t.Fatalf("config after the sharp viewer left max_width %d", c.MaxWidth)
	}
	a.send(wire.KindClientControl, map[string]any{"op": "preset", "preset": "balanced"})
	if c := v.control(t, wire.ControlBitrate); c.BPS != 12_000_000 {
		t.Fatalf("bitrate for balanced %d", c.BPS)
	}
	if c := v.control(t, wire.ControlConfig); c.MaxWidth != 2560 {
		t.Fatalf("config for balanced max_width %d", c.MaxWidth)
	}
	if r.agent.videoCount() != 1 {
		t.Fatalf("%d agent streams", r.agent.videoCount())
	}
}

func TestViewerKeyframeRequestReachesTheAgent(t *testing.T) {
	r := newRig(t)
	a := r.attach(0, PresetSharp, human)
	v := r.agent.video(0)
	v.drainControls()
	a.send(wire.KindClientControl, map[string]any{"op": "keyframe"})
	v.control(t, wire.ControlKeyframe)
}

func TestPeriodicKeyframeRequests(t *testing.T) {
	r := newRig(t, func(o *Options) { o.KeyframeEvery = 50 * time.Millisecond })
	r.attach(0, PresetSharp, human)
	v := r.agent.video(0)
	v.drainControls()
	v.control(t, wire.ControlKeyframe)
	v.control(t, wire.ControlKeyframe)
}

func TestCursorFramesReachViewersAndANewViewerGetsTheCurrentCursor(t *testing.T) {
	r := newRig(t)
	a := r.attach(0, PresetSharp, human)
	eventually(t, "the cursor stream", func() bool { return r.agent.cursor(0) != nil })
	c := r.agent.cursor(0)
	if c.hdr.Display != 0 {
		t.Fatalf("cursor header %+v", c.hdr)
	}
	shape, _ := wire.CursorShape{ID: 9, W: 1, H: 1, BGRA: []byte{1, 2, 3, 4}}.Marshal()
	pos := wire.CursorPos{X: 5, Y: 6, Visible: true, ShapeID: 9}.Marshal()
	c.write(wire.KindCursorShape, shape)
	c.write(wire.KindCursorPos, pos)
	if f := a.nextOf(wire.KindCursorShape); !bytes.Equal(f.payload, shape) {
		t.Fatalf("shape %x", f.payload)
	}
	if f := a.nextOf(wire.KindCursorPos); !bytes.Equal(f.payload, pos) {
		t.Fatalf("pos %x", f.payload)
	}
	b := r.attach(0, PresetSharp, human2)
	if f := b.nextOf(wire.KindCursorShape); !bytes.Equal(f.payload, shape) {
		t.Fatalf("new viewer's shape %x", f.payload)
	}
	if f := b.nextOf(wire.KindCursorPos); !bytes.Equal(f.payload, pos) {
		t.Fatalf("new viewer's pos %x", f.payload)
	}
}

func TestDisplayRemovedPausesItsViewersWithNoOutput(t *testing.T) {
	r := newRig(t)
	a := r.attach(1, PresetSharp, human)
	b := r.attach(0, PresetSharp, human2)
	r.bus.Publish("pc", "", events.DisplayChanged, map[string]any{"displays": []any{map[string]any{"index": float64(0)}}})
	if f := a.nextOf(wire.KindVideoPaused); string(f.payload) != `{"reason":"no_output"}` {
		t.Fatalf("paused %s", f.payload)
	}
	if !b.quiet(wire.KindVideoPaused, 100*time.Millisecond) {
		t.Fatal("the viewer of the display that remains got a paused frame")
	}
}

func TestConnectionStateReachesViewers(t *testing.T) {
	r := newRig(t)
	a := r.attach(0, PresetSharp, human)
	r.bus.Publish("pc", "", events.ConnectionState, map[string]any{"state": "reconnecting", "connection_generation": float64(1), "reason": "lost"})
	n := a.notice("connection_state")
	if n["state"] != "reconnecting" || n["reason"] != "lost" {
		t.Fatalf("connection_state %v", n)
	}
}

func TestAgentStreamThatEndsIsOpenedAgainForItsViewers(t *testing.T) {
	r := newRig(t)
	a := r.attach(0, PresetSharp, human)
	v := r.agent.video(0)
	v.au(true, 1)
	a.nextAU()
	v.end()
	eventually(t, "a second agent stream", func() bool { return r.agent.videoCount() == 2 })
	r.agent.video(1).au(true, 1)
	a.nextOf(wire.KindVideoInfo)
	if au := a.nextAU(); au.Seq != 1 || !au.Key {
		t.Fatalf("access unit on the new agent stream seq %d key %v", au.Seq, au.Key)
	}
}

func TestAttachRefusals(t *testing.T) {
	r := newRig(t)
	r.agent.refuseVideo[3] = wire.CodeNotFound
	r.agent.refuseVideo[4] = wire.CodeCapabilityUnsupported
	ctx := context.Background()
	for _, tc := range []struct {
		display int
		preset  Preset
		code    string
	}{
		{3, PresetSharp, wire.CodeDisplayUnavailable},
		{4, PresetSharp, wire.CodeCapabilityUnsupported},
		{-1, PresetSharp, wire.CodeInvalidArgument},
		{0, "ultra", wire.CodeInvalidArgument},
	} {
		if _, err := r.hub.Attach(ctx, "pc", tc.display, tc.preset, human); apierr.CodeOf(err) != tc.code {
			t.Fatalf("attach display %d preset %q: %v, want %s", tc.display, tc.preset, err, tc.code)
		}
	}
	if _, err := r.hub.Attach(ctx, "other", 0, PresetSharp, human); apierr.CodeOf(err) == "" {
		t.Fatalf("attach to a host without an agent connection: %v", err)
	}
}
