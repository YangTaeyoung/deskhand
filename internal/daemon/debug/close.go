package debug

import (
	"context"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/tunnel"
	"github.com/YangTaeyoung/deskhand/internal/proto"
)

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
	leftOpen := s.closeInspectorIfOpened(ctx)
	snap := s.finishClose(CloseRequested, !keepProcess)
	m.record(s, c, "디버그 세션 닫기", model.TimelineOK)
	return snap, leftOpen, nil
}

// closeInspectorIfOpened closes the Node inspector the daemon turned on for
// a pid attach, and reports whether it could not.
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
	_, err := main.conn.Call(ctx, main.sessionID, "Runtime.evaluate",
		map[string]any{"expression": "require('node:inspector').close()", "includeCommandLineAPI": true})
	return err != nil
}

// finishClose closes a session once: it ends the connections, the tunnels
// and, for a launch, the process tree, then publishes debug.closed.
func (s *session) finishClose(reason string, killTree bool) Session {
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
		_, _ = s.agent.Call(killCtx, "proc.kill", proto.Message{"pid": *pid, "tree": true}, conn.CallOpts{})
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
	s.mu.Unlock()
	if launch && pid != nil {
		s.m.markPendingKill(s.hostID, *pid)
	}
	s.finishClose(CloseConnectionLost, false)
}

// markPendingKill records a launched tree to end when the host returns.
func (m *Manager) markPendingKill(hostID string, pid int) {
	m.mu.Lock()
	if m.pendingKills == nil {
		m.pendingKills = map[string][]int{}
	}
	m.pendingKills[hostID] = append(m.pendingKills[hostID], pid)
	m.mu.Unlock()
}

// HostReady ends the launched trees of the sessions that the host dropped.
func (m *Manager) HostReady(hostID string) {
	m.mu.Lock()
	pids := m.pendingKills[hostID]
	delete(m.pendingKills, hostID)
	m.mu.Unlock()
	if len(pids) == 0 {
		return
	}
	agent, _, err := m.d.Hosts.Conn(hostID)
	if err != nil {
		return
	}
	for _, pid := range pids {
		ctx, cancel := context.WithTimeout(m.base, m.tmo.Call)
		_, _ = agent.Call(ctx, "proc.kill", proto.Message{"pid": pid, "tree": true}, conn.CallOpts{})
		cancel()
	}
}
