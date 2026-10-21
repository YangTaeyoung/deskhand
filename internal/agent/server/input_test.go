package server

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/agent/input"
	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestControlSetWriterOnlyRaisesAndEmitsPreempted(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	if got := c.ok("control.set_writer", proto.Message{"gen": 3}); got["gen"] != float64(3) {
		t.Fatalf("set_writer 3 %v", got)
	}
	ev := c.event("input.preempted")
	if ev["gen"] != float64(3) || ev["previous"] != float64(0) || !reflect.DeepEqual(ev["released"], []any{}) {
		t.Fatalf("input.preempted %v", ev)
	}
	c.events = nil
	if got := c.ok("control.set_writer", proto.Message{"gen": 2}); got["gen"] != float64(3) {
		t.Fatalf("set_writer 2 %v", got)
	}
	if got := c.ok("input.state", nil); got["gen"] != float64(3) || !reflect.DeepEqual(got["pressed"], []any{}) {
		t.Fatalf("input.state %v", got)
	}
	for _, e := range c.events {
		if e.String("name") == "input.preempted" {
			t.Fatalf("a lower generation emitted %v", e)
		}
	}
	for _, bad := range []any{nil, "4", -1, 1.5} {
		res := c.call("control.set_writer", proto.Message{"gen": bad})
		if res["ok"] != false || res.String("code") != wire.CodeInvalidArgument || res.String("error") == "" {
			t.Fatalf("set_writer %v: %v", bad, res)
		}
	}
	if got := c.ok("input.release_all", nil); !reflect.DeepEqual(got["released"], []any{}) {
		t.Fatalf("input.release_all %v", got)
	}
}

func TestUIOpsOfAnOlderWriterArePreempted(t *testing.T) {
	ui := &fakeUI{}
	clip := &fakeClipboard{text: "keep"}
	c, _ := newRPCClient(t, Options{UI: ui, Clipboard: clip})
	c.ok("control.set_writer", proto.Message{"gen": 5})
	for _, req := range []struct {
		op     string
		fields proto.Message
	}{
		{"ui.act", proto.Message{"ref": "e1", "action": "invoke", "gen": 4}},
		{"win.action", proto.Message{"window": "0x1", "action": "focus"}},
		{"clipboard.set", proto.Message{"text": "new", "gen": 4}},
		{"app.launch", proto.Message{"path": "notepad", "gen": 0}},
		{"debug.electron.launch", proto.Message{"path": "deskhand-no-such-electron-app", "gen": 3}},
	} {
		res := c.call(req.op, req.fields)
		if res["ok"] != false || res.String("code") != wire.CodeControlPreempted || res["retryable"] != false || res.String("error") == "" {
			t.Fatalf("%s %v: %v", req.op, req.fields, res)
		}
	}
	if ui.callCount() != 0 || clip.text != "keep" {
		t.Fatalf("a preempted op ran: %d UI calls, clipboard %q", ui.callCount(), clip.text)
	}
	c.ok("ui.act", proto.Message{"ref": "e1", "action": "invoke", "gen": 5})
	c.ok("ui.tree", proto.Message{"window": "foreground"})
	c.ok("clipboard.set", proto.Message{"text": "new", "gen": 6})
	if clip.text != "new" {
		t.Fatalf("clipboard %q", clip.text)
	}
	if got := c.ok("input.state", nil); got["gen"] != float64(6) {
		t.Fatalf("a newer generation in an op did not become the writer's: %v", got)
	}
	ui.mu.Lock()
	params := ui.calls[0].params
	ui.mu.Unlock()
	if _, ok := params["gen"]; ok || params["ref"] != "e1" {
		t.Fatalf("ui.act params %v", params)
	}
	if res := c.call("ui.act", proto.Message{"ref": "e1", "gen": "5"}); res.String("code") != wire.CodeInvalidArgument {
		t.Fatalf("ui.act with a string gen %v", res)
	}
}

func TestAPreemptedJournaledOpReplaysItsRefusal(t *testing.T) {
	ui := &fakeUI{}
	c, _ := newRPCClient(t, Options{UI: ui})
	c.ok("control.set_writer", proto.Message{"gen": 2})
	first := c.call("ui.act", proto.Message{"rid": "r-preempted-1", "ref": "e1", "gen": 1})
	again := c.call("ui.act", proto.Message{"rid": "r-preempted-1", "ref": "e1", "gen": 2})
	if first.String("code") != wire.CodeControlPreempted || again.String("code") != wire.CodeControlPreempted || again["replayed"] != true {
		t.Fatalf("first %v, again %v", first, again)
	}
	if ui.callCount() != 0 {
		t.Fatalf("%d UI calls", ui.callCount())
	}
}

func TestReleaseErrorNamesWhatWasFreed(t *testing.T) {
	freed := []input.Pressed{{K: "button", B: "left"}, {K: "key", SC: 0x1D, Ext: true}, {K: "key", VK: 0x15}}
	m := failure(1, releaseError(freed, &input.Error{Code: wire.CodeSessionLocked, Msg: "the PC is locked; unlock it"}))
	if m["code"] != wire.CodeSessionLocked || m["retryable"] != false {
		t.Fatalf("release error %v", m)
	}
	msg, _ := m["error"].(string)
	for _, want := range []string{"button left", "sc 0x1d (ext)", "vk 0x15", "the PC is locked"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("release error %q has no %q", msg, want)
		}
	}
	empty := failure(1, releaseError(nil, &input.Error{Code: wire.CodeSessionLocked, Msg: "x"}))
	if msg, _ := empty["error"].(string); !strings.Contains(msg, "released nothing") {
		t.Fatalf("empty release error %v", empty)
	}
}

func TestTooLargeGenOnAUIOpIsInvalid(t *testing.T) {
	c, _ := newRPCClient(t, Options{UI: &fakeUI{}})
	res := c.call("ui.act", proto.Message{"ref": "e1", "gen": float64(1e18)})
	if res["ok"] != false || res.String("code") != wire.CodeInvalidArgument {
		t.Fatalf("ui.act with a too-large gen %v", res)
	}
	if got := c.ok("input.state", nil); got["gen"] != float64(0) {
		t.Fatalf("a too-large gen raised the writer generation: %v", got)
	}
}

func TestInputCapFollowsTheSession(t *testing.T) {
	zero := testSession
	zero.SessionID = 0
	locked := testSession
	locked.Locked, locked.InputDesktop = boolPtr(true), session.DesktopWinlogon
	secure := testSession
	secure.InputDesktop = session.DesktopWinlogon
	gone := testSession
	gone.ConnectState = session.ConnectDisconnected
	for _, c := range []struct {
		name string
		info session.Info
		err  error
		want capState
	}{
		{"user session", testSession, nil, capState{State: capReady}},
		{"session 0", zero, nil, capState{State: capUnavailable, Reason: "session0"}},
		{"locked", locked, nil, capState{State: capNeedsUser, Reason: "session_locked"}},
		{"secure desktop", secure, nil, capState{State: capNeedsUser, Reason: "secure_desktop"}},
		{"disconnected", gone, nil, capState{State: capUnavailable, Reason: "session_disconnected"}},
		{"no windows", session.Info{}, session.ErrUnsupported, capState{State: capUnsupported, Reason: session.ErrUnsupported.Error()}},
		{"unreadable", session.Info{}, errors.New("WTSQuerySessionInformation failed"), capState{State: capUnavailable, Reason: "WTSQuerySessionInformation failed"}},
	} {
		if got := inputCap(true, c.info, c.err); !got.equal(c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
	if got := inputCap(false, testSession, nil); got.State != capUnsupported || got.Reason == "" {
		t.Fatalf("input without an injector: %+v", got)
	}
}
