package debug

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// attachMain connects to the Node inspector at addr and attaches the main
// target. start marks the --inspect-brk first-line stop.
func (s *session) attachMain(ctx context.Context, addr string, v jsonVersion, start bool) {
	var targets []jsonTarget
	if err := getJSON(ctx, addr, "/json/list", &targets); err != nil || len(targets) == 0 {
		s.setMainReason(true)
		return
	}
	t0 := targets[0]
	path, err := wsPath(t0.WebSocketDebuggerURL)
	if err != nil {
		s.setMainReason(true)
		return
	}
	cdp, err := s.m.dial(ctx, addr, path, s.handler(true))
	if err != nil {
		s.setMainReason(true)
		return
	}
	s.mu.Lock()
	s.mainConn = cdp
	t := &targetState{info: Target{ID: t0.ID, Role: RoleMain, Type: "node", URL: t0.URL, Title: t0.Title, Attached: true}, conn: cdp}
	s.addTargetLocked(t)
	if s.name == "" {
		s.name = strings.TrimSuffix(browserName(v.Browser), ".exe")
	}
	s.enableTargetLocked(ctx, t, start, start)
	s.mainReason = ""
	s.bumpRev()
	s.mu.Unlock()
	s.emitSession()
	s.mu.Lock()
	s.emitTargetLocked(t, false)
	s.mu.Unlock()
	go s.watchConn(cdp, true)
}

// attachRenderer connects to the DevTools browser endpoint at addr and
// turns on auto-attach so pages attach as flattened sessions.
func (s *session) attachRenderer(ctx context.Context, addr string, v jsonVersion) {
	path, err := wsPath(v.WebSocketDebuggerURL)
	if err != nil {
		s.setRendererReason(true)
		return
	}
	cdp, err := s.m.dial(ctx, addr, path, s.handler(false))
	if err != nil {
		s.setRendererReason(true)
		return
	}
	s.mu.Lock()
	s.rendConn = cdp
	s.rendReason = ""
	s.bumpRev()
	s.mu.Unlock()
	s.emitSession()
	if _, err := cdp.Call(ctx, "", "Target.setAutoAttach",
		map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true}); err != nil {
		s.setRendererReason(true)
	}
	go s.watchConn(cdp, false)
}

type attachedToTarget struct {
	SessionID          string     `json:"sessionId"`
	TargetInfo         targetInfo `json:"targetInfo"`
	WaitingForDebugger bool       `json:"waitingForDebugger"`
}

type targetInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	URL      string `json:"url"`
}

// onAttached records a target auto-attached through the renderer
// connection: a page is fully attached, other kinds are listed only.
func (s *session) onAttached(raw json.RawMessage) {
	var p attachedToTarget
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.m.base, s.m.tmo.Call)
	defer cancel()
	s.mu.Lock()
	if s.rendConn == nil {
		s.mu.Unlock()
		return
	}
	role := RoleRenderer
	page := p.TargetInfo.Type == "page"
	t := &targetState{info: Target{ID: p.TargetInfo.TargetID, Role: role, Type: cdpTargetType(p.TargetInfo.Type),
		URL: p.TargetInfo.URL, Title: p.TargetInfo.Title, Attached: page}, conn: s.rendConn, sessionID: p.SessionID}
	s.addTargetLocked(t)
	if page {
		s.enableTargetLocked(ctx, t, true, false)
	} else if p.WaitingForDebugger {
		_, _ = s.rendConn.Call(ctx, p.SessionID, "Runtime.runIfWaitingForDebugger", nil)
	}
	s.bumpRev()
	s.emitTargetLocked(t, false)
	s.mu.Unlock()
	s.emitSession()
}

func (s *session) onDetached(raw json.RawMessage) {
	var p struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	s.mu.Lock()
	var removed *targetState
	for _, id := range s.order {
		if t := s.targets[id]; t != nil && t.conn == s.rendConn && t.sessionID == p.SessionID {
			removed = t
			break
		}
	}
	if removed == nil {
		s.mu.Unlock()
		return
	}
	s.removeTargetLocked(removed.info.ID)
	s.bumpRev()
	s.emitTargetLocked(removed, true)
	s.mu.Unlock()
	s.emitSession()
}

func (s *session) onTargetInfo(raw json.RawMessage) {
	var p struct {
		TargetInfo targetInfo `json:"targetInfo"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	s.mu.Lock()
	t := s.targets[p.TargetInfo.TargetID]
	if t == nil {
		s.mu.Unlock()
		return
	}
	changed := t.info.Title != p.TargetInfo.Title || t.info.URL != p.TargetInfo.URL
	t.info.Title, t.info.URL = p.TargetInfo.Title, p.TargetInfo.URL
	if changed {
		s.emitTargetLocked(t, false)
	}
	s.mu.Unlock()
}

// removeTargetLocked drops a target from the session.
func (s *session) removeTargetLocked(id string) {
	delete(s.targets, id)
	kept := s.order[:0]
	for _, o := range s.order {
		if o != id {
			kept = append(kept, o)
		}
	}
	s.order = kept
	for _, b := range s.breakpoints {
		b.dropLocations(id)
	}
	s.recomputeStateLocked()
}

// enableTargetLocked turns on the debugging domains of t, sets the
// session's exceptions mode and breakpoints, and releases it when run is
// set; start marks the first-line stop. The caller holds the lock.
func (s *session) enableTargetLocked(ctx context.Context, t *targetState, run, start bool) {
	_, _ = t.conn.Call(ctx, t.sessionID, "Runtime.enable", nil)
	_, _ = t.conn.Call(ctx, t.sessionID, "Debugger.enable", nil)
	if t.info.Role == RoleRenderer {
		_, _ = t.conn.Call(ctx, t.sessionID, "Log.enable", nil)
	}
	_, _ = t.conn.Call(ctx, t.sessionID, "Debugger.setPauseOnExceptions", map[string]any{"state": s.exceptions})
	if !s.bpActive {
		_, _ = t.conn.Call(ctx, t.sessionID, "Debugger.setBreakpointsActive", map[string]any{"active": false})
	}
	for _, b := range append([]*Breakpoint{}, s.breakpoints...) {
		if err := s.applyBreakpointLocked(ctx, t, b); err != nil && apierr.CodeOf(err) == wire.CodeInvalidArgument && !s.breakpointBoundLocked(b.BreakpointID) {
			s.dropBreakpointLocked(b.BreakpointID)
			b.Rev++
			b.Enabled = false
			bp := *b
			s.publish(evBreakpointChanged, map[string]any{"debug_id": s.debugID, "breakpoint": bp, "removed": true,
				"error": map[string]any{"code": wire.CodeInvalidArgument, "message": firstLine(err.Error())}})
		}
	}
	if run {
		if start {
			t.expect = pauseReasonStart
		}
		_, _ = t.conn.Call(ctx, t.sessionID, "Runtime.runIfWaitingForDebugger", nil)
	}
}

// watchConn closes the session when a connection ends while no other side
// is up. The host connection decides between the two self-closes: when the
// agent is not ready it is a lost connection (and connectionLost agrees
// with HostDropped and marks the tree); only an agent that is still ready
// is a process that exited. A close already under way owns the session.
func (s *session) watchConn(cdp *cdpConn, main bool) {
	select {
	case <-cdp.Done():
	case <-s.m.base.Done():
		return
	}
	s.mu.Lock()
	if s.state == StateClosed || s.closing {
		s.mu.Unlock()
		return
	}
	otherUp := (main && s.rendConn != nil && !connClosed(s.rendConn)) || (!main && s.mainConn != nil && !connClosed(s.mainConn))
	s.mu.Unlock()
	if otherUp {
		return
	}
	if st := s.agent.State(); st != conn.StateReady && st != conn.StateDegraded {
		s.connectionLost()
		return
	}
	s.finishClose(CloseProcessExited, false, model.System)
}

func connClosed(c *cdpConn) bool {
	select {
	case <-c.Done():
		return true
	default:
		return false
	}
}

func cdpTargetType(t string) string {
	switch t {
	case "page", "iframe", "webview", "worker", "service_worker", "shared_worker":
		return t
	case "background_page", "browser", "other":
		return "other"
	default:
		return "other"
	}
}

func browserName(b string) string {
	if i := strings.IndexByte(b, '/'); i > 0 {
		return b[:i]
	}
	if b == "" {
		return "node"
	}
	return b
}

// TargetAttach attaches a target the session listed but did not attach on
// its own.
func (m *Manager) TargetAttach(ctx context.Context, debugID, targetID string, c model.Caller) (Target, error) {
	s, err := m.live(debugID)
	if err != nil {
		return Target{}, err
	}
	s.mu.Lock()
	t := s.targets[targetID]
	if t == nil {
		s.mu.Unlock()
		return Target{}, apierr.NotFound("그런 대상이 없습니다. debug.get으로 대상을 확인하세요")
	}
	if t.info.Attached {
		info := t.info
		s.mu.Unlock()
		return info, nil
	}
	if t.conn == nil {
		s.mu.Unlock()
		return Target{}, &OpError{Err: apierr.New(wire.CodeStaleTarget, "그 대상의 연결이 사라졌습니다. debug.get으로 대상을 다시 확인하세요", false, apierr.NextCheckState)}
	}
	t.info.Attached = true
	s.enableTargetLocked(ctx, t, false, false)
	s.bumpRev()
	info := t.info
	s.emitTargetLocked(t, false)
	s.mu.Unlock()
	s.emitSession()
	m.record(s, c, "대상 부착: "+targetID, model.TimelineOK)
	return info, nil
}
