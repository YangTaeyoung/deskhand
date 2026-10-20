package input

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/monoclock"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestLastInjectedTimesTheLastInputAndTheLastAction(t *testing.T) {
	var refuse atomic.Bool
	sys := &fakeSystem{accept: func(_ int, in []raw) int {
		if refuse.Load() {
			return 0
		}
		return len(in)
	}}
	j := newTestInjector(t, sys, userSession, oneDisplay, Options{})
	if input, action := j.LastInjected(); input != 0 || action != 0 {
		t.Fatalf("LastInjected %d, %d before any input", input, action)
	}
	var lastInput, lastAction uint64
	// step runs do and checks that the input reading changed when injects
	// and the action reading when acts.
	step := func(name string, injects, acts bool, do func()) {
		t.Helper()
		time.Sleep(2 * time.Millisecond)
		before := monoclock.NowUS()
		do()
		after := monoclock.NowUS()
		input, action := j.LastInjected()
		for _, c := range []struct {
			what       string
			changes    bool
			got, prior uint64
		}{{"input", injects, input, lastInput}, {"action", acts, action, lastAction}} {
			switch {
			case c.changes && (c.got < before || c.got > after):
				t.Fatalf("%s: the %s reading is %d, want one from %d to %d", name, c.what, c.got, before, after)
			case !c.changes && c.got != c.prior:
				t.Fatalf("%s: the %s reading went from %d to %d", name, c.what, c.prior, c.got)
			}
		}
		lastInput, lastAction = input, action
	}
	step("move", true, false, func() { mustOK(t, j.Apply(wire.InputEvent{Seq: 1, K: "move", X: 10, Y: 10})) })
	step("button down", true, true, func() { mustOK(t, j.Apply(wire.InputEvent{Seq: 2, K: "button", B: "left", Down: true})) })
	step("another move", true, false, func() { mustOK(t, j.Apply(wire.InputEvent{Seq: 3, K: "move", X: 20, Y: 10})) })
	step("release_all", true, true, func() { mustOK(t, j.Apply(wire.InputEvent{Seq: 4, K: "release_all"})) })
	step("release_all with nothing held", false, false, func() { mustOK(t, j.Apply(wire.InputEvent{Seq: 5, K: "release_all"})) })
	step("wheel", true, true, func() { mustOK(t, j.Apply(wire.InputEvent{Seq: 6, K: "wheel", DY: -120})) })
	step("text", true, true, func() { mustOK(t, j.Apply(wire.InputEvent{Seq: 7, K: "text", Text: "ab"})) })
	step("key down", true, true, func() { mustOK(t, j.Apply(keyDown(8, 0, 0x1E))) })
	step("ReleaseAll", true, true, func() {
		if _, err := j.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
	})
	step("an invalid event", false, false, func() { mustFail(t, j.Apply(wire.InputEvent{Seq: 9, K: "jump"}), wire.CodeInvalidArgument) })
	j.SetWriter(5)
	step("an event of an older generation", false, false, func() { mustFail(t, j.Apply(keyDown(10, 1, 0x1E)), wire.CodeControlPreempted) })
	refuse.Store(true)
	step("a send Windows refused", false, false, func() { mustFail(t, j.Apply(keyDown(11, 5, 0x1E)), wire.CodeInternal) })
}
