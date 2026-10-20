package debug

import (
	"context"
	"encoding/json"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/proto"
)

// pausedEvent is a CDP Debugger.paused.
type pausedEvent struct {
	CallFrames     []cdpFrame  `json:"callFrames"`
	Reason         string      `json:"reason"`
	Data           *pausedData `json:"data"`
	HitBreakpoints []string    `json:"hitBreakpoints"`
}

type pausedData struct {
	remoteObject
}

type cdpFrame struct {
	CallFrameID  string        `json:"callFrameId"`
	FunctionName string        `json:"functionName"`
	Location     cdpLocation   `json:"location"`
	URL          string        `json:"url"`
	ScopeChain   []cdpScope    `json:"scopeChain"`
	This         *remoteObject `json:"this"`
}

type cdpLocation struct {
	ScriptID     string `json:"scriptId"`
	LineNumber   int    `json:"lineNumber"`
	ColumnNumber int    `json:"columnNumber"`
}

type cdpScope struct {
	Type   string       `json:"type"`
	Object remoteObject `json:"object"`
	Name   string       `json:"name"`
}

// onPaused records a pause: it bumps the target gen, builds the PauseState,
// registers the object ids it issues and publishes debug.paused.
func (s *session) onPaused(t *targetState, raw json.RawMessage) {
	var p pausedEvent
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	s.mu.Lock()
	// A pause that a resume cancelled still arrives once V8 runs code; drop
	// it and resume without surfacing it.
	if t.cancelPause && p.Reason != "exception" && p.Reason != "promiseRejection" && len(p.HitBreakpoints) == 0 && t.expect != pauseReasonStep {
		t.cancelPause = false
		tconn, sid := t.conn, t.sessionID
		s.mu.Unlock()
		_, _ = tconn.Call(context.Background(), sid, "Debugger.resume", nil)
		return
	}
	t.cancelPause = false
	t.info.Gen++
	t.paused, t.pausePending = true, false
	t.info.Paused, t.info.PausePending = true, false
	t.pauseObjects = map[string]int64{}
	gen := t.info.Gen
	reason := s.pauseReasonLocked(t, p)
	hit := []string{}
	for _, cdp := range p.HitBreakpoints {
		if b := s.breakpointByCDP(t, cdp); b != nil {
			hit = append(hit, b.BreakpointID)
		}
	}
	state := &PauseState{TargetID: t.info.ID, Gen: gen, Paused: true, Reason: reason, HitBreakpoints: hit,
		CallFrames: []CallFrame{}}
	if reason == pauseReasonPauseRequest || reason == pauseReasonStep {
		a := s.actorExpectLocked(t)
		state.RequestedBy = &a
	}
	for _, f := range p.CallFrames {
		url := f.URL
		if url == "" {
			url = t.scriptURL[f.Location.ScriptID]
		}
		cf := CallFrame{FrameID: f.CallFrameID, Function: f.FunctionName, ScriptID: f.Location.ScriptID, URL: url,
			Line: f.Location.LineNumber + 1, Column: f.Location.ColumnNumber + 1, ScopeChain: []Scope{}}
		for _, sc := range f.ScopeChain {
			cf.ScopeChain = append(cf.ScopeChain, Scope{Type: cdpScopeType(sc.Type), ObjectID: sc.Object.ObjectID, Name: sc.Name})
			if sc.Object.ObjectID != "" {
				t.pauseObjects[sc.Object.ObjectID] = gen
			}
		}
		if f.This != nil && f.This.Type != "" {
			v := f.This.value()
			cf.This = &v
			s.trackObjectLocked(t, v.ObjectID, false)
		}
		state.CallFrames = append(state.CallFrames, cf)
	}
	if (reason == pauseReasonException || reason == pauseReasonPromiseRejection) && p.Data != nil && p.Data.Type != "" {
		v := p.Data.value()
		state.Exception = &v
		s.trackObjectLocked(t, v.ObjectID, false)
	}
	t.pause = state
	t.expect = ""
	s.recomputeStateLocked()
	st := *state
	s.mu.Unlock()
	s.publish(evPaused, map[string]any{"debug_id": s.debugID, "state": st})
	s.mu.Lock()
	s.emitTargetLocked(t, false)
	s.mu.Unlock()
	s.emitSession()
}

// pauseReasonLocked maps a CDP pause reason to the API reason.
func (s *session) pauseReasonLocked(t *targetState, p pausedEvent) string {
	switch p.Reason {
	case "exception":
		return pauseReasonException
	case "promiseRejection":
		return pauseReasonPromiseRejection
	}
	if len(p.HitBreakpoints) > 0 {
		return pauseReasonBreakpoint
	}
	switch t.expect {
	case pauseReasonPauseRequest, pauseReasonStep, pauseReasonStart:
		return t.expect
	}
	return pauseReasonOther
}

// actorExpectLocked returns the actor that asked for the pending pause or
// step, or the session creator.
func (s *session) actorExpectLocked(t *targetState) Actor {
	if t.expectActor.Actor != "" {
		return t.expectActor
	}
	return s.createdBy
}

// onResumed records a resume: it bumps the gen, clears the pause, drops the
// pause object group and publishes debug.resumed.
func (s *session) onResumed(t *targetState) {
	s.mu.Lock()
	if !t.paused && !t.pausePending {
		s.mu.Unlock()
		return
	}
	t.info.Gen++
	t.paused, t.pausePending = false, false
	t.info.Paused, t.info.PausePending = false, false
	t.pause = nil
	t.pauseObjects = map[string]int64{}
	gen := t.info.Gen
	by := t.resumeBy
	t.resumeBy = Actor{}
	s.recomputeStateLocked()
	s.mu.Unlock()
	fields := map[string]any{"debug_id": s.debugID, "target_id": t.info.ID, "gen": gen}
	if by.Actor != "" {
		fields["by"] = by
	}
	s.publish(evResumed, fields)
	s.mu.Lock()
	s.emitTargetLocked(t, false)
	s.mu.Unlock()
	s.emitSession()
}

// trackObjectLocked records an object id as a pause-gen id (console false)
// or a console-group id.
func (s *session) trackObjectLocked(t *targetState, objectID string, console bool) {
	if objectID == "" {
		return
	}
	if console {
		t.consoleObjects[objectID] = true
		return
	}
	t.pauseObjects[objectID] = t.info.Gen
}

type consoleAPICalled struct {
	Type       string         `json:"type"`
	Args       []remoteObject `json:"args"`
	Timestamp  float64        `json:"timestamp"`
	StackTrace *stackTrace    `json:"stackTrace"`
}

func (s *session) onConsoleAPI(t *targetState, raw json.RawMessage) {
	var p consoleAPICalled
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	e := ConsoleEntry{Time: nowTime(), TargetID: t.info.ID, Source: exceptionSourceConsole, Level: consoleLevel(p.Type),
		Text: consoleText(p.Args)}
	s.applyFrames(&e, p.StackTrace)
	s.enqueueConsole(e)
}

type exceptionThrown struct {
	Timestamp        float64          `json:"timestamp"`
	ExceptionDetails exceptionDetails `json:"exceptionDetails"`
}

func (s *session) onExceptionThrown(t *targetState, raw json.RawMessage) {
	var p exceptionThrown
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	info := p.ExceptionDetails.info()
	e := ConsoleEntry{Time: nowTime(), TargetID: t.info.ID, Source: exceptionSourceException, Level: "error", Text: info.Text,
		ScriptID: info.ScriptID, URL: info.URL, Line: info.Line, Column: info.Column, Stack: info.Stack}
	s.enqueueConsole(e)
}

type logEntryAdded struct {
	Entry logEntry `json:"entry"`
}

type logEntry struct {
	Level      string      `json:"level"`
	Text       string      `json:"text"`
	URL        string      `json:"url"`
	LineNumber int         `json:"lineNumber"`
	StackTrace *stackTrace `json:"stackTrace"`
}

func (s *session) onLogEntry(t *targetState, raw json.RawMessage) {
	var p logEntryAdded
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	line := p.Entry.LineNumber + 1
	e := ConsoleEntry{Time: nowTime(), TargetID: t.info.ID, Source: exceptionSourceBrowser, Level: logLevel(p.Entry.Level),
		Text: cut(p.Entry.Text, consoleTextMax), URL: p.Entry.URL}
	if p.Entry.URL != "" {
		e.Line = &line
	}
	e.Stack = p.Entry.StackTrace.stack()
	s.enqueueConsole(e)
}

// applyFrames sets the call site and stack of a console entry from a stack.
func (s *session) applyFrames(e *ConsoleEntry, st *stackTrace) {
	frames := st.stack()
	e.Stack = frames
	if len(frames) > 0 {
		e.ScriptID, e.URL = frames[0].ScriptID, frames[0].URL
		line, col := frames[0].Line, frames[0].Column
		e.Line, e.Column = &line, &col
	}
}

// enqueueConsole hands an entry to the console pump without blocking the
// CDP dispatch goroutine; a full channel drops the entry and counts it so
// pause and target events are never held up behind console I/O.
func (s *session) enqueueConsole(e ConsoleEntry) {
	select {
	case s.consoleCh <- e:
	default:
		s.mu.Lock()
		s.console.drop(1)
		s.mu.Unlock()
	}
}

// consolePump stores and publishes the queued console entries off the
// dispatch goroutine; it ends with the manager.
func (s *session) consolePump() {
	for {
		select {
		case e := <-s.consoleCh:
			s.mu.Lock()
			stored := s.console.add(e)
			s.mu.Unlock()
			s.publish(evConsole, map[string]any{"debug_id": s.debugID, "entry": stored})
		case <-s.m.base.Done():
			return
		}
	}
}

func nowTime() string { return model.Time(time.Now()) }

func consoleLevel(cdpType string) string {
	switch cdpType {
	case "debug", "trace":
		return "debug"
	case "info":
		return "info"
	case "warning":
		return "warning"
	case "error", "assert":
		return "error"
	default:
		return "log"
	}
}

func logLevel(level string) string {
	switch level {
	case "verbose":
		return "debug"
	case "info":
		return "info"
	case "warning":
		return "warning"
	case "error":
		return "error"
	default:
		return "log"
	}
}

// cdpScopeType maps a CDP scope type to the API type.
func cdpScopeType(t string) string {
	switch t {
	case "wasm-expression-stack":
		return "wasm_expression_stack"
	default:
		return t
	}
}

type breakpointResolved struct {
	BreakpointID string      `json:"breakpointId"`
	Location     cdpLocation `json:"location"`
}

func (s *session) onBreakpointResolved(t *targetState, raw json.RawMessage) {
	var p breakpointResolved
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	s.mu.Lock()
	b := s.breakpointByCDP(t, p.BreakpointID)
	if b == nil {
		s.mu.Unlock()
		return
	}
	url := s.scriptURLLocked(t, p.Location.ScriptID)
	s.addLocationLocked(b, BreakpointLocation{TargetID: t.info.ID, ScriptID: p.Location.ScriptID, URL: url,
		Line: p.Location.LineNumber + 1, Column: p.Location.ColumnNumber + 1})
	bp := *b
	s.mu.Unlock()
	s.publish(evBreakpointChanged, map[string]any{"debug_id": s.debugID, "breakpoint": bp, "removed": false})
}

func (s *session) scriptURLLocked(t *targetState, scriptID string) string {
	for _, sc := range t.scripts {
		if sc.ScriptID == scriptID {
			return sc.URL
		}
	}
	return ""
}

// breakpointByCDP returns the session breakpoint whose CDP id on t is id.
func (s *session) breakpointByCDP(t *targetState, id string) *Breakpoint {
	for our, cdp := range t.cdpBreakpoints {
		if cdp == id {
			return s.breakpointLocked(our)
		}
	}
	return nil
}

func (s *session) breakpointLocked(id string) *Breakpoint {
	for _, b := range s.breakpoints {
		if b.BreakpointID == id {
			return b
		}
	}
	return nil
}

// addLocationLocked adds a location to a breakpoint when it is new and
// raises its rev.
func (s *session) addLocationLocked(b *Breakpoint, loc BreakpointLocation) {
	for _, l := range b.Locations {
		if l == loc {
			return
		}
	}
	b.Locations = append(b.Locations, loc)
	b.Resolved = true
	b.Rev++
}

// callAgentTree polls debug.tree while a launch waits for its ports.
func (s *session) treeStatus(ctx context.Context) (alive bool, code *int) {
	if s.pid == nil {
		return true, nil
	}
	res, err := s.agent.Call(ctx, "debug.tree", proto.Message{"pid": *s.pid}, conn.CallOpts{})
	if err != nil {
		return true, nil
	}
	alive = res.Bool("alive")
	if res.Has("exit_code") {
		n := res.Int("exit_code")
		code = &n
	}
	return alive, code
}
