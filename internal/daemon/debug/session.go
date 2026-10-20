package debug

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
)

// Event kinds of the debugger (local-api.md §6).
const (
	evSessionChanged    = "debug.session_changed"
	evTargetChanged     = "debug.target_changed"
	evBreakpointChanged = "debug.breakpoint_changed"
	evPaused            = "debug.paused"
	evResumed           = "debug.resumed"
	evConsole           = "debug.console"
	evConsoleCleared    = "debug.console_cleared"
	evClosed            = "debug.closed"
)

// targetState is one debuggable target with its CDP connection.
type targetState struct {
	info      Target
	conn      *cdpConn
	sessionID string

	paused       bool
	pausePending bool
	pause        *PauseState
	// expect is the reason of the next pause the daemon caused, consumed
	// when it arrives, with the actor that asked for it.
	expect      string
	expectActor Actor
	// resumeBy is the actor of a resume or step, carried to debug.resumed.
	resumeBy Actor
	// cancelPause is set when a resume cancelled a still-pending pause; the
	// spurious pause that then arrives is resumed without being surfaced.
	cancelPause bool

	scripts []Script
	// scriptURL maps the id of each parsed script that has a URL to it.
	scriptURL map[string]string

	// cdpBreakpoints maps a session breakpoint id to its CDP id on this
	// target.
	cdpBreakpoints map[string]string

	// pauseObjects maps an object id issued during a pause to its gen;
	// consoleObjects holds the ids of the session's console group.
	pauseObjects   map[string]int64
	consoleObjects map[string]bool
}

// session is one debug session the daemon owns.
type session struct {
	m       *Manager
	debugID string
	hostID  string
	host    HostInfo
	agent   Agent
	jobID   string

	mu sync.Mutex
	// meta is the session's own fields; targets and console are kept apart.
	kind         string
	name         string
	path         string
	args         []string
	cwd          string
	pauseOnStart bool
	pid          *int
	// started is the root process start time (RFC 3339) a launch reported,
	// passed to proc.kill so the agent refuses a reused pid.
	started     string
	ports       Ports
	state       string
	mainReason  string
	rendReason  string
	breakpoints []*Breakpoint
	bpActive    bool
	exceptions  string
	createdBy   Actor
	createdAt   time.Time
	closedAt    time.Time
	closeReason string
	exitCode    *int
	rev         int64
	bpSeq       int

	targets map[string]*targetState
	order   []string

	mainConn *cdpConn
	rendConn *cdpConn

	console consoleBuffer

	tunnels []string
	// closedOnce guards the one-time close work; closing marks a close in
	// progress so watchConn does not race it to process_exited.
	closedOnce bool
	closing    bool

	// consoleCh carries console entries off the CDP dispatch goroutine to
	// the console pump; a full channel drops the entry.
	consoleCh chan ConsoleEntry
}

// newSessionShell returns an empty session.
func newSessionShell(m *Manager, id, kind string, host HostInfo, agent Agent, c model.Caller) *session {
	s := &session{m: m, debugID: id, hostID: host.HostID, host: host, agent: agent, kind: kind, name: "",
		state: StateRunning, bpActive: true, exceptions: exceptionsDefault, createdBy: actorOf(c), createdAt: time.Now(),
		rev: 1, args: []string{}, breakpoints: []*Breakpoint{}, targets: map[string]*targetState{},
		consoleCh: make(chan ConsoleEntry, consoleQueue)}
	go s.consolePump()
	return s
}

func (s *session) closedTime() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closedAt
}

// bumpRev raises the session rev; the caller holds the lock.
func (s *session) bumpRev() int64 {
	s.rev++
	return s.rev
}

// snapshot returns the session as the API shows it.
func (s *session) snapshot() Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *session) snapshotLocked() Session {
	out := Session{DebugID: s.debugID, HostID: s.hostID, JobID: s.jobID, Kind: s.kind, Name: s.name, Path: s.path,
		Args: append([]string{}, s.args...), Cwd: s.cwd, PauseOnStart: s.pauseOnStart, PID: s.pid, Ports: s.ports,
		State: s.state, MainUnavailableReason: s.mainReason, RendererUnavailableReason: s.rendReason,
		BreakpointsActive: s.bpActive, PauseOnExceptions: s.exceptions, CreatedBy: s.createdBy,
		CreatedAt: model.Time(s.createdAt), CloseReason: s.closeReason, ExitCode: s.exitCode, Rev: s.rev}
	out.CreatedAt = model.Time(s.createdAt)
	if !s.closedAt.IsZero() {
		out.ClosedAt = model.Time(s.closedAt)
	}
	out.Targets = []Target{}
	for _, main := range []bool{true, false} {
		for _, id := range s.order {
			if t := s.targets[id]; t != nil && (t.info.Role == RoleMain) == main {
				out.Targets = append(out.Targets, t.info)
			}
		}
	}
	out.Breakpoints = make([]Breakpoint, 0, len(s.breakpoints))
	for _, b := range s.breakpoints {
		out.Breakpoints = append(out.Breakpoints, *b)
	}
	return out
}

// publish sends a debugger event to the host's subscribers.
func (s *session) publish(kind string, fields map[string]any) {
	if s.m.d.Bus != nil {
		s.m.d.Bus.Publish(s.hostID, s.jobID, kind, fields)
	}
}

// emitSession publishes debug.session_changed with the current snapshot.
func (s *session) emitSession() {
	s.publish(evSessionChanged, map[string]any{"debug_id": s.debugID, "session": s.snapshot()})
}

// emitTargetLocked publishes debug.target_changed for t; the caller holds
// the lock.
func (s *session) emitTargetLocked(t *targetState, removed bool) {
	s.publish(evTargetChanged, map[string]any{"debug_id": s.debugID, "target": t.info, "removed": removed, "rev": s.rev})
}

// addTargetLocked records a target in creation order.
func (s *session) addTargetLocked(t *targetState) {
	if _, ok := s.targets[t.info.ID]; !ok {
		s.order = append(s.order, t.info.ID)
	}
	s.targets[t.info.ID] = t
	if t.scriptURL == nil {
		t.scriptURL = map[string]string{}
	}
	if t.cdpBreakpoints == nil {
		t.cdpBreakpoints = map[string]string{}
	}
	if t.pauseObjects == nil {
		t.pauseObjects = map[string]int64{}
	}
	if t.consoleObjects == nil {
		t.consoleObjects = map[string]bool{}
	}
}

// target returns the target id, held under the lock.
func (s *session) targetLocked(id string) *targetState { return s.targets[id] }

// handler returns the event handler for conn.
func (s *session) handler(main bool) func(cdpEvent) {
	return func(ev cdpEvent) { s.onEvent(main, ev) }
}

// onEvent routes a CDP event to its target and applies it.
func (s *session) onEvent(main bool, ev cdpEvent) {
	switch ev.Method {
	case "Target.attachedToTarget":
		s.onAttached(ev.Params)
		return
	case "Target.detachedFromTarget":
		s.onDetached(ev.Params)
		return
	case "Target.targetInfoChanged":
		s.onTargetInfo(ev.Params)
		return
	}
	s.mu.Lock()
	t := s.targetBySession(main, ev.SessionID)
	s.mu.Unlock()
	if t == nil {
		return
	}
	switch ev.Method {
	case "Debugger.scriptParsed":
		s.onScriptParsed(t, ev.Params)
	case "Debugger.paused":
		s.onPaused(t, ev.Params)
	case "Debugger.resumed":
		s.onResumed(t)
	case "Debugger.breakpointResolved":
		s.onBreakpointResolved(t, ev.Params)
	case "Runtime.consoleAPICalled":
		s.onConsoleAPI(t, ev.Params)
	case "Runtime.exceptionThrown":
		s.onExceptionThrown(t, ev.Params)
	case "Log.entryAdded":
		s.onLogEntry(t, ev.Params)
	case "Runtime.executionContextsCleared":
		s.onContextsCleared(t)
	}
}

// targetBySession finds the target of a connection event; the caller holds
// the lock.
func (s *session) targetBySession(main bool, sessionID string) *targetState {
	for _, id := range s.order {
		t := s.targets[id]
		if t == nil {
			continue
		}
		if main && t.conn == s.mainConn && t.sessionID == "" && sessionID == "" {
			return t
		}
		if !main && t.conn == s.rendConn && t.sessionID == sessionID && sessionID != "" {
			return t
		}
	}
	return nil
}

type scriptParsed struct {
	ScriptID string `json:"scriptId"`
	URL      string `json:"url"`
}

func (s *session) onScriptParsed(t *targetState, raw json.RawMessage) {
	var p scriptParsed
	if json.Unmarshal(raw, &p) != nil || p.URL == "" {
		return
	}
	s.mu.Lock()
	if _, ok := t.scriptURL[p.ScriptID]; !ok {
		t.scriptURL[p.ScriptID] = p.URL
		t.scripts = append(t.scripts, Script{ScriptID: p.ScriptID, TargetID: t.info.ID, URL: p.URL})
	}
	s.mu.Unlock()
}

func (s *session) onContextsCleared(t *targetState) {
	s.mu.Lock()
	t.scripts = nil
	t.scriptURL = map[string]string{}
	t.pauseObjects = map[string]int64{}
	t.consoleObjects = map[string]bool{}
	if t.paused {
		t.paused, t.pause, t.pausePending = false, nil, false
		t.info.Gen++
		t.info.Paused, t.info.PausePending = false, false
		s.recomputeStateLocked()
	}
	s.mu.Unlock()
}

// recomputeStateLocked sets the session state from the targets.
func (s *session) recomputeStateLocked() {
	if s.state == StateClosed {
		return
	}
	paused := false
	for _, t := range s.targets {
		if t.paused {
			paused = true
			break
		}
	}
	want := StateRunning
	if paused {
		want = StatePaused
	}
	if want != s.state {
		s.state = want
		s.bumpRev()
	}
}
