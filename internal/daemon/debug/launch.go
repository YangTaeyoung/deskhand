package debug

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/jobs"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/tunnel"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Launch starts an Electron app on the host with its inspector ports and
// connects to it. On success it returns the session; a launch that fails on
// the PC or its debugger returns an *OpError, and a bad request a plain
// error.
func (m *Manager) Launch(ctx context.Context, p LaunchParams, c model.Caller) (Session, error) {
	if strings.TrimSpace(p.Path) == "" {
		return Session{}, apierr.Invalid("path가 없습니다. 실행할 Electron 앱의 경로를 주세요")
	}
	mode, err := exceptionsMode(p.PauseOnExceptions)
	if err != nil {
		return Session{}, err
	}
	if err := validSpecs(p.Breakpoints); err != nil {
		return Session{}, err
	}
	agent, host, err := m.d.Hosts.Conn(p.HostRef)
	if err != nil {
		return Session{}, err
	}
	s, err := m.newSession(KindLaunch, host, agent, c)
	if err != nil {
		return Session{}, err
	}
	s.mu.Lock()
	s.path, s.args, s.cwd, s.pauseOnStart, s.exceptions = p.Path, append([]string{}, p.Args...), p.Cwd, p.PauseOnStart, mode
	s.name = strings.TrimSuffix(filepath.Base(strings.ReplaceAll(p.Path, "\\", "/")), ".exe")
	s.setSpecsLocked(p.Breakpoints)
	s.mu.Unlock()
	m.startJob(s, "디버그 실행: "+s.name)

	callCtx, cancel := context.WithTimeout(ctx, m.tmo.Call)
	params := proto.Message{"path": p.Path, "pause_on_start": p.PauseOnStart}
	if len(p.Args) > 0 {
		params["args"] = toAnySlice(p.Args)
	}
	if p.Cwd != "" {
		params["cwd"] = p.Cwd
	}
	info := conn.CallInfo{}
	res, cerr := agent.Call(callCtx, "debug.electron.launch", params, conn.CallOpts{Info: &info})
	cancel()
	if cerr != nil {
		return m.launchFailed(s, c, outcomeError(info, cerr))
	}
	pid := res.Int("pid")
	s.mu.Lock()
	s.pid = &pid
	ports, _ := res["ports"].(map[string]any)
	s.ports = Ports{Inspect: intField(ports, "inspect"), RemoteDebugging: intField(ports, "remote_debugging")}
	p1, p2 := s.ports.Inspect, s.ports.RemoteDebugging
	s.mu.Unlock()

	mainAddr, err := s.openTunnel(p1)
	if err != nil {
		return m.launchFailed(s, c, &OpError{Err: apierr.As(err)})
	}
	rendAddr, err := s.openTunnel(p2)
	if err != nil {
		return m.launchFailed(s, c, &OpError{Err: apierr.As(err)})
	}

	connCtx, cancel2 := context.WithTimeout(m.base, m.tmo.Probe*3)
	oe := s.connectLaunch(connCtx, mainAddr, rendAddr)
	cancel2()
	if oe != nil {
		return m.launchFailed(s, c, oe)
	}
	s.emitSession()
	m.record(s, c, "디버그 실행: "+s.name, model.TimelineOK)
	return s.snapshot(), nil
}

// Attach attaches to a running inspector on the host, by port or by pid.
func (m *Manager) Attach(ctx context.Context, p AttachParams, c model.Caller) (Session, error) {
	if (p.Port == 0) == (p.PID == 0) {
		return Session{}, apierr.Invalid("debug.attach에는 port나 pid 중 하나만 주세요")
	}
	if p.Port != 0 && (p.Port < 1 || p.Port > 65535) {
		return Session{}, apierr.Invalid("port는 1~65535 사이여야 합니다")
	}
	mode, err := exceptionsMode(p.PauseOnExceptions)
	if err != nil {
		return Session{}, err
	}
	if err := validSpecs(p.Breakpoints); err != nil {
		return Session{}, err
	}
	switch p.Role {
	case "", RoleMain, RoleRenderer:
	default:
		return Session{}, apierr.Invalid("role은 main이나 renderer여야 합니다")
	}
	agent, host, err := m.d.Hosts.Conn(p.HostRef)
	if err != nil {
		return Session{}, err
	}
	s, err := m.newSession(KindAttach, host, agent, c)
	if err != nil {
		return Session{}, err
	}
	s.mu.Lock()
	s.exceptions = mode
	s.setSpecsLocked(p.Breakpoints)
	s.mu.Unlock()
	m.startJob(s, "디버그 부착")

	p1, p2 := p.Port, 0
	role := p.Role
	if p.PID != 0 {
		callCtx, cancel := context.WithTimeout(ctx, m.tmo.Call)
		info := conn.CallInfo{}
		res, cerr := agent.Call(callCtx, "debug.attach.pid", proto.Message{"pid": p.PID}, conn.CallOpts{Info: &info})
		cancel()
		if cerr != nil {
			return m.launchFailed(s, c, outcomeError(info, cerr))
		}
		pid := p.PID
		s.mu.Lock()
		s.pid = &pid
		s.mu.Unlock()
		p1 = res.Int("inspect_port")
		p2 = res.Int("remote_debugging_port")
		role = RoleMain
	}

	oe := m.attachConnect(s, c, p1, p2, role)
	if oe != nil {
		return m.launchFailed(s, c, oe)
	}
	s.emitSession()
	m.record(s, c, "디버그 부착: "+s.name, model.TimelineOK)
	return s.snapshot(), nil
}

// attachConnect probes a port, decides its role when not given, and
// attaches main and renderer as it finds them.
func (m *Manager) attachConnect(s *session, c model.Caller, port, remoteDebugging int, role string) *OpError {
	ctx, cancel := context.WithTimeout(m.base, m.tmo.Probe*3)
	defer cancel()
	s.mu.Lock()
	s.ports.Inspect = port
	if remoteDebugging != 0 {
		s.ports.RemoteDebugging = remoteDebugging
	}
	s.mu.Unlock()
	addr, err := s.openTunnel(port)
	if err != nil {
		return &OpError{Err: apierr.As(err)}
	}
	var v jsonVersion
	if err := getJSON(ctx, addr, "/json/version", &v); err != nil {
		return &OpError{Err: apierr.New(wire.CodeNotFound,
			"그 포트의 디버거가 응답하지 않습니다. 포트 번호와 앱이 --inspect로 실행됐는지 확인하세요", true, apierr.NextCheckState)}
	}
	if role == "" {
		if isNode(v.Browser) {
			role = RoleMain
		} else {
			role = RoleRenderer
		}
	}
	if role == RoleMain {
		s.attachMain(ctx, addr, v, false)
		s.mu.Lock()
		s.ports.Inspect = port
		if s.name == "" {
			s.name = browserName(v.Browser)
		}
		s.mu.Unlock()
		if remoteDebugging != 0 {
			raddr, err := s.openTunnel(remoteDebugging)
			if err == nil {
				var rv jsonVersion
				if getJSON(ctx, raddr, "/json/version", &rv) == nil {
					s.attachRenderer(ctx, raddr, rv)
				}
			}
		}
		return nil
	}
	s.mu.Lock()
	s.ports.RemoteDebugging = port
	s.ports.Inspect = 0
	if s.name == "" {
		s.name = browserName(v.Browser)
	}
	s.mu.Unlock()
	s.attachRenderer(ctx, addr, v)
	return nil
}

// setSpecsLocked turns the launch or attach breakpoint specs into session
// breakpoints; the caller holds the lock.
func (s *session) setSpecsLocked(specs []BreakpointSpec) {
	for _, sp := range specs {
		s.bpSeq++
		enabled := sp.Enabled == nil || *sp.Enabled
		s.breakpoints = append(s.breakpoints, &Breakpoint{BreakpointID: breakpointID(s.bpSeq), Match: sp.Match, Pattern: sp.Pattern,
			Line: sp.Line, Column: sp.Column, Condition: sp.Condition, Enabled: enabled, Locations: []BreakpointLocation{}, Rev: 1})
	}
}

// startJob creates the session's debug job when a job registry is set.
func (m *Manager) startJob(s *session, summary string) {
	if m.d.Jobs == nil {
		return
	}
	job, err := m.d.Jobs.Create(s.hostID, jobs.KindDebug, "", summary, map[string]any{"debug_id": s.debugID})
	if err == nil {
		s.mu.Lock()
		s.jobID = job.JobID
		s.mu.Unlock()
	}
}

// launchFailed cleans up a failed launch or attach: it ends the launched
// process tree, closes the session's tunnels and finishes its job.
func (m *Manager) launchFailed(s *session, c model.Caller, oe *OpError) (Session, error) {
	s.mu.Lock()
	s.state = StateClosed
	s.closeReason = CloseRequested
	s.closedAt = time.Now()
	pid := s.pid
	kind := s.kind
	s.bumpRev()
	snap := s.snapshotLocked()
	s.mu.Unlock()
	if m.d.Tunnels != nil {
		m.d.Tunnels.CloseOwner(tunnel.OwnerDebugPrefix + s.debugID)
	}
	if kind == KindLaunch && pid != nil {
		killCtx, cancel := context.WithTimeout(m.base, m.tmo.Call)
		_, _ = s.agent.Call(killCtx, "proc.kill", proto.Message{"pid": *pid, "tree": true}, conn.CallOpts{})
		cancel()
	}
	if m.d.Jobs != nil && s.jobID != "" {
		result := map[string]any{"error": oe.Err}
		if snap.ExitCode != nil {
			result["exit_code"] = *snap.ExitCode
		}
		_, _ = m.d.Jobs.Finish(s.jobID, model.StatusFailed, result)
	}
	m.record(s, c, "디버그 실행 실패: "+firstLine(oe.Err.Message), model.TimelineFailed)
	return snap, oe
}

// outcomeError classifies an agent call failure: unknown when the request
// went out without an answer, else failed.
func outcomeError(info conn.CallInfo, err error) *OpError {
	e := apierr.As(err)
	if e.Code == wire.CodeExecutionOutcomeUnknown || (info.Sent && !info.Answered) {
		return &OpError{Err: e, Unknown: true}
	}
	return &OpError{Err: e}
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func intField(m map[string]any, k string) int {
	if m == nil {
		return 0
	}
	return toInt(m[k])
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}
