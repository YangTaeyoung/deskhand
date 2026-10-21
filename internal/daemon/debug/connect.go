package debug

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/tunnel"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// openTunnel opens a tunnel owned by the session to the PC port remote and
// returns its local address.
func (s *session) openTunnel(remote int) (string, error) {
	info, err := s.m.d.Tunnels.Open(s.agent, tunnel.OpenParams{RemotePort: remote, Owner: tunnel.OwnerDebugPrefix + s.debugID})
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.tunnels = append(s.tunnels, info.TunnelID)
	s.mu.Unlock()
	return info.LocalAddress, nil
}

// reachable reports whether the PC port answers through the tunnel local
// addr: it dials and reads briefly; an immediate end means the PC port has
// no listener.
func reachable(ctx context.Context, addr string) bool {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := c.Write([]byte("HEAD /json/version HTTP/1.0\r\n\r\n")); err != nil {
		return false
	}
	buf := make([]byte, 1)
	_, err = c.Read(buf)
	if err == nil {
		return true
	}
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

// awaitPort polls addr for /json/version until the deadline, returning the
// version and whether the PC port was ever reachable. It stops early when
// the launched tree is gone.
func (s *session) awaitPort(ctx context.Context, addr string) (jsonVersion, bool, bool) {
	deadline := time.Now().Add(s.m.tmo.Probe)
	everReachable := false
	for {
		var v jsonVersion
		if err := getJSON(ctx, addr, "/json/version", &v); err == nil {
			return v, true, true
		}
		if reachable(ctx, addr) {
			everReachable = true
		}
		if s.launchTreeGone(ctx) {
			return jsonVersion{}, false, everReachable
		}
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			return jsonVersion{}, false, everReachable
		}
		if sleep(ctx, s.m.tmo.Poll) != nil {
			return jsonVersion{}, false, everReachable
		}
	}
}

// launchTreeGone reports whether a launch session's process tree has ended;
// it records the exit code when it has.
func (s *session) launchTreeGone(ctx context.Context) bool {
	if s.kind != KindLaunch || s.pid == nil {
		return false
	}
	alive, code := s.treeStatus(ctx)
	if !alive {
		s.mu.Lock()
		if code != nil && s.exitCode == nil {
			s.exitCode = code
		}
		s.mu.Unlock()
		return true
	}
	return false
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// connectLaunch connects to the inspector ports of a launch per §9 and
// fills the session targets and unavailability reasons. It returns an
// OpError when no side attaches. Without pause_on_start both ports are
// awaited together and each side attaches as soon as it answers, applying
// the breakpoints and exception mode immediately.
func (s *session) connectLaunch(ctx context.Context, mainAddr, rendAddr string) *OpError {
	if s.pauseOnStart {
		return s.connectPaused(ctx, mainAddr, rendAddr)
	}
	mc := make(chan bool, 1)
	rc := make(chan bool, 1)
	go func() { mc <- s.connectMain(ctx, mainAddr, false) }()
	go func() { rc <- s.connectRenderer(ctx, rendAddr) }()
	mainOK := <-mc
	rendOK := <-rc
	return s.launchOutcome(mainOK, rendOK)
}

// connectPaused connects a pause_on_start launch. It waits for main first;
// once main is attached, set up and released to its first-line stop, it
// returns with renderer_unavailable_reason RendererWaiting and awaits the
// renderer in the background. When main does not attach it awaits the
// renderer and returns the launch outcome.
func (s *session) connectPaused(ctx context.Context, mainAddr, rendAddr string) *OpError {
	if !s.connectMain(ctx, mainAddr, true) {
		return s.launchOutcome(false, s.connectRenderer(ctx, rendAddr))
	}
	s.mu.Lock()
	s.rendReason = RendererWaiting
	s.bumpRev()
	s.mu.Unlock()
	go s.awaitRenderer(rendAddr)
	return nil
}

// connectMain waits for the inspector port at addr and attaches main; start
// marks the --inspect-brk first-line stop. It reports whether main
// attached and records main_unavailable_reason when it did not.
func (s *session) connectMain(ctx context.Context, addr string, start bool) bool {
	v, ok, opened := s.awaitPort(ctx, addr)
	if !ok {
		s.setMainReason(opened)
		return false
	}
	return s.attachMain(ctx, addr, v, start)
}

// connectRenderer waits for the DevTools port at addr and attaches the
// renderer. It reports whether the renderer attached; when it did not it
// records renderer_unavailable_reason and starts retryRenderer.
func (s *session) connectRenderer(ctx context.Context, addr string) bool {
	v, ok, opened := s.awaitPort(ctx, addr)
	if ok && s.attachRenderer(ctx, addr, v) {
		return true
	}
	if !ok {
		s.setRendererReason(opened)
	}
	go s.retryRenderer(addr)
	return false
}

// awaitRenderer attaches the renderer of an open pause_on_start session
// once its port answers within the probe time. Otherwise it sets the final
// renderer_unavailable_reason, raises the rev, publishes
// debug.session_changed and starts retryRenderer. It stops when the session
// finishes.
func (s *session) awaitRenderer(addr string) {
	ctx, cancel := context.WithCancel(s.m.base)
	defer cancel()
	go func() {
		select {
		case <-s.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	v, ok, opened := s.awaitPort(ctx, addr)
	if ok && s.attachRenderer(ctx, addr, v) {
		return
	}
	s.mu.Lock()
	if s.closedOnce || s.closing {
		s.mu.Unlock()
		return
	}
	if !ok {
		s.setRendererReasonLocked(opened)
	}
	s.bumpRev()
	s.mu.Unlock()
	s.emitSession()
	go s.retryRenderer(addr)
}

// retryRenderer keeps probing the renderer port while the session is open
// and attaches it when it answers, clearing renderer_unavailable_reason.
func (s *session) retryRenderer(rendAddr string) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.m.base.Done():
			return
		case <-t.C:
		}
		s.mu.Lock()
		done := s.state == StateClosed || s.closing || s.rendConn != nil
		s.mu.Unlock()
		if done {
			return
		}
		ctx, cancel := context.WithTimeout(s.m.base, s.m.tmo.Probe)
		var v jsonVersion
		if getJSON(ctx, rendAddr, "/json/version", &v) == nil {
			s.attachRenderer(ctx, rendAddr, v)
			cancel()
			return
		}
		cancel()
	}
}

// launchOutcome returns the failure when no side attached.
func (s *session) launchOutcome(mainOK, rendOK bool) *OpError {
	if mainOK || rendOK {
		return nil
	}
	s.mu.Lock()
	code := s.exitCode
	s.mu.Unlock()
	if code != nil {
		return &OpError{Err: apierr.New(wire.CodeExitedEarly,
			"앱이 디버거에 붙기 전에 종료됐습니다(같은 앱이 이미 실행 중이라 단일 인스턴스 잠금에 걸렸을 수 있습니다). 실행 중인 앱을 닫고 다시 시도하세요", false, apierr.NextCheckState)}
	}
	return &OpError{Err: apierr.New(wire.CodeTimeout,
		"디버거 포트가 20초 안에 응답하지 않았습니다. 앱이 --inspect로 실행됐는지 확인하고 다시 시도하세요", true, apierr.NextRetry)}
}

func (s *session) setMainReason(opened bool) {
	s.mu.Lock()
	if opened {
		s.mainReason = InspectorNotResponding
	} else {
		s.mainReason = InspectorDisabled
	}
	s.mu.Unlock()
}

func (s *session) setRendererReason(opened bool) {
	s.mu.Lock()
	s.setRendererReasonLocked(opened)
	s.mu.Unlock()
}

// setRendererReasonLocked records why the renderer port did not answer; the
// caller holds the lock.
func (s *session) setRendererReasonLocked(opened bool) {
	if opened {
		s.rendReason = RemoteDebuggingNotResponding
	} else {
		s.rendReason = RemoteDebuggingDisabled
	}
}

var errNoTarget = errors.New("the inspector listed no target")
