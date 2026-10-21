package localapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/debug"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
)

const (
	// debugLaunchTimeout bounds a launch or attach while it probes the
	// inspector ports.
	debugLaunchTimeout = 90 * time.Second
	// debugEvaluateTimeout bounds a debug.evaluate round trip.
	debugEvaluateTimeout = 35 * time.Second
)

func (s *Server) registerDebug() {
	if s.d.Debug == nil {
		return
	}
	s.methods["debug.list"] = s.debugList
	s.methods["debug.get"] = s.debugGet
	s.methods["debug.electron.launch"] = s.debugLaunch
	s.methods["debug.attach"] = s.debugAttach
	s.methods["debug.target.attach"] = s.debugTargetAttach
	s.methods["debug.breakpoint.set"] = s.debugBreakpointSet
	s.methods["debug.breakpoint.update"] = s.debugBreakpointUpdate
	s.methods["debug.breakpoint.remove"] = s.debugBreakpointRemove
	s.methods["debug.breakpoints.activate"] = s.debugBreakpointsActivate
	s.methods["debug.exceptions"] = s.debugExceptions
	s.methods["debug.pause"] = s.debugPause
	s.methods["debug.resume"] = s.debugResume
	s.methods["debug.step"] = s.debugStep
	s.methods["debug.state"] = s.debugState
	s.methods["debug.variables"] = s.debugVariables
	s.methods["debug.evaluate"] = s.debugEvaluate
	s.methods["debug.scripts"] = s.debugScripts
	s.methods["debug.source"] = s.debugSource
	s.methods["debug.console"] = s.debugConsole
	s.methods["debug.console.clear"] = s.debugConsoleClear
	s.methods["debug.close"] = s.debugClose
}

type debugParams struct {
	HostID            string                 `json:"host_id"`
	DebugID           string                 `json:"debug_id"`
	TargetID          string                 `json:"target_id"`
	Path              string                 `json:"path"`
	Args              []string               `json:"args"`
	Cwd               string                 `json:"cwd"`
	PauseOnStart      bool                   `json:"pause_on_start"`
	Breakpoints       []debug.BreakpointSpec `json:"breakpoints"`
	PauseOnExceptions string                 `json:"pause_on_exceptions"`
	Port              int                    `json:"port"`
	PID               int                    `json:"pid"`
	Role              string                 `json:"role"`
	Match             string                 `json:"match"`
	Pattern           string                 `json:"pattern"`
	Line              int                    `json:"line"`
	Column            *int                   `json:"column"`
	Condition         *string                `json:"condition"`
	BreakpointID      string                 `json:"breakpoint_id"`
	Enabled           *bool                  `json:"enabled"`
	Active            bool                   `json:"active"`
	Mode              string                 `json:"mode"`
	Kind              string                 `json:"kind"`
	ObjectID          string                 `json:"object_id"`
	Offset            int                    `json:"offset"`
	Limit             int                    `json:"limit"`
	Expression        string                 `json:"expression"`
	FrameID           string                 `json:"frame_id"`
	ScriptID          string                 `json:"script_id"`
	Cursor            *int64                 `json:"cursor"`
	Before            *int64                 `json:"before"`
	KeepProcess       bool                   `json:"keep_process"`
}

// debugActionEnvelope wraps the success of a debug action in an envelope.
func (s *Server) debugActionEnvelope(debugID string, fields map[string]any) map[string]any {
	hostID, jobID, _ := s.d.Debug.Meta(debugID)
	o := outcome{status: model.StatusSucceeded, action: model.Executed()}
	return s.envelope(hostID, jobID, conn.CallInfo{}, o, nil).Map(fields)
}

// debugOpResult turns a (fields, *OpError) into a success or failed
// envelope.
func (s *Server) debugOpResult(debugID string, fields map[string]any, oe *debug.OpError) (any, error) {
	hostID, jobID, _ := s.d.Debug.Meta(debugID)
	if oe == nil {
		o := outcome{status: model.StatusSucceeded, action: model.Executed()}
		return s.envelope(hostID, jobID, conn.CallInfo{}, o, nil).Map(fields), nil
	}
	o := outcome{status: model.StatusFailed, action: model.NotStarted(), err: oe.Err}
	if oe.Unknown {
		o = outcome{status: model.StatusUnknown, action: model.UnknownAction(), err: oe.Err}
	}
	return s.envelope(hostID, jobID, conn.CallInfo{}, o, nil).Map(fields), nil
}

func (s *Server) debugList(_ context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	hostID := ""
	if p.HostID != "" {
		row, err := s.d.Hosts.Resolve(p.HostID)
		if err != nil {
			return nil, err
		}
		hostID = row.HostID
	}
	return map[string]any{"sessions": s.d.Debug.List(hostID)}, nil
}

func (s *Server) debugGet(_ context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if p.DebugID == "" {
		return nil, required("debug_id", "debug.get")
	}
	sess, err := s.d.Debug.Get(p.DebugID)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Server) debugLaunch(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	cctx, cancel := s.detached(debugLaunchTimeout)
	defer cancel()
	out, err := s.d.Debug.Launch(cctx, debug.LaunchParams{HostRef: p.HostID, Path: p.Path, Args: p.Args, Cwd: p.Cwd,
		PauseOnStart: p.PauseOnStart, Breakpoints: p.Breakpoints, PauseOnExceptions: p.PauseOnExceptions}, sess.Caller())
	return s.debugSessionResult(out, err)
}

func (s *Server) debugAttach(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	cctx, cancel := s.detached(debugLaunchTimeout)
	defer cancel()
	out, err := s.d.Debug.Attach(cctx, debug.AttachParams{HostRef: p.HostID, Port: p.Port, PID: p.PID, Role: p.Role,
		Breakpoints: p.Breakpoints, PauseOnExceptions: p.PauseOnExceptions}, sess.Caller())
	return s.debugSessionResult(out, err)
}

// debugSessionResult turns a launch or attach result into an envelope with
// the session, or a JSON-RPC error for a bad request.
func (s *Server) debugSessionResult(out debug.Session, err error) (any, error) {
	if err == nil {
		o := outcome{status: model.StatusRunning, action: model.Executed()}
		return s.envelope(out.HostID, out.JobID, conn.CallInfo{}, o, nil).Map(map[string]any{"session": out}), nil
	}
	oe, ok := debug.AsOpError(err)
	if !ok {
		return nil, err
	}
	o := outcome{status: model.StatusFailed, action: model.NotStarted(), err: oe.Err}
	if oe.Unknown {
		o = outcome{status: model.StatusUnknown, action: model.UnknownAction(), err: oe.Err}
	}
	var sess any = out
	if out.DebugID == "" {
		sess = nil
	}
	return s.envelope(out.HostID, out.JobID, conn.CallInfo{}, o, nil).Map(map[string]any{"session": sess}), nil
}

func (s *Server) debugTargetAttach(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if p.DebugID == "" || p.TargetID == "" {
		return nil, required("debug_id와 target_id", "debug.target.attach")
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	t, err := s.d.Debug.TargetAttach(cctx, p.DebugID, p.TargetID, sess.Caller())
	if err != nil {
		if oe, ok := debug.AsOpError(err); ok {
			return s.debugOpResult(p.DebugID, map[string]any{"target": nil}, oe)
		}
		return nil, err
	}
	return s.debugActionEnvelope(p.DebugID, map[string]any{"target": t}), nil
}

func (s *Server) debugBreakpointSet(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	spec := debug.BreakpointSpec{Match: p.Match, Pattern: p.Pattern, Line: p.Line, Column: p.Column}
	if p.Condition != nil {
		spec.Condition = *p.Condition
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	bp, err := s.d.Debug.BreakpointSet(cctx, p.DebugID, spec, sess.Caller())
	if err != nil {
		return nil, err
	}
	return s.debugActionEnvelope(p.DebugID, map[string]any{"breakpoint": bp}), nil
}

func (s *Server) debugBreakpointUpdate(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if p.BreakpointID == "" {
		return nil, required("breakpoint_id", "debug.breakpoint.update")
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	bp, err := s.d.Debug.BreakpointUpdate(cctx, p.DebugID, p.BreakpointID, p.Enabled, p.Condition, sess.Caller())
	if err != nil {
		return nil, err
	}
	return s.debugActionEnvelope(p.DebugID, map[string]any{"breakpoint": bp}), nil
}

func (s *Server) debugBreakpointRemove(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if p.BreakpointID == "" {
		return nil, required("breakpoint_id", "debug.breakpoint.remove")
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	bp, err := s.d.Debug.BreakpointRemove(cctx, p.DebugID, p.BreakpointID, sess.Caller())
	if err != nil {
		return nil, err
	}
	return s.debugActionEnvelope(p.DebugID, map[string]any{"breakpoint": bp}), nil
}

func (s *Server) debugBreakpointsActivate(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	out, err := s.d.Debug.BreakpointsActivate(cctx, p.DebugID, p.Active, sess.Caller())
	if err != nil {
		return nil, err
	}
	return s.debugActionEnvelope(p.DebugID, map[string]any{"session": out}), nil
}

func (s *Server) debugExceptions(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	out, err := s.d.Debug.Exceptions(cctx, p.DebugID, p.Mode, sess.Caller())
	if err != nil {
		return nil, err
	}
	return s.debugActionEnvelope(p.DebugID, map[string]any{"session": out}), nil
}

func (s *Server) debugPause(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	return s.debugControl(raw, sess, func(cctx context.Context, p debugParams) (debug.Target, *debug.OpError) {
		return s.d.Debug.Pause(cctx, p.DebugID, p.TargetID, sess.Caller())
	})
}

func (s *Server) debugResume(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	return s.debugControl(raw, sess, func(cctx context.Context, p debugParams) (debug.Target, *debug.OpError) {
		return s.d.Debug.Resume(cctx, p.DebugID, p.TargetID, sess.Caller())
	})
}

func (s *Server) debugStep(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	return s.debugControl(raw, sess, func(cctx context.Context, p debugParams) (debug.Target, *debug.OpError) {
		return s.d.Debug.Step(cctx, p.DebugID, p.TargetID, p.Kind, sess.Caller())
	})
}

func (s *Server) debugControl(raw json.RawMessage, _ *session, run func(context.Context, debugParams) (debug.Target, *debug.OpError)) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if p.DebugID == "" || p.TargetID == "" {
		return nil, required("debug_id와 target_id", "debug 실행 제어")
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	t, oe := run(cctx, p)
	fields := map[string]any{"target": nil}
	if oe == nil {
		fields["target"] = t
	}
	return s.debugOpResult(p.DebugID, fields, oe)
}

func (s *Server) debugState(_ context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if p.DebugID == "" || p.TargetID == "" {
		return nil, required("debug_id와 target_id", "debug.state")
	}
	return s.d.Debug.State(p.DebugID, p.TargetID)
}

func (s *Server) debugVariables(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if p.DebugID == "" || p.TargetID == "" || p.ObjectID == "" {
		return nil, required("debug_id, target_id와 object_id", "debug.variables")
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	return s.d.Debug.Variables(cctx, p.DebugID, p.TargetID, p.ObjectID, p.Offset, p.Limit)
}

func (s *Server) debugEvaluate(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if p.DebugID == "" || p.TargetID == "" || p.Expression == "" {
		return nil, required("debug_id, target_id와 expression", "debug.evaluate")
	}
	cctx, cancel := s.detached(debugEvaluateTimeout)
	defer cancel()
	out, oe := s.d.Debug.Evaluate(cctx, p.DebugID, p.TargetID, p.Expression, p.FrameID, sess.Caller())
	fields := map[string]any{}
	if oe == nil {
		b, _ := json.Marshal(out)
		_ = json.Unmarshal(b, &fields)
	}
	return s.debugOpResult(p.DebugID, fields, oe)
}

func (s *Server) debugScripts(_ context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if p.DebugID == "" {
		return nil, required("debug_id", "debug.scripts")
	}
	return s.d.Debug.Scripts(p.DebugID, p.TargetID)
}

func (s *Server) debugSource(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if p.DebugID == "" || p.TargetID == "" || p.ScriptID == "" {
		return nil, required("debug_id, target_id와 script_id", "debug.source")
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	return s.d.Debug.Source(cctx, p.DebugID, p.TargetID, p.ScriptID)
}

func (s *Server) debugConsole(_ context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if p.DebugID == "" {
		return nil, required("debug_id", "debug.console")
	}
	return s.d.Debug.Console(p.DebugID, debug.ConsoleQuery{Cursor: p.Cursor, Before: p.Before, Limit: p.Limit})
}

func (s *Server) debugConsoleClear(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if p.DebugID == "" {
		return nil, required("debug_id", "debug.console.clear")
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	through, err := s.d.Debug.ConsoleClear(cctx, p.DebugID, sess.Caller())
	if err != nil {
		return nil, err
	}
	return s.debugActionEnvelope(p.DebugID, map[string]any{"cleared_through": through}), nil
}

func (s *Server) debugClose(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p debugParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if p.DebugID == "" {
		return nil, required("debug_id", "debug.close")
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	out, leftOpen, err := s.d.Debug.Close(cctx, p.DebugID, p.KeepProcess, sess.Caller())
	if err != nil {
		return nil, err
	}
	fields := map[string]any{"session": out}
	if leftOpen {
		fields["inspector_left_open"] = true
	}
	return s.debugActionEnvelope(p.DebugID, fields), nil
}
