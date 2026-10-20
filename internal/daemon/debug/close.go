package debug

import (
	"context"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/tunnel"
	"github.com/YangTaeyoung/deskhand/internal/proto"
)

// pendingKill is a launched tree to end once its host returns.
type pendingKill struct {
	pid     int
	started string
}

// Close ends a session: it closes the CDP connections and the session's
// tunnels, ends the launched process tree unless keepProcess is set, and
// tries to close an inspector it turned on for a pid attach.
func (m *Manager) Close(ctx context.Context, debugID string, keepProcess bool, c model.Caller) (Session, bool, error) {
	s, err := m.get(debugID)
	if err != nil {
		return Session{}, false, err
	}
	if s.snapshot().State == StateClosed {
		return s.snapshot(), false, nil
	}
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	leftOpen := s.closeInspectorIfOpened(ctx)
	snap := s.finishClose(CloseRequested, !keepProcess, c)
	m.record(s, c, "디버그 세션 닫기", model.TimelineOK)
	return snap, leftOpen, nil
}

// closeInspectorIfOpened closes the Node inspector the daemon turned on for
// a pid attach, and reports whether it is still open afterward. Node closes
// the inspector by ending the WebSocket without a reply, so the connection
// closing is the success signal.
func (s *session) closeInspectorIfOpened(ctx context.Context) bool {
	s.mu.Lock()
	open := s.kind == KindAttach && s.pid != nil
	var main *targetState
	for _, id := range s.order {
		if t := s.targets[id]; t != nil && t.info.Role == RoleMain && t.info.Attached {
			main = t
			break
		}
	}
	s.mu.Unlock()
	if !open || main == nil {
		return false
	}
	go func() {
		_, _ = main.conn.Call(ctx, main.sessionID, "Runtime.evaluate",
			map[string]any{"expression": "require('node:inspector').close()", "includeCommandLineAPI": true})
	}()
	select {
	case <-main.conn.Done():
		return false
	case <-time.After(2 * time.Second):
		return true
	case <-ctx.Done():
		return !connClosed(main.conn)
	}
}

// killRootParams builds the proc.kill of a launch's process tree, with the
// root's start time so the agent refuses a reused pid.
func killRootParams(pid int, started string) proto.Message {
	p := proto.Message{"pid": pid, "tree": true}
	if started != "" {
		p["started"] = started
	}
	return p
}

// finishClose closes a session once: it ends the connections, the tunnels
// and, for a launch, the process tree, then publishes debug.closed. A
// close the session caused on its own (process exit or a lost connection)
// is recorded on the timeline as the system.
func (s *session) finishClose(reason string, killTree bool, c model.Caller) Session {
	s.mu.Lock()
	if s.closedOnce {
		snap := s.snapshotLocked()
		s.mu.Unlock()
		return snap
	}
	s.closedOnce = true
	s.state = StateClosed
	s.closeReason = reason
	s.closedAt = time.Now()
	mainConn, rendConn := s.mainConn, s.rendConn
	kind := s.kind
	pid := s.pid
	started := s.started
	s.bumpRev()
	snap := s.snapshotLocked()
	rev := s.rev
	exitCode := s.exitCode
	s.mu.Unlock()

	if mainConn != nil {
		mainConn.Close()
	}
	if rendConn != nil {
		rendConn.Close()
	}
	if s.m.d.Tunnels != nil {
		s.m.d.Tunnels.CloseOwner(tunnel.OwnerDebugPrefix + s.debugID)
	}
	if kind == KindLaunch && killTree && pid != nil {
		killCtx, cancel := context.WithTimeout(s.m.base, s.m.tmo.Call)
		_, _ = s.agent.Call(killCtx, "proc.kill", killRootParams(*pid, started), conn.CallOpts{})
		cancel()
	}
	if s.m.d.Jobs != nil && s.jobID != "" {
		status := model.StatusSucceeded
		result := map[string]any{"close_reason": reason}
		if reason != CloseRequested {
			status = model.StatusFailed
		}
		if exitCode != nil {
			result["exit_code"] = *exitCode
		}
		_, _ = s.m.d.Jobs.Mirror(s.jobID, status, result, true)
	}
	fields := map[string]any{"debug_id": s.debugID, "reason": reason, "rev": rev}
	if exitCode != nil {
		fields["exit_code"] = *exitCode
	}
	s.publish(evClosed, fields)
	s.emitSession()
	if reason != CloseRequested {
		s.m.record(s, c, "디버그 세션 종료: "+reason, model.TimelineUnknown)
	}
	return snap
}

// shutdown closes a session's connections when the manager closes, without
// recording an end.
func (s *session) shutdown() {
	s.mu.Lock()
	mainConn, rendConn := s.mainConn, s.rendConn
	s.mu.Unlock()
	if mainConn != nil {
		mainConn.Close()
	}
	if rendConn != nil {
		rendConn.Close()
	}
}

// connectionLost closes a session whose host dropped and, for a launch,
// marks its tree to be ended once the host is back.
func (s *session) connectionLost() {
	s.mu.Lock()
	if s.state == StateClosed {
		s.mu.Unlock()
		return
	}
	launch := s.kind == KindLaunch
	pid := s.pid
	started := s.started
	s.mu.Unlock()
	if launch && pid != nil {
		s.m.markPendingKill(s.hostID, pendingKill{pid: *pid, started: started})
	}
	s.finishClose(CloseConnectionLost, false, model.System)
}

// markPendingKill records a launched tree to end when the host returns.
func (m *Manager) markPendingKill(hostID string, k pendingKill) {
	m.mu.Lock()
	if m.pendingKills == nil {
		m.pendingKills = map[string][]pendingKill{}
	}
	m.pendingKills[hostID] = append(m.pendingKills[hostID], k)
	m.mu.Unlock()
}

// HostReady ends the launched trees of the sessions that the host dropped.
func (m *Manager) HostReady(hostID string) {
	m.mu.Lock()
	kills := m.pendingKills[hostID]
	delete(m.pendingKills, hostID)
	m.mu.Unlock()
	if len(kills) == 0 {
		return
	}
	agent, _, err := m.d.Hosts.Conn(hostID)
	if err != nil {
		return
	}
	for _, k := range kills {
		ctx, cancel := context.WithTimeout(m.base, m.tmo.Call)
		_, _ = agent.Call(ctx, "proc.kill", killRootParams(k.pid, k.started), conn.CallOpts{})
		cancel()
	}
}
