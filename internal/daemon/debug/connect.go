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
// OpError when no port responds.
func (s *session) connectLaunch(ctx context.Context, mainAddr, rendAddr string) *OpError {
	start := s.pauseOnStart
	if start {
		v, ok, openedMain := s.awaitPort(ctx, mainAddr)
		if ok {
			s.attachMain(ctx, mainAddr, v, true)
		} else {
			s.setMainReason(openedMain)
		}
		v2, ok2, openedR := s.awaitPort(ctx, rendAddr)
		if ok2 {
			s.attachRenderer(ctx, rendAddr, v2)
		} else {
			s.setRendererReason(openedR)
		}
		return s.launchOutcome(ok, ok2)
	}
	type res struct {
		v      jsonVersion
		ok     bool
		opened bool
	}
	mc := make(chan res, 1)
	rc := make(chan res, 1)
	go func() { v, ok, o := s.awaitPort(ctx, mainAddr); mc <- res{v, ok, o} }()
	go func() { v, ok, o := s.awaitPort(ctx, rendAddr); rc <- res{v, ok, o} }()
	m := <-mc
	r := <-rc
	if m.ok {
		s.attachMain(ctx, mainAddr, m.v, false)
	} else {
		s.setMainReason(m.opened)
	}
	if r.ok {
		s.attachRenderer(ctx, rendAddr, r.v)
	} else {
		s.setRendererReason(r.opened)
	}
	return s.launchOutcome(m.ok, r.ok)
}

// launchOutcome returns the failure when no port responded.
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
	if opened {
		s.rendReason = RemoteDebuggingNotResponding
	} else {
		s.rendReason = RemoteDebuggingDisabled
	}
	s.mu.Unlock()
}

var errNoTarget = errors.New("the inspector listed no target")
