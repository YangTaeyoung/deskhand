package debug

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
)

// setBreakpointResult is the CDP Debugger.setBreakpointByUrl result.
type setBreakpointResult struct {
	BreakpointID string        `json:"breakpointId"`
	Locations    []cdpLocation `json:"locations"`
}

// bpParams returns the Debugger.setBreakpointByUrl params of a breakpoint.
func bpParams(b *Breakpoint) map[string]any {
	p := map[string]any{"lineNumber": b.Line - 1}
	if b.Column != nil {
		p["columnNumber"] = *b.Column - 1
	}
	if b.Condition != "" {
		p["condition"] = b.Condition
	}
	switch b.Match {
	case matchURL:
		p["url"] = b.Pattern
	case matchURLRegex:
		p["urlRegex"] = b.Pattern
	default:
		p["urlRegex"] = fileNameRegex(b.Pattern)
	}
	return p
}

// fileNameRegex builds the urlRegex that matches a URL path ending in
// pattern at a path separator, ignoring a query or fragment.
func fileNameRegex(pattern string) string {
	return `(^|[/\\])` + regexp.QuoteMeta(pattern) + `([?#].*)?$`
}

// applyBreakpointLocked sets b on the attached target t through CDP and
// records its CDP id and resolved locations; the caller holds the lock. An
// invalid url_regex (checked in the target's own engine) is returned as
// INVALID_ARGUMENT so the first target's refusal can surface.
func (s *session) applyBreakpointLocked(ctx context.Context, t *targetState, b *Breakpoint) error {
	if !t.info.Attached || !b.Enabled {
		return nil
	}
	if b.Match == matchURLRegex {
		if err := s.validateRegexLocked(ctx, t, b.Pattern); err != nil {
			return err
		}
	}
	raw, err := t.conn.Call(ctx, t.sessionID, "Debugger.setBreakpointByUrl", bpParams(b))
	if err != nil {
		return err
	}
	var res setBreakpointResult
	if json.Unmarshal(raw, &res) != nil {
		return nil
	}
	t.cdpBreakpoints[b.BreakpointID] = res.BreakpointID
	for _, loc := range res.Locations {
		url := s.scriptURLLocked(t, loc.ScriptID)
		s.addLocationLocked(b, BreakpointLocation{TargetID: t.info.ID, ScriptID: loc.ScriptID, URL: url,
			Line: loc.LineNumber + 1, Column: loc.ColumnNumber + 1})
	}
	return nil
}

// validateRegexLocked checks that pattern compiles in the target's own
// JavaScript engine (Debugger.setBreakpointByUrl accepts an invalid
// urlRegex silently). An engine error is INVALID_ARGUMENT with its
// message; a transport error is not treated as invalid.
func (s *session) validateRegexLocked(ctx context.Context, t *targetState, pattern string) error {
	raw, err := t.conn.Call(ctx, t.sessionID, "Runtime.evaluate",
		map[string]any{"expression": "new RegExp(" + quote(pattern) + ")", "throwOnSideEffect": true})
	if err != nil {
		return nil
	}
	var res struct {
		ExceptionDetails *exceptionDetails `json:"exceptionDetails"`
	}
	if json.Unmarshal(raw, &res) != nil || res.ExceptionDetails == nil {
		return nil
	}
	return apierr.Invalid("url_regex가 올바르지 않습니다: " + firstLine(res.ExceptionDetails.headline()))
}

// removeBreakpointLocked removes b's CDP breakpoint from t.
func (s *session) removeBreakpointLocked(ctx context.Context, t *targetState, b *Breakpoint) {
	cdp := t.cdpBreakpoints[b.BreakpointID]
	if cdp == "" {
		return
	}
	delete(t.cdpBreakpoints, b.BreakpointID)
	_, _ = t.conn.Call(ctx, t.sessionID, "Debugger.removeBreakpoint", map[string]any{"breakpointId": cdp})
}

// dropLocationsLocked removes b's locations on target id.
func (b *Breakpoint) dropLocations(targetID string) {
	kept := b.Locations[:0]
	for _, l := range b.Locations {
		if l.TargetID != targetID {
			kept = append(kept, l)
		}
	}
	b.Locations = kept
	b.Resolved = len(b.Locations) > 0
}

// BreakpointSet adds a breakpoint to the session and applies it to the
// attached targets; the first target that rejects a urlRegex makes it an
// INVALID_ARGUMENT.
func (m *Manager) BreakpointSet(ctx context.Context, debugID string, sp BreakpointSpec, c model.Caller) (Breakpoint, error) {
	s, err := m.live(debugID)
	if err != nil {
		return Breakpoint{}, err
	}
	if err := validSpec(sp); err != nil {
		return Breakpoint{}, err
	}
	s.mu.Lock()
	s.bpSeq++
	b := &Breakpoint{BreakpointID: breakpointID(s.bpSeq), Match: sp.Match, Pattern: sp.Pattern, Line: sp.Line, Column: sp.Column,
		Condition: sp.Condition, Enabled: sp.Enabled == nil || *sp.Enabled, Locations: []BreakpointLocation{}, Rev: 1}
	s.breakpoints = append(s.breakpoints, b)
	targets := s.attachedTargetsLocked()
	s.bumpRev()
	s.mu.Unlock()

	var applyErr error
	for i, t := range targets {
		s.mu.Lock()
		err := s.applyBreakpointLocked(ctx, t, b)
		s.mu.Unlock()
		if err != nil && i == 0 {
			applyErr = err
			break
		}
	}
	if applyErr != nil {
		s.mu.Lock()
		s.dropBreakpointLocked(b.BreakpointID)
		s.bumpRev()
		s.mu.Unlock()
		return Breakpoint{}, apierr.Invalid("중단점을 걸 수 없습니다: " + firstLine(applyErr.Error()))
	}
	s.mu.Lock()
	bp := *b
	s.mu.Unlock()
	s.publish(evBreakpointChanged, map[string]any{"debug_id": s.debugID, "breakpoint": bp, "removed": false})
	s.emitSession()
	m.record(s, c, "중단점 설정: "+sp.Pattern, model.TimelineOK)
	return bp, nil
}

// BreakpointUpdate enables, disables or re-conditions a breakpoint.
func (m *Manager) BreakpointUpdate(ctx context.Context, debugID, bpID string, enabled *bool, condition *string, c model.Caller) (Breakpoint, error) {
	s, err := m.live(debugID)
	if err != nil {
		return Breakpoint{}, err
	}
	s.mu.Lock()
	b := s.breakpointLocked(bpID)
	if b == nil {
		s.mu.Unlock()
		return Breakpoint{}, apierr.NotFound("그런 중단점이 없습니다. debug.get으로 중단점을 확인하세요")
	}
	targets := s.attachedTargetsLocked()
	reapply := false
	if condition != nil && *condition != b.Condition {
		b.Condition = *condition
		reapply = b.Enabled
		for _, t := range targets {
			s.removeBreakpointLocked(ctx, t, b)
		}
		b.dropLocations("")
		b.Locations = []BreakpointLocation{}
		b.Resolved = false
	}
	if enabled != nil && *enabled != b.Enabled {
		b.Enabled = *enabled
		if !b.Enabled {
			for _, t := range targets {
				s.removeBreakpointLocked(ctx, t, b)
			}
			b.Locations = []BreakpointLocation{}
			b.Resolved = false
		} else {
			reapply = true
		}
	}
	b.Rev++
	s.bumpRev()
	s.mu.Unlock()
	if reapply {
		for _, t := range targets {
			s.mu.Lock()
			_ = s.applyBreakpointLocked(ctx, t, b)
			s.mu.Unlock()
		}
	}
	s.mu.Lock()
	bp := *b
	s.mu.Unlock()
	s.publish(evBreakpointChanged, map[string]any{"debug_id": s.debugID, "breakpoint": bp, "removed": false})
	m.record(s, c, "중단점 변경: "+bp.Pattern, model.TimelineOK)
	return bp, nil
}

// BreakpointRemove removes a breakpoint from the session and the targets.
func (m *Manager) BreakpointRemove(ctx context.Context, debugID, bpID string, c model.Caller) (Breakpoint, error) {
	s, err := m.live(debugID)
	if err != nil {
		return Breakpoint{}, err
	}
	s.mu.Lock()
	b := s.breakpointLocked(bpID)
	if b == nil {
		s.mu.Unlock()
		return Breakpoint{}, apierr.NotFound("그런 중단점이 없습니다. debug.get으로 중단점을 확인하세요")
	}
	for _, t := range s.attachedTargetsLocked() {
		s.removeBreakpointLocked(ctx, t, b)
	}
	s.dropBreakpointLocked(bpID)
	b.Rev++
	b.Enabled = false
	s.bumpRev()
	bp := *b
	s.mu.Unlock()
	s.publish(evBreakpointChanged, map[string]any{"debug_id": s.debugID, "breakpoint": bp, "removed": true})
	s.emitSession()
	m.record(s, c, "중단점 삭제: "+bp.Pattern, model.TimelineOK)
	return bp, nil
}

// BreakpointsActivate turns every breakpoint on or off at once.
func (m *Manager) BreakpointsActivate(ctx context.Context, debugID string, active bool, c model.Caller) (Session, error) {
	s, err := m.live(debugID)
	if err != nil {
		return Session{}, err
	}
	s.mu.Lock()
	s.bpActive = active
	targets := s.attachedTargetsLocked()
	s.bumpRev()
	for _, t := range targets {
		_, _ = t.conn.Call(ctx, t.sessionID, "Debugger.setBreakpointsActive", map[string]any{"active": active})
	}
	snap := s.snapshotLocked()
	s.mu.Unlock()
	s.emitSession()
	m.record(s, c, activeLabel(active), model.TimelineOK)
	return snap, nil
}

func activeLabel(active bool) string {
	if active {
		return "중단점 활성화"
	}
	return "중단점 비활성화"
}

// Exceptions sets the pause-on-exceptions mode of the session.
func (m *Manager) Exceptions(ctx context.Context, debugID, mode string, c model.Caller) (Session, error) {
	s, err := m.live(debugID)
	if err != nil {
		return Session{}, err
	}
	normal, err := exceptionsMode(mode)
	if err != nil {
		return Session{}, err
	}
	s.mu.Lock()
	s.exceptions = normal
	targets := s.attachedTargetsLocked()
	s.bumpRev()
	for _, t := range targets {
		_, _ = t.conn.Call(ctx, t.sessionID, "Debugger.setPauseOnExceptions", map[string]any{"state": normal})
	}
	snap := s.snapshotLocked()
	s.mu.Unlock()
	s.emitSession()
	m.record(s, c, "예외 중단: "+normal, model.TimelineOK)
	return snap, nil
}

// dropBreakpointLocked removes a breakpoint from the session list.
func (s *session) dropBreakpointLocked(bpID string) {
	kept := s.breakpoints[:0]
	for _, b := range s.breakpoints {
		if b.BreakpointID != bpID {
			kept = append(kept, b)
		}
	}
	s.breakpoints = kept
}

// breakpointBoundLocked reports whether the breakpoint has a CDP id on any
// attached target.
func (s *session) breakpointBoundLocked(bpID string) bool {
	for _, t := range s.targets {
		if t.cdpBreakpoints[bpID] != "" {
			return true
		}
	}
	return false
}

// attachedTargetsLocked returns the attached targets in creation order.
func (s *session) attachedTargetsLocked() []*targetState {
	var out []*targetState
	for _, id := range s.order {
		if t := s.targets[id]; t != nil && t.info.Attached {
			out = append(out, t)
		}
	}
	return out
}

func breakpointID(seq int) string { return breakpointPrefix + itoa(seq) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
