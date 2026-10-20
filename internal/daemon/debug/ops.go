package debug

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// attachedTarget returns an attached target for an operation; a missing or
// unattached target is the error the caller surfaces.
func (s *session) attachedTarget(targetID string) (*targetState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.targets[targetID]
	if t == nil {
		return nil, apierr.NotFound("그런 대상이 없습니다. debug.get으로 대상을 확인하세요")
	}
	if !t.info.Attached {
		return nil, apierr.New(wire.CodeInvalidArgument, "그 대상에 아직 붙지 않았습니다. debug.target.attach로 먼저 붙으세요", false, nextAttachTarget)
	}
	return t, nil
}

// State returns the pause state of a target.
func (m *Manager) State(debugID, targetID string) (PauseState, error) {
	s, err := m.live(debugID)
	if err != nil {
		return PauseState{}, err
	}
	t, err := s.attachedTarget(targetID)
	if err != nil {
		return PauseState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.pause != nil {
		return *t.pause, nil
	}
	return PauseState{TargetID: targetID, Gen: t.info.Gen, Paused: false, HitBreakpoints: []string{}, CallFrames: []CallFrame{}}, nil
}

// Pause asks a target to pause.
func (m *Manager) Pause(ctx context.Context, debugID, targetID string, c model.Caller) (Target, *OpError) {
	s, oe := m.liveOp(debugID)
	if oe != nil {
		return Target{}, oe
	}
	t, err := s.attachedTargetOp(targetID)
	if err != nil {
		return Target{}, err
	}
	s.mu.Lock()
	if t.paused {
		info := t.info
		s.mu.Unlock()
		return info, nil
	}
	t.expect = pauseReasonPauseRequest
	t.expectActor = actorOf(c)
	t.cancelPause = false
	if !t.pausePending {
		t.pausePending = true
		t.info.PausePending = true
		t.info.Gen++
		s.emitTargetLocked(t, false)
	}
	tconn, sess := t.conn, t.sessionID
	s.mu.Unlock()
	if _, err := tconn.Call(ctx, sess, "Debugger.pause", nil); err != nil {
		s.mu.Lock()
		if t.pausePending && !t.paused {
			t.pausePending = false
			t.info.PausePending = false
			t.info.Gen++
			t.expect = ""
			s.emitTargetLocked(t, false)
		}
		s.mu.Unlock()
		return Target{}, failed(err)
	}
	m.record(s, c, "일시정지 요청", model.TimelineOK)
	return s.readTarget(targetID), nil
}

// Resume resumes a target.
func (m *Manager) Resume(ctx context.Context, debugID, targetID string, c model.Caller) (Target, *OpError) {
	s, oe := m.liveOp(debugID)
	if oe != nil {
		return Target{}, oe
	}
	t, err := s.attachedTargetOp(targetID)
	if err != nil {
		return Target{}, err
	}
	s.mu.Lock()
	t.resumeBy = actorOf(c)
	t.expect = ""
	if t.pausePending {
		// The pause had not landed; cancel it and auto-resume the spurious
		// pause V8 still delivers when the engine next runs code.
		t.pausePending = false
		t.info.PausePending = false
		t.cancelPause = true
		t.info.Gen++
		s.emitTargetLocked(t, false)
	}
	tconn, sess := t.conn, t.sessionID
	s.mu.Unlock()
	// Resume on a target that is not paused (e.g. a cancelled pending
	// pause) answers an error from V8; the contract keeps resume succeeded.
	_, _ = tconn.Call(ctx, sess, "Debugger.resume", nil)
	m.record(s, c, "재개", model.TimelineOK)
	return s.readTarget(targetID), nil
}

// Step runs one step of a paused target.
func (m *Manager) Step(ctx context.Context, debugID, targetID, kind string, c model.Caller) (Target, *OpError) {
	method := map[string]string{"over": "Debugger.stepOver", "into": "Debugger.stepInto", "out": "Debugger.stepOut"}[kind]
	if method == "" {
		return Target{}, &OpError{Err: apierr.Invalid("step kind는 over, into, out 중 하나여야 합니다")}
	}
	s, oe := m.liveOp(debugID)
	if oe != nil {
		return Target{}, oe
	}
	t, err := s.attachedTargetOp(targetID)
	if err != nil {
		return Target{}, err
	}
	s.mu.Lock()
	if !t.paused {
		s.mu.Unlock()
		return Target{}, &OpError{Err: apierr.New(wire.CodeStaleTarget, "실행 중인 대상은 한 단계 실행할 수 없습니다. 먼저 멈춘 뒤 다시 시도하세요", false, apierr.NextCheckState)}
	}
	t.expect = pauseReasonStep
	t.expectActor = actorOf(c)
	t.resumeBy = actorOf(c)
	tconn, sess := t.conn, t.sessionID
	s.mu.Unlock()
	if _, err := tconn.Call(ctx, sess, method, nil); err != nil {
		s.mu.Lock()
		t.expect = ""
		t.resumeBy = Actor{}
		s.mu.Unlock()
		return Target{}, failed(err)
	}
	m.record(s, c, "한 단계 실행: "+kind, model.TimelineOK)
	return s.readTarget(targetID), nil
}

// readTarget returns a target's info read at response time.
func (s *session) readTarget(targetID string) Target {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.targets[targetID]; t != nil {
		return t.info
	}
	return Target{ID: targetID}
}

// Variables reads the properties of an object or scope.
func (m *Manager) Variables(ctx context.Context, debugID, targetID, objectID string, offset, limit int) (map[string]any, error) {
	s, err := m.live(debugID)
	if err != nil {
		return nil, err
	}
	t, err := s.attachedTarget(targetID)
	if err != nil {
		return nil, err
	}
	if err := s.checkObject(t, objectID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = variablesDefault
	}
	if limit > variablesMax {
		limit = variablesMax
	}
	if offset < 0 {
		offset = 0
	}
	raw, cerr := t.conn.Call(ctx, t.sessionID, "Runtime.getProperties",
		map[string]any{"objectId": objectID, "ownProperties": true, "generatePreview": true})
	if cerr != nil {
		return nil, apierr.Internal("변수를 읽지 못했습니다: " + firstLine(cerr.Error()))
	}
	props := s.properties(t, objectID, raw)
	total := len(props)
	if offset > total {
		offset = total
	}
	end := min(offset+limit, total)
	return map[string]any{"properties": props[offset:end], "total": total}, nil
}

type getPropertiesResult struct {
	Result             []propertyDescriptor `json:"result"`
	InternalProperties []internalProperty   `json:"internalProperties"`
}

type propertyDescriptor struct {
	Name     string        `json:"name"`
	Value    *remoteObject `json:"value"`
	Get      *remoteObject `json:"get"`
	Set      *remoteObject `json:"set"`
	Writable *bool         `json:"writable"`
}

type internalProperty struct {
	Name  string        `json:"name"`
	Value *remoteObject `json:"value"`
}

// properties maps a getProperties result and registers the object ids it
// issues with the group of objectID.
func (s *session) properties(t *targetState, objectID string, raw json.RawMessage) []Property {
	var res getPropertiesResult
	_ = json.Unmarshal(raw, &res)
	s.mu.Lock()
	console := t.consoleObjects[objectID]
	s.mu.Unlock()
	out := []Property{}
	add := func(v RemoteValue) {
		s.mu.Lock()
		s.trackObjectLocked(t, v.ObjectID, console)
		s.mu.Unlock()
	}
	for _, p := range res.Result {
		switch {
		case p.Value != nil:
			v := p.Value.value()
			add(v)
			out = append(out, Property{RemoteValue: v, Name: p.Name, Kind: "property", Writable: p.Writable})
		case p.Get != nil || p.Set != nil:
			out = append(out, Property{RemoteValue: RemoteValue{Type: "undefined", ValuePreview: accessorPreview}, Name: p.Name, Kind: "accessor"})
		}
	}
	for _, p := range res.InternalProperties {
		if p.Value == nil {
			continue
		}
		v := p.Value.value()
		add(v)
		out = append(out, Property{RemoteValue: v, Name: p.Name, Kind: "internal"})
	}
	return out
}

// checkObject refuses an object id that belongs to a finished pause.
func (s *session) checkObject(t *targetState, objectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.consoleObjects[objectID] {
		return nil
	}
	if gen, ok := t.pauseObjects[objectID]; ok && t.paused && gen == t.info.Gen {
		return nil
	}
	return apierr.New(wire.CodeStaleTarget, "그 값은 이 일시정지에서 더 이상 읽을 수 없습니다. 다시 멈춘 뒤 debug.state로 호출 스택을 읽으세요", false, apierr.NextCheckState)
}

// Evaluate evaluates an expression on a target or a call frame.
func (m *Manager) Evaluate(ctx context.Context, debugID, targetID, expr, frameID string, c model.Caller) (Evaluation, *OpError) {
	s, oe := m.liveOp(debugID)
	if oe != nil {
		return Evaluation{}, oe
	}
	t, err := s.attachedTargetOp(targetID)
	if err != nil {
		return Evaluation{}, err
	}
	s.mu.Lock()
	if frameID != "" && !t.paused {
		s.mu.Unlock()
		return Evaluation{}, &OpError{Err: apierr.New(wire.CodeStaleTarget, "실행 중인 대상에서는 프레임 식을 평가할 수 없습니다. 먼저 멈춘 뒤 다시 시도하세요", false, apierr.NextCheckState)}
	}
	tconn, sess := t.conn, t.sessionID
	s.mu.Unlock()
	m.record(s, c, "식 평가: "+cut(expr, 200), model.TimelineOK)

	ectx, cancel := context.WithTimeout(ctx, m.tmo.Evaluate)
	defer cancel()
	var raw json.RawMessage
	var cerr error
	if frameID != "" {
		raw, cerr = tconn.Call(ectx, sess, "Debugger.evaluateOnCallFrame",
			map[string]any{"callFrameId": frameID, "expression": expr, "objectGroup": consoleGroup, "generatePreview": true, "includeCommandLineAPI": true})
	} else {
		raw, cerr = tconn.Call(ectx, sess, "Runtime.evaluate",
			map[string]any{"expression": expr, "replMode": true, "includeCommandLineAPI": true, "objectGroup": consoleGroup, "generatePreview": true})
	}
	if cerr != nil {
		if ectx.Err() != nil {
			_, _ = tconn.Call(context.Background(), sess, "Runtime.terminateExecution", nil)
			return Evaluation{}, &OpError{Err: apierr.New(wire.CodeTimeout, "식 평가가 30초를 넘겨 중단했습니다. 더 간단한 식으로 다시 시도하세요", true, apierr.NextRetry)}
		}
		return Evaluation{}, &OpError{Err: apierr.Internal("식을 평가하지 못했습니다: " + firstLine(cerr.Error()))}
	}
	var res struct {
		Result           remoteObject      `json:"result"`
		ExceptionDetails *exceptionDetails `json:"exceptionDetails"`
	}
	_ = json.Unmarshal(raw, &res)
	v := res.Result.value()
	s.mu.Lock()
	s.trackObjectLocked(t, v.ObjectID, true)
	s.mu.Unlock()
	out := Evaluation{RemoteValue: v}
	if res.ExceptionDetails != nil {
		out.Exception = res.ExceptionDetails.info()
		if res.ExceptionDetails.Exception != nil {
			tv := res.ExceptionDetails.Exception.value()
			s.mu.Lock()
			s.trackObjectLocked(t, tv.ObjectID, true)
			s.mu.Unlock()
			out.RemoteValue = tv
		}
	}
	return out, nil
}

// Scripts returns the loaded scripts with a URL, of one target or all.
func (m *Manager) Scripts(debugID, targetID string) (map[string]any, error) {
	s, err := m.live(debugID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	scripts := []Script{}
	for _, id := range s.order {
		t := s.targets[id]
		if t == nil || (targetID != "" && id != targetID) {
			continue
		}
		scripts = append(scripts, t.scripts...)
	}
	return map[string]any{"scripts": scripts}, nil
}

// Source returns the text of a script, truncated at 2 MiB.
func (m *Manager) Source(ctx context.Context, debugID, targetID, scriptID string) (Source, error) {
	s, err := m.live(debugID)
	if err != nil {
		return Source{}, err
	}
	t, err := s.attachedTarget(targetID)
	if err != nil {
		return Source{}, err
	}
	s.mu.Lock()
	url := s.scriptURLLocked(t, scriptID)
	_, known := t.scriptURL[scriptID]
	s.mu.Unlock()
	if !known {
		return Source{}, apierr.NotFound("그런 스크립트가 없습니다. debug.scripts로 script_id를 확인하세요")
	}
	raw, cerr := t.conn.Call(ctx, t.sessionID, "Debugger.getScriptSource", map[string]any{"scriptId": scriptID})
	if cerr != nil {
		return Source{}, apierr.Internal("소스를 읽지 못했습니다: " + firstLine(cerr.Error()))
	}
	var res struct {
		ScriptSource string `json:"scriptSource"`
	}
	_ = json.Unmarshal(raw, &res)
	text, truncated := truncateUTF8(res.ScriptSource, sourceMax)
	return Source{ScriptID: scriptID, URL: url, Text: text, Truncated: truncated}, nil
}

// truncateUTF8 cuts s to at most max bytes at a rune boundary.
func truncateUTF8(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// Console returns a page of the session console.
func (m *Manager) Console(debugID string, q ConsoleQuery) (ConsolePage, error) {
	s, err := m.live(debugID)
	if err != nil {
		return ConsolePage{}, err
	}
	if err := checkQuery(q); err != nil {
		return ConsolePage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.console.page(q), nil
}

// ConsoleClear clears the session console and the console object group.
func (m *Manager) ConsoleClear(ctx context.Context, debugID string, c model.Caller) (int64, error) {
	s, err := m.live(debugID)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	through := s.console.clear()
	var targets []*targetState
	for _, id := range s.order {
		if t := s.targets[id]; t != nil && t.info.Attached {
			targets = append(targets, t)
			t.consoleObjects = map[string]bool{}
		}
	}
	s.mu.Unlock()
	for _, t := range targets {
		_, _ = t.conn.Call(ctx, t.sessionID, "Runtime.releaseObjectGroup", map[string]any{"objectGroup": consoleGroup})
	}
	s.publish(evConsoleCleared, map[string]any{"debug_id": s.debugID, "through_seq": through})
	m.record(s, c, "콘솔 지우기", model.TimelineOK)
	return through, nil
}

// liveOp returns a session for an execution-control op; a closed session is
// a failed envelope.
func (m *Manager) liveOp(debugID string) (*session, *OpError) {
	s, err := m.live(debugID)
	if err != nil {
		return nil, &OpError{Err: apierr.As(err)}
	}
	if s.snapshot().State == StateClosed {
		return nil, &OpError{Err: notFound(debugID)}
	}
	return s, nil
}

// attachedTargetOp returns an attached target for an execution-control op.
func (s *session) attachedTargetOp(targetID string) (*targetState, *OpError) {
	t, err := s.attachedTarget(targetID)
	if err != nil {
		return nil, &OpError{Err: apierr.As(err)}
	}
	return t, nil
}
