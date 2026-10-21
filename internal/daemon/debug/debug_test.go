package debug

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/tunnel"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

var testCaller = model.Caller{Actor: model.ActorHuman, ClientName: "tester"}

func bctx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// nodeServer is a fake Node inspector that resolves one script and one
// breakpoint.
func nodeServer(t *testing.T) *cdpServer {
	s := newCDPServer(t, "node.js/v22").withNodeTarget("node-1")
	s.respond = func(sessionID, method string, params json.RawMessage) (any, *CDPError) {
		switch method {
		case "Debugger.setBreakpointByUrl":
			return map[string]any{"breakpointId": "cdp-1", "locations": []any{
				map[string]any{"scriptId": "s1", "lineNumber": 9, "columnNumber": 2}}}, nil
		case "Runtime.getProperties":
			return map[string]any{"result": []any{
				map[string]any{"name": "total", "value": map[string]any{"type": "number", "value": 52000, "description": "52000"}},
				map[string]any{"name": "name", "value": map[string]any{"type": "string", "value": "서울"}}}}, nil
		}
		return nil, nil
	}
	return s
}

// launchBoth launches against a node and a browser endpoint.
func launchBoth(t *testing.T, node, browser *cdpServer, pauseOnStart bool) (*debugEnv, Session) {
	t.Helper()
	e := newDebugEnv(t, node.port(), browser.port())
	s, err := e.m.Launch(bctx(t), LaunchParams{HostRef: "h", Path: `C:\app\App.exe`, PauseOnStart: pauseOnStart}, testCaller)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	return e, s
}

// pauseMain pushes a Debugger.paused on the node connection with one frame
// holding a local scope.
func (s *cdpServer) pauseMain(reason string, hit []string) {
	frame := map[string]any{"callFrameId": "f0", "functionName": "calcTotal",
		"location": map[string]any{"scriptId": "s1", "lineNumber": 9, "columnNumber": 2}, "url": "file:///app/main.js",
		"scopeChain": []any{map[string]any{"type": "local",
			"object": map[string]any{"type": "object", "className": "Object", "objectId": "scope-1"}}},
		"this": map[string]any{"type": "object", "className": "global", "objectId": "this-1"}}
	s.emitAll("", "Debugger.paused", map[string]any{"reason": reason, "hitBreakpoints": hit, "callFrames": []any{frame}})
}

func TestBreakpointPauseStackVariablesResume(t *testing.T) {
	node := nodeServer(t)
	browser := newCDPServer(t, "Chrome/120").withPage(page{sessionID: "pg", targetID: "page-1", typ: "page", url: "file:///index.html"})
	e, s := launchBoth(t, node, browser, false)
	main := mainTarget(t, s)

	bp, err := e.m.BreakpointSet(bctx(t), s.DebugID, BreakpointSpec{Match: matchURL, Pattern: "file:///app/main.js", Line: 10}, testCaller)
	if err != nil {
		t.Fatalf("breakpoint.set: %v", err)
	}
	if !bp.Resolved || len(bp.Locations) == 0 || bp.Locations[0].Line != 10 {
		t.Fatalf("breakpoint not resolved at line 10: %+v", bp)
	}

	node.pauseMain(pauseReasonOther, []string{"cdp-1"})
	e.waitEvent(t, evPaused)

	st, err := e.m.State(s.DebugID, main)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if !st.Paused || st.Reason != pauseReasonBreakpoint {
		t.Fatalf("state reason = %q paused=%v", st.Reason, st.Paused)
	}
	if len(st.HitBreakpoints) != 1 || st.HitBreakpoints[0] != bp.BreakpointID {
		t.Fatalf("hit_breakpoints = %v, want [%s]", st.HitBreakpoints, bp.BreakpointID)
	}
	if len(st.CallFrames) != 1 || st.CallFrames[0].Function != "calcTotal" || st.CallFrames[0].Line != 10 {
		t.Fatalf("call frame = %+v", st.CallFrames)
	}
	scope := st.CallFrames[0].ScopeChain
	if len(scope) != 1 || scope[0].Type != "local" || scope[0].ObjectID != "scope-1" {
		t.Fatalf("scope = %+v", scope)
	}

	page, err := e.m.Variables(bctx(t), s.DebugID, main, "scope-1", 0, 200)
	if err != nil {
		t.Fatalf("variables: %v", err)
	}
	props := page["properties"].([]Property)
	if len(props) != 2 || props[0].Name != "total" || props[0].ValuePreview != "52000" {
		t.Fatalf("properties = %+v", props)
	}
	if props[1].ValuePreview != `"서울"` {
		t.Fatalf("korean string preview = %q", props[1].ValuePreview)
	}

	genBefore := st.Gen
	if _, oe := e.m.Resume(bctx(t), s.DebugID, main, testCaller); oe != nil {
		t.Fatalf("resume: %v", oe.Err)
	}
	node.emitAll("", "Debugger.resumed", map[string]any{})
	e.waitEvent(t, evResumed)
	after, _ := e.m.State(s.DebugID, main)
	if after.Paused || after.Gen <= genBefore {
		t.Fatalf("after resume gen=%d (before %d) paused=%v", after.Gen, genBefore, after.Paused)
	}
}

func TestCallFrameWithoutURLTakesTheURLOfItsParsedScript(t *testing.T) {
	node := nodeServer(t)
	browser := newCDPServer(t, "Chrome/120")
	e, s := launchBoth(t, node, browser, false)
	main := mainTarget(t, s)
	node.emitAll("", "Debugger.scriptParsed", map[string]any{"scriptId": "s1", "url": "file:///app/main.js"})
	frame := map[string]any{"callFrameId": "f0", "functionName": "calcTotal",
		"location": map[string]any{"scriptId": "s1", "lineNumber": 9, "columnNumber": 2}, "url": "",
		"scopeChain": []any{}}
	other := map[string]any{"callFrameId": "f1", "functionName": "",
		"location": map[string]any{"scriptId": "s9", "lineNumber": 0, "columnNumber": 0}, "url": "",
		"scopeChain": []any{}}
	node.emitAll("", "Debugger.paused", map[string]any{"reason": "other", "callFrames": []any{frame, other}})
	e.waitEvent(t, evPaused)
	st, err := e.m.State(s.DebugID, main)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if len(st.CallFrames) != 2 || st.CallFrames[0].URL != "file:///app/main.js" || st.CallFrames[1].URL != "" {
		t.Fatalf("call frames = %+v, want the URL of script s1 on the first and none on the unknown script", st.CallFrames)
	}
}

func TestStepOverIntoOut(t *testing.T) {
	node := nodeServer(t)
	browser := newCDPServer(t, "Chrome/120")
	e, s := launchBoth(t, node, browser, false)
	main := mainTarget(t, s)
	node.pauseMain(pauseReasonOther, nil)
	e.waitEvent(t, evPaused)

	for _, kind := range []string{"over", "into", "out"} {
		before, _ := e.m.State(s.DebugID, main)
		if _, oe := e.m.Step(bctx(t), s.DebugID, main, kind, testCaller); oe != nil {
			t.Fatalf("step %s: %v", kind, oe.Err)
		}
		node.emitAll("", "Debugger.resumed", map[string]any{})
		e.waitEvent(t, evResumed)
		node.pauseMain(pauseReasonOther, nil)
		ev := e.waitEvent(t, evPaused)
		stRaw, _ := json.Marshal(ev.Fields["state"])
		var ps PauseState
		_ = json.Unmarshal(stRaw, &ps)
		if ps.Reason != pauseReasonStep {
			t.Fatalf("step %s reason = %q", kind, ps.Reason)
		}
		if ps.Gen <= before.Gen {
			t.Fatalf("step %s gen did not advance: %d <= %d", kind, ps.Gen, before.Gen)
		}
	}
	if _, oe := e.m.Step(bctx(t), s.DebugID, main, "sideways", testCaller); oe == nil {
		t.Fatal("step with a bad kind was accepted")
	}
}

func TestEvaluateInFrame(t *testing.T) {
	node := nodeServer(t)
	node.respond = func(sessionID, method string, params json.RawMessage) (any, *CDPError) {
		if method == "Debugger.evaluateOnCallFrame" {
			return map[string]any{"result": map[string]any{"type": "number", "value": 52000, "description": "52000"}}, nil
		}
		if method == "Runtime.evaluate" {
			return map[string]any{"result": map[string]any{"type": "object", "className": "TypeError", "subtype": "error",
				"description": "TypeError: x is not a function"}, "exceptionDetails": map[string]any{
				"text": "Uncaught", "exception": map[string]any{"type": "object", "subtype": "error",
					"description": "TypeError: x is not a function"}}}, nil
		}
		return nil, nil
	}
	browser := newCDPServer(t, "Chrome/120")
	e, s := launchBoth(t, node, browser, false)
	main := mainTarget(t, s)
	node.pauseMain(pauseReasonOther, nil)
	e.waitEvent(t, evPaused)

	ev, oe := e.m.Evaluate(bctx(t), s.DebugID, main, "order.total", "f0", testCaller)
	if oe != nil {
		t.Fatalf("evaluate: %v", oe.Err)
	}
	if ev.ValuePreview != "52000" || ev.Exception != nil {
		t.Fatalf("evaluate frame = %+v", ev)
	}

	bad, oe := e.m.Evaluate(bctx(t), s.DebugID, main, "x()", "", testCaller)
	if oe != nil {
		t.Fatalf("evaluate throw: %v", oe.Err)
	}
	if bad.Exception == nil || bad.Exception.Text != "TypeError: x is not a function" {
		t.Fatalf("thrown value not reported: %+v", bad)
	}

	if _, oe := e.m.Resume(bctx(t), s.DebugID, main, testCaller); oe != nil {
		t.Fatalf("resume: %v", oe.Err)
	}
	node.emitAll("", "Debugger.resumed", map[string]any{})
	e.waitEvent(t, evResumed)
	if _, oe := e.m.Evaluate(bctx(t), s.DebugID, main, "order.total", "f0", testCaller); oe == nil || oe.Err.Code != wire.CodeStaleTarget {
		t.Fatalf("frame evaluate on a running target was not STALE_TARGET: %v", oe)
	}
}

func TestConsoleAndExceptionsRecorded(t *testing.T) {
	node := nodeServer(t)
	browser := newCDPServer(t, "Chrome/120")
	e, s := launchBoth(t, node, browser, false)

	node.emitAll("", "Runtime.consoleAPICalled", map[string]any{"type": "log",
		"args": []any{map[string]any{"type": "string", "value": "안녕하세요"}, map[string]any{"type": "number", "value": 3}}})
	node.emitAll("", "Runtime.exceptionThrown", map[string]any{"exceptionDetails": map[string]any{
		"text": "Uncaught", "lineNumber": 4, "columnNumber": 2, "url": "file:///app/main.js",
		"exception": map[string]any{"type": "object", "subtype": "error", "description": "Error: 의도된 예외"}}})
	e.waitEvent(t, evConsole)
	e.waitEvent(t, evConsole)

	page, err := e.m.Console(s.DebugID, ConsoleQuery{})
	if err != nil {
		t.Fatalf("console: %v", err)
	}
	if len(page.Entries) != 2 {
		t.Fatalf("console entries = %d", len(page.Entries))
	}
	if page.Entries[0].Text != "안녕하세요 3" || page.Entries[0].Source != exceptionSourceConsole {
		t.Fatalf("console entry 0 = %+v", page.Entries[0])
	}
	if page.Entries[1].Source != exceptionSourceException || page.Entries[1].Level != "error" || page.Entries[1].Text != "Error: 의도된 예외" {
		t.Fatalf("exception entry = %+v", page.Entries[1])
	}
	if *page.Entries[1].Line != 5 {
		t.Fatalf("exception line = %d, want 5", *page.Entries[1].Line)
	}
}

func TestCloseCleansOnlyOwnResources(t *testing.T) {
	node := nodeServer(t)
	browser := newCDPServer(t, "Chrome/120")
	e, s := launchBoth(t, node, browser, false)

	// A tunnel of another owner must survive the session's close.
	other, err := e.tuns.Open(e.a, tunnel.OpenParams{RemotePort: closedPort(t), Owner: tunnel.OwnerUser})
	if err != nil {
		t.Fatalf("other tunnel: %v", err)
	}
	before := len(e.tuns.List(""))
	if before < 3 {
		t.Fatalf("expected the session's two tunnels and the other, got %d", before)
	}

	snap, _, err := e.m.Close(bctx(t), s.DebugID, false, testCaller)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if snap.State != StateClosed || snap.CloseReason != CloseRequested {
		t.Fatalf("closed session = %+v", snap)
	}
	list := e.tuns.List("")
	if len(list) != 1 || list[0].TunnelID != other.TunnelID {
		t.Fatalf("close removed the wrong tunnels: %+v", list)
	}
	if len(e.a.kills) != 1 || e.a.kills[0] != 4321 {
		t.Fatalf("launch tree was not ended once: %v", e.a.kills)
	}
	if _, err := e.m.State(s.DebugID, "node-1"); err == nil {
		t.Fatal("state on a closed session was not refused")
	}
}

func TestStaleObjectIDsRejected(t *testing.T) {
	node := nodeServer(t)
	browser := newCDPServer(t, "Chrome/120")
	e, s := launchBoth(t, node, browser, false)
	main := mainTarget(t, s)
	node.pauseMain(pauseReasonOther, nil)
	e.waitEvent(t, evPaused)
	if _, err := e.m.Variables(bctx(t), s.DebugID, main, "scope-1", 0, 200); err != nil {
		t.Fatalf("variables while paused: %v", err)
	}
	if _, oe := e.m.Resume(bctx(t), s.DebugID, main, testCaller); oe != nil {
		t.Fatalf("resume: %v", oe.Err)
	}
	node.emitAll("", "Debugger.resumed", map[string]any{})
	e.waitEvent(t, evResumed)
	_, err := e.m.Variables(bctx(t), s.DebugID, main, "scope-1", 0, 200)
	if apierr.CodeOf(err) != wire.CodeStaleTarget {
		t.Fatalf("stale object id was not rejected: %v", err)
	}
}

func TestRendererOnlyAndMainOnlyLaunch(t *testing.T) {
	t.Run("renderer only", func(t *testing.T) {
		browser := newCDPServer(t, "Chrome/120").withPage(page{sessionID: "pg", targetID: "page-1", typ: "page", url: "file:///index.html"})
		e := newDebugEnv(t, closedPort(t), browser.port())
		s, err := e.m.Launch(bctx(t), LaunchParams{HostRef: "h", Path: "App"}, testCaller)
		if err != nil {
			t.Fatalf("launch: %v", err)
		}
		if s.MainUnavailableReason != InspectorDisabled {
			t.Fatalf("main_unavailable_reason = %q", s.MainUnavailableReason)
		}
		if len(s.Targets) != 1 || s.Targets[0].Role != RoleRenderer {
			t.Fatalf("targets = %+v", s.Targets)
		}
	})
	t.Run("main only", func(t *testing.T) {
		node := nodeServer(t)
		e := newDebugEnv(t, node.port(), closedPort(t))
		s, err := e.m.Launch(bctx(t), LaunchParams{HostRef: "h", Path: "App"}, testCaller)
		if err != nil {
			t.Fatalf("launch: %v", err)
		}
		if s.RendererUnavailableReason != RemoteDebuggingDisabled {
			t.Fatalf("renderer_unavailable_reason = %q", s.RendererUnavailableReason)
		}
		if len(s.Targets) != 1 || s.Targets[0].Role != RoleMain {
			t.Fatalf("targets = %+v", s.Targets)
		}
	})
}

func TestExitedEarly(t *testing.T) {
	e := newDebugEnv(t, closedPort(t), closedPort(t))
	code := 0
	e.a.treeDer = func() (bool, *int) { return false, &code }
	_, err := e.m.Launch(bctx(t), LaunchParams{HostRef: "h", Path: "App"}, testCaller)
	oe, ok := AsOpError(err)
	if !ok || oe.Err.Code != wire.CodeExitedEarly {
		t.Fatalf("launch error = %v, want EXITED_EARLY", err)
	}
	if len(e.a.kills) != 1 {
		t.Fatalf("exited-early launch did not clean its tree: %v", e.a.kills)
	}
}

func TestPauseGenAdvancesPerTarget(t *testing.T) {
	node := nodeServer(t)
	browser := newCDPServer(t, "Chrome/120")
	e, s := launchBoth(t, node, browser, false)
	main := mainTarget(t, s)
	last := int64(-1)
	for i := 0; i < 3; i++ {
		node.pauseMain(pauseReasonOther, nil)
		ev := e.waitEvent(t, evPaused)
		var ps PauseState
		b, _ := json.Marshal(ev.Fields["state"])
		_ = json.Unmarshal(b, &ps)
		if ps.Gen <= last {
			t.Fatalf("pause gen did not advance: %d <= %d", ps.Gen, last)
		}
		st, _ := e.m.State(s.DebugID, main)
		if st.Gen != ps.Gen {
			t.Fatalf("state gen %d != event gen %d", st.Gen, ps.Gen)
		}
		last = ps.Gen
		if _, oe := e.m.Resume(bctx(t), s.DebugID, main, testCaller); oe != nil {
			t.Fatalf("resume: %v", oe.Err)
		}
		node.emitAll("", "Debugger.resumed", map[string]any{})
		rev := e.waitEvent(t, evResumed)
		gen := int64(toInt(rev.Fields["gen"]))
		if gen <= last {
			t.Fatalf("resume gen did not advance: %d <= %d", gen, last)
		}
		last = gen
	}
}

func TestPausePendingResumeCancelsTheSpuriousPause(t *testing.T) {
	node := nodeServer(t) // default Debugger.pause emits no paused: it stays pending
	browser := newCDPServer(t, "Chrome/120")
	e, s := launchBoth(t, node, browser, false)
	main := mainTarget(t, s)

	before, _ := e.m.State(s.DebugID, main)
	tg, oe := e.m.Pause(bctx(t), s.DebugID, main, testCaller)
	if oe != nil {
		t.Fatalf("pause: %v", oe.Err)
	}
	if !tg.PausePending || tg.Gen <= before.Gen {
		t.Fatalf("pause did not report pending with a new gen: %+v", tg)
	}
	tg2, oe := e.m.Resume(bctx(t), s.DebugID, main, testCaller)
	if oe != nil {
		t.Fatalf("resume: %v", oe.Err)
	}
	if tg2.PausePending || tg2.Gen <= tg.Gen {
		t.Fatalf("resume did not clear pending with a new gen: %+v", tg2)
	}
	// The pause V8 still delivers is auto-resumed, never surfaced.
	resumes := node.commandCount("Debugger.resume")
	node.pauseMain(pauseReasonOther, nil)
	deadline := time.Now().Add(2 * time.Second)
	for node.commandCount("Debugger.resume") <= resumes {
		if time.Now().After(deadline) {
			t.Fatal("the cancelled pause was not auto-resumed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	st, _ := e.m.State(s.DebugID, main)
	if st.Paused {
		t.Fatalf("the cancelled pause surfaced: %+v", st)
	}
}

// mainTarget returns the id of the main target of a session.
func mainTarget(t *testing.T, s Session) string {
	t.Helper()
	for _, tg := range s.Targets {
		if tg.Role == RoleMain {
			return tg.ID
		}
	}
	t.Fatalf("no main target in %+v", s.Targets)
	return ""
}

func TestSessionListsMainBeforeTheRendererWhateverAttachedFirst(t *testing.T) {
	s := &session{targets: map[string]*targetState{}}
	s.addTargetLocked(&targetState{info: Target{ID: "page-1", Role: RoleRenderer, Type: "page"}})
	s.addTargetLocked(&targetState{info: Target{ID: "page-2", Role: RoleRenderer, Type: "page"}})
	s.addTargetLocked(&targetState{info: Target{ID: "node-1", Role: RoleMain, Type: "node"}})
	got := s.snapshotLocked().Targets
	if len(got) != 3 || got[0].ID != "node-1" || got[1].ID != "page-1" || got[2].ID != "page-2" {
		t.Fatalf("targets = %+v, want node-1 then page-1 and page-2 in the order they attached", got)
	}
}

// waitPumpEnded fails the test unless the console pump of s returns soon.
func waitPumpEnded(t *testing.T, s *session) {
	t.Helper()
	select {
	case <-s.pumpDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the console pump of the closed session is still running")
	}
}

func TestClosingASessionEndsItsConsolePump(t *testing.T) {
	t.Run("close", func(t *testing.T) {
		node := nodeServer(t)
		browser := newCDPServer(t, "Chrome/120")
		e, snap := launchBoth(t, node, browser, false)
		s, err := e.m.get(snap.DebugID)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := e.m.Close(bctx(t), snap.DebugID, false, testCaller); err != nil {
			t.Fatalf("close: %v", err)
		}
		waitPumpEnded(t, s)
	})
	t.Run("failed launch", func(t *testing.T) {
		e := newDebugEnv(t, closedPort(t), closedPort(t))
		code := 0
		e.a.treeDer = func() (bool, *int) { return false, &code }
		snap, err := e.m.Launch(bctx(t), LaunchParams{HostRef: "h", Path: "App"}, testCaller)
		if _, ok := AsOpError(err); !ok {
			t.Fatalf("launch error = %v, want a failed launch", err)
		}
		s, err := e.m.get(snap.DebugID)
		if err != nil {
			t.Fatal(err)
		}
		waitPumpEnded(t, s)
	})
}

func TestPauseOnStartExitedEarly(t *testing.T) {
	e := newDebugEnv(t, closedPort(t), closedPort(t))
	other, err := e.tuns.Open(e.a, tunnel.OpenParams{RemotePort: closedPort(t), Owner: tunnel.OwnerUser})
	if err != nil {
		t.Fatalf("other tunnel: %v", err)
	}
	code := 3
	e.a.treeDer = func() (bool, *int) { return false, &code }
	snap, err := e.m.Launch(bctx(t), LaunchParams{HostRef: "h", Path: "App", PauseOnStart: true}, testCaller)
	oe, ok := AsOpError(err)
	if !ok || oe.Err.Code != wire.CodeExitedEarly {
		t.Fatalf("launch error = %v, want EXITED_EARLY", err)
	}
	if snap.State != StateClosed || snap.CloseReason != CloseProcessExited || snap.ExitCode == nil || *snap.ExitCode != 3 {
		t.Fatalf("session = %+v, want closed by process_exited with exit code 3", snap)
	}
	if ev := e.waitEvent(t, evClosed); toInt(ev.Fields["exit_code"]) != 3 || ev.Fields["reason"] != CloseProcessExited {
		t.Fatalf("debug.closed = %+v", ev.Fields)
	}
	if len(e.a.kills) != 1 || e.a.kills[0] != 4321 {
		t.Fatalf("launch tree was not ended once: %v", e.a.kills)
	}
	if list := e.tuns.List(""); len(list) != 1 || list[0].TunnelID != other.TunnelID {
		t.Fatalf("tunnels after the failed launch = %+v, want only the other owner's", list)
	}
}

func TestPauseOnStartReturnsOnceMainIsAttachedWhileTheRendererWaits(t *testing.T) {
	node := nodeServer(t)
	e := newDebugEnv(t, node.port(), closedPort(t))
	start := time.Now()
	s, err := e.m.Launch(bctx(t), LaunchParams{HostRef: "h", Path: "App", PauseOnStart: true, PauseOnExceptions: "all",
		Breakpoints: []BreakpointSpec{{Match: matchURL, Pattern: "file:///app/main.js", Line: 10}}}, testCaller)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if took := time.Since(start); took >= e.m.tmo.Probe/2 {
		t.Fatalf("launch took %s, want a return once main is attached", took)
	}
	if s.RendererUnavailableReason != RendererWaiting {
		t.Fatalf("renderer_unavailable_reason = %q, want %q", s.RendererUnavailableReason, RendererWaiting)
	}
	if len(s.Targets) != 1 || s.Targets[0].Role != RoleMain || !s.Targets[0].Attached {
		t.Fatalf("targets = %+v, want the attached main", s.Targets)
	}
	if node.commandCount("Debugger.setBreakpointByUrl") != 1 || node.commandCount("Debugger.setPauseOnExceptions") != 1 ||
		node.commandCount("Runtime.runIfWaitingForDebugger") != 1 {
		t.Fatalf("main was not set up and released before the launch returned: breakpoints %d, exceptions %d, run %d",
			node.commandCount("Debugger.setBreakpointByUrl"), node.commandCount("Debugger.setPauseOnExceptions"),
			node.commandCount("Runtime.runIfWaitingForDebugger"))
	}
	final := e.waitSession(t, s.DebugID, func(x Session) bool { return x.Rev > s.Rev && x.RendererUnavailableReason != RendererWaiting })
	if final.RendererUnavailableReason != RemoteDebuggingDisabled {
		t.Fatalf("renderer_unavailable_reason after the wait = %q, want %q", final.RendererUnavailableReason, RemoteDebuggingDisabled)
	}
}

func TestPauseOnStartAttachesTheRendererThatAnswersLater(t *testing.T) {
	node := nodeServer(t)
	browser := newCDPServer(t, "Chrome/120").withPage(page{sessionID: "pg", targetID: "page-1", typ: "page", url: "file:///index.html"})
	release := browser.holdVersion()
	e := newDebugEnv(t, node.port(), browser.port())
	s, err := e.m.Launch(bctx(t), LaunchParams{HostRef: "h", Path: "App", PauseOnStart: true}, testCaller)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if s.RendererUnavailableReason != RendererWaiting {
		t.Fatalf("renderer_unavailable_reason = %q, want %q", s.RendererUnavailableReason, RendererWaiting)
	}
	release()
	e.waitSession(t, s.DebugID, func(x Session) bool {
		if x.Rev <= s.Rev || x.RendererUnavailableReason != "" {
			return false
		}
		for _, tg := range x.Targets {
			if tg.Role == RoleRenderer && tg.ID == "page-1" {
				return true
			}
		}
		return false
	})
}

func TestDebugEventsStoreOnlyTheirIDsAndOrder(t *testing.T) {
	node := nodeServer(t)
	browser := newCDPServer(t, "Chrome/120")
	e, s := launchBoth(t, node, browser, false)
	long := strings.Repeat("콘솔 글 ", 500)
	node.emitAll("", "Runtime.consoleAPICalled", map[string]any{"type": "log",
		"args": []any{map[string]any{"type": "string", "value": long}}})
	console := e.waitEvent(t, evConsole)
	node.pauseMain(pauseReasonOther, nil)
	paused := e.waitEvent(t, evPaused)
	changed := e.waitEvent(t, evSessionChanged)

	stored := func(ev events.Event) map[string]any {
		t.Helper()
		row, err := e.st.GetEvent(ev.Seq)
		if err != nil {
			t.Fatalf("stored event %d: %v", ev.Seq, err)
		}
		if row.Kind != ev.Kind {
			t.Fatalf("stored kind %q, want %q", row.Kind, ev.Kind)
		}
		var m map[string]any
		if err := json.Unmarshal(row.Payload, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	entry := console.Fields["entry"].(ConsoleEntry)
	if got := stored(console); len(got) != 2 || got["debug_id"] != s.DebugID || toInt(got["seq"]) != int(entry.Seq) {
		t.Fatalf("stored debug.console has %d bytes of fields %v, want debug_id and seq %d", len(fmt.Sprint(got)), keysOf(got), entry.Seq)
	}
	ps := paused.Fields["state"].(PauseState)
	if got := stored(paused); len(got) != 4 || got["debug_id"] != s.DebugID || got["target_id"] != ps.TargetID ||
		toInt(got["gen"]) != int(ps.Gen) || got["reason"] != ps.Reason {
		t.Fatalf("stored debug.paused fields %v, want debug_id, target_id, gen and reason of the pause", keysOf(got))
	}
	snap := changed.Fields["session"].(Session)
	if got := stored(changed); len(got) != 3 || got["debug_id"] != s.DebugID || toInt(got["rev"]) != int(snap.Rev) || got["state"] != snap.State {
		t.Fatalf("stored debug.session_changed fields %v, want debug_id, rev and state", keysOf(got))
	}
}

// keysOf returns the sorted keys of m.
func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
