package input

import (
	"io"
	"log"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestStaleGenerationRejected(t *testing.T) {
	sys := &fakeSystem{}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{})
	mustOK(t, j.Apply(keyDown(1, 0, 0x1E)))
	if got := j.SetWriter(5); got != 5 {
		t.Fatalf("SetWriter(5) = %d", got)
	}
	before := len(sys.all())
	for _, gen := range []uint64{0, 4} {
		ack := j.Apply(keyDown(2, gen, 0x1E))
		mustFail(t, ack, wire.CodeControlPreempted)
		if ack.Seq != 2 {
			t.Fatalf("ack seq %d", ack.Seq)
		}
	}
	if got := sys.all()[before:]; len(got) != 0 {
		t.Fatalf("a stale event sent %v", got)
	}
	mustOK(t, j.Apply(keyDown(3, 5, 0x1E)))
	if got := j.SetWriter(3); got != 5 || j.Gen() != 5 {
		t.Fatalf("SetWriter(3) = %d, gen %d; the generation only rises", got, j.Gen())
	}
	if err := j.Admit(4); err == nil || !strings.Contains(err.Error(), "5") {
		t.Fatalf("Admit(4) = %v", err)
	} else if e, ok := err.(*Error); !ok || e.Code != wire.CodeControlPreempted {
		t.Fatalf("Admit(4) error %#v", err)
	}
	if err := j.Admit(5); err != nil {
		t.Fatalf("Admit(5) = %v", err)
	}
}

func TestANewerGenerationInAnEventTakesOver(t *testing.T) {
	var got []Preemption
	sys := &fakeSystem{}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{Preempted: func(p Preemption) { got = append(got, p) }})
	j.SetWriter(2)
	mustOK(t, j.Apply(keyDown(1, 2, 0x2A)))
	mustOK(t, j.Apply(keyDown(2, 3, 0x1E)))
	if j.Gen() != 3 {
		t.Fatalf("gen %d", j.Gen())
	}
	mustFail(t, j.Apply(keyDown(3, 2, 0x1E)), wire.CodeControlPreempted)
	want := []Preemption{{Gen: 2}, {Gen: 3, Previous: 2, Released: []Pressed{{K: "key", SC: 0x2A}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("preemptions %+v, want %+v", got, want)
	}
}

func TestSetWriterReleasesHeldInputAndReportsPreemption(t *testing.T) {
	var got []Preemption
	sys := &fakeSystem{}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{Preempted: func(p Preemption) { got = append(got, p) }})
	j.SetWriter(1)
	mustOK(t, j.Apply(wire.InputEvent{Seq: 1, Gen: 1, K: "key", SC: 0x1D, Ext: true, Down: true}))
	mustOK(t, j.Apply(wire.InputEvent{Seq: 2, Gen: 1, K: "button", B: "left", Down: true}))
	mustOK(t, j.Apply(wire.InputEvent{Seq: 3, Gen: 1, K: "key", VK: 0x15, Down: true}))
	wantHeld := []Pressed{{K: "button", B: "left"}, {K: "key", SC: 0x1D, Ext: true}, {K: "key", VK: 0x15}}
	if p := j.Pressed(); !reflect.DeepEqual(p, wantHeld) {
		t.Fatalf("pressed %+v", p)
	}
	j.SetWriter(2)
	calls := sys.sent()
	last := calls[len(calls)-1]
	want := []raw{
		{mouse: true, flags: mouseeventfLeftUp},
		{scan: 0x1D, flags: keyeventfScancode | keyeventfExtendedKey | keyeventfKeyUp},
		{vk: 0x15, scan: 0x115, flags: keyeventfKeyUp},
	}
	if !reflect.DeepEqual(last, want) {
		t.Fatalf("release sent %+v, want %+v", last, want)
	}
	if len(j.Pressed()) != 0 {
		t.Fatalf("still pressed %+v", j.Pressed())
	}
	if len(got) != 2 || got[1].Gen != 2 || got[1].Previous != 1 || !reflect.DeepEqual(got[1].Released, wantHeld) {
		t.Fatalf("preemptions %+v", got)
	}
}

func TestAKeyUpEndsItsPress(t *testing.T) {
	sys := &fakeSystem{}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{})
	mustOK(t, j.Apply(keyDown(1, 0, 0x2A)))
	mustOK(t, j.Apply(wire.InputEvent{Seq: 2, K: "key", SC: 0x2A}))
	if p := j.Pressed(); len(p) != 0 {
		t.Fatalf("pressed %+v", p)
	}
	released, err := j.ReleaseAll()
	if err != nil || len(released) != 0 {
		t.Fatalf("ReleaseAll = %v, %v", released, err)
	}
	if n := len(sys.sent()); n != 2 {
		t.Fatalf("%d sends, want 2 (nothing to release)", n)
	}
}

func TestReleaseAllOnStreamClose(t *testing.T) {
	sys := &fakeSystem{}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{})
	c, first := serveTestStream(t, j, 0)
	if first["ok"] != true {
		t.Fatalf("first line %v", first)
	}
	c.send(keyDown(1, 0, 0x1D))
	c.send(wire.InputEvent{Seq: 2, K: "button", B: "right", Down: true})
	for seq := uint64(1); seq <= 2; seq++ {
		if a := c.ack(); !a.OK || a.Seq != seq || a.DoneMonoUS == 0 {
			t.Fatalf("ack %+v", a)
		}
	}
	c.closeAndWait()
	calls := sys.sent()
	last := calls[len(calls)-1]
	want := []raw{{mouse: true, flags: mouseeventfRightUp}, keyRaw(0x1D, true)}
	if !reflect.DeepEqual(last, want) {
		t.Fatalf("on close sent %+v, want %+v", last, want)
	}
	if p := j.Pressed(); len(p) != 0 {
		t.Fatalf("still pressed %+v", p)
	}
}

func TestStreamCloseReleasesOnlyItsOwnInput(t *testing.T) {
	sys := &fakeSystem{}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{})
	a, _ := serveTestStream(t, j, 0)
	b, _ := serveTestStream(t, j, 0)
	a.send(keyDown(1, 0, 0x2A))
	a.ack()
	b.send(keyDown(1, 0, 0x1D))
	b.ack()
	a.closeAndWait()
	if p := j.Pressed(); !reflect.DeepEqual(p, []Pressed{{K: "key", SC: 0x1D}}) {
		t.Fatalf("pressed after the first stream closed %+v", p)
	}
	b.closeAndWait()
	if p := j.Pressed(); len(p) != 0 {
		t.Fatalf("pressed %+v", p)
	}
}

func TestStreamRefusesAStaleHeader(t *testing.T) {
	j := newTestInjector(t, &fakeSystem{}, userSession, oneDisplay, Options{})
	j.SetWriter(3)
	c, first := serveTestStream(t, j, 2)
	if first["ok"] != false || first["code"] != wire.CodeControlPreempted || first["error"] == "" {
		t.Fatalf("first line %v", first)
	}
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		t.Fatal("a refused stream stays open")
	}
}

func TestStreamAcceptsWithTheCurrentGeneration(t *testing.T) {
	j := newTestInjector(t, &fakeSystem{}, userSession, oneDisplay, Options{})
	j.SetWriter(3)
	c, first := serveTestStream(t, j, 4)
	if first["ok"] != true || first["gen"] != float64(4) {
		t.Fatalf("first line %v", first)
	}
	c.send(keyDown(1, 3, 0x1E))
	mustFail(t, c.ack(), wire.CodeControlPreempted)
	c.send(wire.InputEvent{Seq: 2, Gen: 4, K: "release_all"})
	mustOK(t, c.ack())
}

func TestStreamWithANewerGenerationPreemptsARunningText(t *testing.T) {
	release, entered := make(chan struct{}), make(chan struct{})
	var once sync.Once
	sys := &fakeSystem{}
	preempted := make(chan Preemption, 4)
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{Preempted: func(p Preemption) { preempted <- p }})
	j.SetWriter(1)
	<-preempted
	sys.hook = func(call int, in []raw) {
		if in[0].flags&keyeventfUnicode != 0 {
			once.Do(func() {
				close(entered)
				<-release
			})
		}
	}
	c, _ := serveTestStream(t, j, 1)
	c.send(wire.InputEvent{Seq: 1, Gen: 1, K: "text", Text: strings.Repeat("가", 6000), Method: "unicode"})
	<-entered
	c.send(wire.InputEvent{Seq: 2, Gen: 2, K: "key", SC: 0x1E, Down: true})
	deadline := time.Now().Add(5 * time.Second)
	for j.Gen() != 2 {
		if time.Now().After(deadline) {
			t.Fatal("the newer generation waited for the text before taking over")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	select {
	case p := <-preempted:
		if p.Gen != 2 || p.Previous != 1 {
			t.Fatalf("preemption %+v", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no preemption")
	}
	a := c.ack()
	mustFail(t, a, wire.CodeControlPreempted)
	if !strings.Contains(a.Error, "100 of 6000") {
		t.Fatalf("text ack %+v", a)
	}
	mustOK(t, c.ack())
}

func TestInputDesktopWinlogonReported(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(*session.Info)
		code   string
		reason string
	}{
		{"locked", func(i *session.Info) { i.Locked, i.InputDesktop = true, session.DesktopWinlogon }, wire.CodeSessionLocked, "session_locked"},
		{"secure desktop", func(i *session.Info) { i.InputDesktop = session.DesktopWinlogon }, wire.CodeElevationRequired, "secure_desktop"},
		{"session 0", func(i *session.Info) { i.SessionID, i.InputDesktop = 0, session.DesktopUnknown }, wire.CodeDisplayUnavailable, "session0"},
		{"disconnected", func(i *session.Info) { i.ConnectState = session.ConnectDisconnected }, wire.CodeDisplayUnavailable, "session_disconnected"},
	} {
		info := userSession
		c.mutate(&info)
		sys := &fakeSystem{}
		j := newTestInjector(t, sys, info, oneDisplay, Options{})
		for _, ev := range []wire.InputEvent{
			keyDown(1, 0, 0x1E),
			{Seq: 2, K: "move", X: 10, Y: 10},
			{Seq: 3, K: "text", Text: "a", Method: "unicode"},
		} {
			ack := j.Apply(ev)
			mustFail(t, ack, c.code)
			if !strings.Contains(ack.Error, c.reason) {
				t.Errorf("%s: %s error %q has no %s", c.name, ev.K, ack.Error, c.reason)
			}
		}
		if got := sys.all(); len(got) != 0 {
			t.Errorf("%s: sent %v", c.name, got)
		}
		if got := Outage(info); got != c.reason {
			t.Errorf("%s: Outage = %q", c.name, got)
		}
	}
	if got := Outage(userSession); got != "" {
		t.Fatalf("Outage of the user's session = %q", got)
	}
}

func TestReleaseAllAttemptsAndMarksStaleInALockedSession(t *testing.T) {
	cur := userSession
	refuse := false
	sys := &fakeSystem{accept: func(_ int, in []raw) int {
		if refuse {
			return 0
		}
		return len(in)
	}}
	j := newInjector(sys, Options{Logger: discardLogger(), Displays: func() ([]display.Info, error) { return oneDisplay, nil },
		SessionInfo: func() (session.Info, error) { return cur, nil }, InputDesktop: func() string { return cur.InputDesktop }})
	mustOK(t, j.Apply(wire.InputEvent{Seq: 1, K: "key", SC: 0x2A, Down: true}))
	cur.Locked, cur.InputDesktop = true, session.DesktopWinlogon
	refuse = true
	before := len(sys.sent())
	mustFail(t, j.Apply(wire.InputEvent{Seq: 2, K: "release_all"}), wire.CodeSessionLocked)
	if len(sys.sent()) <= before {
		t.Fatal("release_all in a locked session did not attempt to release")
	}
	if p := j.Pressed(); !reflect.DeepEqual(p, []Pressed{{K: "key", SC: 0x2A}}) {
		t.Fatalf("pressed after a refused release_all %+v", p)
	}
	cur = userSession
	refuse = false
	mark := len(sys.sent())
	mustOK(t, j.Apply(wire.InputEvent{Seq: 3, K: "key", SC: 0x1D, Down: true}))
	if calls := sys.sent()[mark:]; !reflect.DeepEqual(calls, [][]raw{{keyRaw(0x2A, true)}, {keyRaw(0x1D, false)}}) {
		t.Fatalf("the stale release was not retried before the next input: %+v", calls)
	}
}

func discardLogger() *log.Logger { return log.New(io.Discard, "", 0) }

func TestAGenerationPastItsBoundIsInvalidAndDoesNotRaise(t *testing.T) {
	sys := &fakeSystem{}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{})
	mustFail(t, j.Apply(wire.InputEvent{Seq: 1, Gen: maxGen + 1, K: "key", SC: 0x1E, Down: true}), wire.CodeInvalidArgument)
	if j.Gen() != 0 {
		t.Fatalf("a too-large event gen raised the generation to %d", j.Gen())
	}
	if len(sys.all()) != 0 {
		t.Fatalf("a too-large event injected %v", sys.all())
	}
	c, first := serveTestStream(t, j, maxGen+1)
	if first["ok"] != false || first["code"] != wire.CodeInvalidArgument || first["error"] == "" {
		t.Fatalf("stream header first line %v", first)
	}
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		t.Fatal("a stream with a too-large header gen stays open")
	}
	if j.Gen() != 0 {
		t.Fatalf("a too-large header gen raised the generation to %d", j.Gen())
	}
	c2, ok := serveTestStream(t, j, 0)
	if ok["ok"] != true {
		t.Fatalf("stream first line %v", ok)
	}
	c2.send(wire.InputEvent{Seq: 1, Gen: maxGen + 1, K: "key", SC: 0x1E, Down: true})
	mustFail(t, c2.ack(), wire.CodeInvalidArgument)
	if j.Gen() != 0 {
		t.Fatalf("a too-large event gen on a stream raised the generation to %d", j.Gen())
	}
}

func TestAShortSendIsReportedAndNotTracked(t *testing.T) {
	for _, c := range []struct {
		above bool
		code  string
	}{{true, wire.CodeElevationRequired}, {false, wire.CodeInternal}} {
		sys := &fakeSystem{above: c.above, accept: func(int, []raw) int { return 0 }}
		j := newTestInjector(t, sys, userSession, oneDisplay, Options{})
		mustFail(t, j.Apply(keyDown(1, 0, 0x2A)), c.code)
		if p := j.Pressed(); len(p) != 0 {
			t.Fatalf("pressed %+v after a refused send", p)
		}
	}
}

func TestAPartlySentBatchReleasesWhatItPressed(t *testing.T) {
	sys := &fakeSystem{accept: func(call int, in []raw) int {
		if call == 0 {
			return 1
		}
		return len(in)
	}}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{})
	mustFail(t, j.Apply(wire.InputEvent{Seq: 1, K: "text", Text: "ab", Method: "unicode"}), wire.CodeInternal)
	calls := sys.sent()
	if len(calls) != 2 || !reflect.DeepEqual(calls[1], []raw{unicodeRaw('a', true)}) {
		t.Fatalf("sends %+v", calls)
	}
}

func TestAFailedReleaseIsRetriedBeforeTheNextInput(t *testing.T) {
	refuse := false
	sys := &fakeSystem{}
	sys.accept = func(call int, in []raw) int {
		if refuse {
			return 0
		}
		return len(in)
	}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{})
	j.SetWriter(1)
	mustOK(t, j.Apply(keyDown(1, 1, 0x2A)))
	refuse = true
	j.SetWriter(2)
	if p := j.Pressed(); !reflect.DeepEqual(p, []Pressed{{K: "key", SC: 0x2A}}) {
		t.Fatalf("pressed after a refused release %+v", p)
	}
	refuse = false
	before := len(sys.sent())
	mustOK(t, j.Apply(keyDown(2, 2, 0x1D)))
	calls := sys.sent()[before:]
	want := [][]raw{{keyRaw(0x2A, true)}, {keyRaw(0x1D, false)}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("sends %+v, want %+v", calls, want)
	}
	if p := j.Pressed(); !reflect.DeepEqual(p, []Pressed{{K: "key", SC: 0x1D}}) {
		t.Fatalf("pressed %+v", p)
	}
}

func TestKeyButtonAndWheelInputs(t *testing.T) {
	sys := &fakeSystem{}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{})
	for _, ev := range []wire.InputEvent{
		{Seq: 1, K: "key", SC: 0x4B, Ext: true, Down: true},
		{Seq: 2, K: "key", SC: 0x4B, Ext: true},
		{Seq: 3, K: "key", VK: 0x25, Down: true},
		{Seq: 4, K: "key", VK: 0x15},
		{Seq: 5, K: "button", B: "x2", Down: true},
		{Seq: 6, K: "button", B: "middle"},
		{Seq: 7, K: "wheel", DX: 30, DY: -120},
		{Seq: 8, K: "wheel", DY: 15},
		{Seq: 9, K: "key", SC: 0x1E, VK: 0x41, Down: true},
	} {
		mustOK(t, j.Apply(ev))
	}
	neg120 := int32(-120)
	want := []raw{
		{scan: 0x4B, flags: keyeventfScancode | keyeventfExtendedKey},
		{scan: 0x4B, flags: keyeventfScancode | keyeventfExtendedKey | keyeventfKeyUp},
		{vk: 0x25, scan: 0x125, flags: keyeventfExtendedKey},
		{vk: 0x15, scan: 0x115, flags: keyeventfKeyUp},
		{mouse: true, data: xbutton2, flags: mouseeventfXDown},
		{mouse: true, flags: mouseeventfMiddleUp},
		{mouse: true, data: uint32(neg120), flags: mouseeventfWheel},
		{mouse: true, data: 30, flags: mouseeventfHWheel},
		{mouse: true, data: 15, flags: mouseeventfWheel},
		{scan: 0x1E, flags: keyeventfScancode},
	}
	if got := sys.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("sent\n%+v\nwant\n%+v", got, want)
	}
}

func TestMalformedEventsAreInvalid(t *testing.T) {
	sys := &fakeSystem{}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{})
	for _, ev := range []wire.InputEvent{
		{Seq: 1, K: "key", Down: true},
		{Seq: 2, K: "key", SC: 0x1E0, Down: true},
		{Seq: 3, K: "key", VK: 0x100},
		{Seq: 4, K: "button", B: "back", Down: true},
		{Seq: 5, K: "text", Text: "a", Method: "type"},
		{Seq: 6, K: "tap"},
		{Seq: 7},
	} {
		mustFail(t, j.Apply(ev), wire.CodeInvalidArgument)
	}
	if got := sys.all(); len(got) != 0 {
		t.Fatalf("sent %v", got)
	}
}

func TestInputWithoutAnOperatingSystemIsUnsupported(t *testing.T) {
	j := newInjector(nil, Options{})
	mustFail(t, j.Apply(keyDown(1, 0, 0x1E)), wire.CodeCapabilityUnsupported)
	j.SetWriter(2)
	mustFail(t, j.Apply(keyDown(2, 1, 0x1E)), wire.CodeControlPreempted)
}

func TestAbsoluteCoordinateScaling(t *testing.T) {
	displays := []display.Info{
		{Index: 0, X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
		{Index: 1, X: -2560, Y: -360, Width: 2560, Height: 1440},
	}
	vs := rect{x: -2560, y: -360, w: 4480, h: 1440}
	sys := &fakeSystem{vs: vs}
	j := newTestInjector(t, sys, userSession, displays, Options{})
	for _, c := range []struct {
		ev     wire.InputEvent
		px, py int
	}{
		{wire.InputEvent{K: "move", Display: 1, X: 0, Y: 0}, 0, 0},
		{wire.InputEvent{K: "move", Display: 1, X: 2559, Y: 1439}, 2559, 1439},
		{wire.InputEvent{K: "move", Display: 0, X: 0, Y: 0}, 2560, 360},
		{wire.InputEvent{K: "move", Display: 0, X: 1919, Y: 1079}, 4479, 1439},
		{wire.InputEvent{K: "move", Display: 0, X: 960, Y: 540}, 3520, 900},
		{wire.InputEvent{K: "move", Display: 0, X: 5000, Y: -7}, 4479, 360},
	} {
		before := len(sys.all())
		mustOK(t, j.Apply(c.ev))
		got := sys.all()[before:]
		if len(got) != 1 || !got[0].mouse || got[0].flags != mouseeventfMove|mouseeventfAbsolute|mouseeventfVirtualDesk {
			t.Fatalf("%+v sent %+v", c.ev, got)
		}
		if x, y := pixelOf(got[0].dx, vs.w), pixelOf(got[0].dy, vs.h); x != c.px || y != c.py {
			t.Errorf("%+v lands on (%d,%d) of the virtual desktop, want (%d,%d)", c.ev, x, y, c.px, c.py)
		}
	}
	mustFail(t, j.Apply(wire.InputEvent{Seq: 9, K: "move", Display: 2}), wire.CodeDisplayUnavailable)
	for _, w := range []int{1, 1920, 4480, 7680, 16384} {
		for x := 0; x < w; x++ {
			n := normalize(x, w)
			if n < 0 || n > 65535 || pixelOf(n, w) != x {
				t.Fatalf("width %d pixel %d normalizes to %d, back to %d", w, x, n, pixelOf(n, w))
			}
		}
	}
}

// pixelOf is the pixel of a 0–65535 coordinate on an axis of w pixels.
func pixelOf(n int32, w int) int { return int(int64(n) * int64(w) >> 16) }
