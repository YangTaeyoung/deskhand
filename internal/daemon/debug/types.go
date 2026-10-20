// Package debug is the daemon's CDP debugger (docs/local-api.md §9): it
// launches an Electron app on a PC with its inspector ports or attaches to
// a running one, reaches the Node inspector of the main process and the
// DevTools endpoint of the renderers through the daemon's loopback tunnels,
// and serves breakpoints, pauses, call stacks, variables, evaluation,
// stepping, scripts and the console of each session.
package debug

import (
	"errors"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
)

// Session kinds, states and close reasons.
const (
	KindLaunch = "launch"
	KindAttach = "attach"

	StateRunning = "running"
	StatePaused  = "paused"
	StateClosed  = "closed"

	CloseRequested      = "requested"
	CloseProcessExited  = "process_exited"
	CloseConnectionLost = "connection_lost"
)

// Target roles.
const (
	RoleMain     = "main"
	RoleRenderer = "renderer"
)

// Reasons a session has no main or no renderer.
const (
	InspectorDisabled            = "inspector_disabled"
	InspectorNotResponding       = "inspector_not_responding"
	RemoteDebuggingDisabled      = "remote_debugging_disabled"
	RemoteDebuggingNotResponding = "remote_debugging_not_responding"
	exceptionsDefault            = "uncaught"
	nextAttachTarget             = "attach_target"
	consoleGroup                 = "console"
	breakpointPrefix             = "bp-"
	debugPrefix                  = "d-"
	matchFileName                = "file_name"
	matchURL                     = "url"
	matchURLRegex                = "url_regex"
	sourceMax                    = 2 << 20
	variablesDefault             = 200
	variablesMax                 = 1000
	timelineKind                 = "debug"
	accessorPreview              = "(...)"
	exceptionSourceConsole       = "console"
	exceptionSourceException     = "exception"
	exceptionSourceBrowser       = "browser"
	pauseReasonBreakpoint        = "breakpoint"
	pauseReasonException         = "exception"
	pauseReasonPromiseRejection  = "promise_rejection"
	pauseReasonPauseRequest      = "pause_request"
	pauseReasonStep              = "step"
	pauseReasonStart             = "start"
	pauseReasonOther             = "other"
)

// Actor is the client behind a request.
type Actor struct {
	Actor      string `json:"actor"`
	ClientName string `json:"client_name,omitempty"`
}

func actorOf(c model.Caller) Actor { return Actor{Actor: c.Actor, ClientName: c.ClientName} }

// Ports are the PC's loopback ports of a session.
type Ports struct {
	Inspect         int `json:"inspect,omitempty"`
	RemoteDebugging int `json:"remote_debugging,omitempty"`
}

// Target is a debuggable target of a session.
type Target struct {
	ID           string `json:"id"`
	Role         string `json:"role"`
	Type         string `json:"type"`
	URL          string `json:"url"`
	Title        string `json:"title"`
	Attached     bool   `json:"attached"`
	Paused       bool   `json:"paused"`
	PausePending bool   `json:"pause_pending"`
	Gen          int64  `json:"gen"`
}

// BreakpointSpec is what a new breakpoint is.
type BreakpointSpec struct {
	Match     string `json:"match"`
	Pattern   string `json:"pattern"`
	Line      int    `json:"line"`
	Column    *int   `json:"column,omitempty"`
	Condition string `json:"condition,omitempty"`
	Enabled   *bool  `json:"enabled,omitempty"`
}

// BreakpointLocation is where a breakpoint bound in a loaded script.
type BreakpointLocation struct {
	TargetID string `json:"target_id"`
	ScriptID string `json:"script_id"`
	URL      string `json:"url"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
}

// Breakpoint is a breakpoint of a session.
type Breakpoint struct {
	BreakpointID string               `json:"breakpoint_id"`
	Match        string               `json:"match"`
	Pattern      string               `json:"pattern"`
	Line         int                  `json:"line"`
	Column       *int                 `json:"column,omitempty"`
	Condition    string               `json:"condition,omitempty"`
	Enabled      bool                 `json:"enabled"`
	Resolved     bool                 `json:"resolved"`
	Locations    []BreakpointLocation `json:"locations"`
	Rev          int64                `json:"rev"`
}

// Session is a debug session (DebugSession of local-api.md §5).
type Session struct {
	DebugID                   string       `json:"debug_id"`
	HostID                    string       `json:"host_id"`
	JobID                     string       `json:"job_id,omitempty"`
	Kind                      string       `json:"kind"`
	Name                      string       `json:"name"`
	Path                      string       `json:"path,omitempty"`
	Args                      []string     `json:"args"`
	Cwd                       string       `json:"cwd,omitempty"`
	PauseOnStart              bool         `json:"pause_on_start"`
	PID                       *int         `json:"pid,omitempty"`
	Ports                     Ports        `json:"ports"`
	State                     string       `json:"state"`
	Targets                   []Target     `json:"targets"`
	MainUnavailableReason     string       `json:"main_unavailable_reason,omitempty"`
	RendererUnavailableReason string       `json:"renderer_unavailable_reason,omitempty"`
	Breakpoints               []Breakpoint `json:"breakpoints"`
	BreakpointsActive         bool         `json:"breakpoints_active"`
	PauseOnExceptions         string       `json:"pause_on_exceptions"`
	CreatedBy                 Actor        `json:"created_by"`
	CreatedAt                 string       `json:"created_at"`
	ClosedAt                  string       `json:"closed_at,omitempty"`
	CloseReason               string       `json:"close_reason,omitempty"`
	ExitCode                  *int         `json:"exit_code,omitempty"`
	Rev                       int64        `json:"rev"`
}

// Scope is a scope of a call frame.
type Scope struct {
	Type     string `json:"type"`
	ObjectID string `json:"object_id"`
	Name     string `json:"name,omitempty"`
}

// CallFrame is a frame of a paused call stack.
type CallFrame struct {
	FrameID    string       `json:"frame_id"`
	Function   string       `json:"function"`
	ScriptID   string       `json:"script_id"`
	URL        string       `json:"url"`
	Line       int          `json:"line"`
	Column     int          `json:"column"`
	ScopeChain []Scope      `json:"scope_chain"`
	This       *RemoteValue `json:"this,omitempty"`
}

// PauseState says whether a target is paused, why, and its call stack.
type PauseState struct {
	TargetID       string       `json:"target_id"`
	Gen            int64        `json:"gen"`
	Paused         bool         `json:"paused"`
	Reason         string       `json:"reason,omitempty"`
	RequestedBy    *Actor       `json:"requested_by,omitempty"`
	HitBreakpoints []string     `json:"hit_breakpoints"`
	CallFrames     []CallFrame  `json:"call_frames"`
	Exception      *RemoteValue `json:"exception,omitempty"`
}

// Script is a loaded script with a URL.
type Script struct {
	ScriptID string `json:"script_id"`
	TargetID string `json:"target_id"`
	URL      string `json:"url"`
}

// Source is the text of a script.
type Source struct {
	ScriptID  string `json:"script_id"`
	URL       string `json:"url"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

// Evaluation is the result of debug.evaluate: the value, or the thrown
// value with Exception.
type Evaluation struct {
	RemoteValue
	Exception *ExceptionInfo `json:"exception,omitempty"`
}

// OpError is the failure of an operation the daemon attempted on the PC
// or its debugger: the method answers an envelope with it. Unknown marks
// an operation whose outcome is not known.
type OpError struct {
	Err     *apierr.Error
	Unknown bool
}

func (e *OpError) Error() string { return e.Err.Message }
func (e *OpError) Unwrap() error { return e.Err }

// failed returns an OpError of err.
func failed(err error) *OpError { return &OpError{Err: apierr.As(err)} }

// AsOpError returns err as an OpError when it is one.
func AsOpError(err error) (*OpError, bool) {
	var oe *OpError
	if errors.As(err, &oe) {
		return oe, true
	}
	return nil, false
}
