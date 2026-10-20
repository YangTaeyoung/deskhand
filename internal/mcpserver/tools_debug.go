package mcpserver

import (
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type breakpointSpecInput struct {
	Match     string `json:"match" jsonschema:"How pattern names scripts: file_name (URL path ends with pattern), url (exact URL) or url_regex (JavaScript regex over URLs)."`
	Pattern   string `json:"pattern" jsonschema:"The script path, URL or regex to break in."`
	Line      int    `json:"line" jsonschema:"1-based line."`
	Column    int    `json:"column,omitempty" jsonschema:"1-based column; omit for the first executable position on the line."`
	Condition string `json:"condition,omitempty" jsonschema:"Optional JavaScript condition; the breakpoint only stops when it is truthy."`
}

type debugLaunchInput struct {
	Host              string                `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Path              string                `json:"path" jsonschema:"Path of the Electron executable on the PC."`
	Args              []string              `json:"args,omitempty" jsonschema:"Extra arguments for the app."`
	Cwd               string                `json:"cwd,omitempty" jsonschema:"Working directory; defaults to the executable's folder."`
	PauseOnStart      bool                  `json:"pause_on_start,omitempty" jsonschema:"Start with --inspect-brk so the main process stops on its first line."`
	Breakpoints       []breakpointSpecInput `json:"breakpoints,omitempty" jsonschema:"Breakpoints to set as soon as the targets attach."`
	PauseOnExceptions string                `json:"pause_on_exceptions,omitempty" jsonschema:"none, uncaught (default) or all."`
}

type debugAttachInput struct {
	Host              string                `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Port              int                   `json:"port,omitempty" jsonschema:"Loopback inspector port on the PC. Exactly one of port or pid is required."`
	PID               int                   `json:"pid,omitempty" jsonschema:"PID of a running Node or Electron process; its inspector is turned on. Exactly one of port or pid is required."`
	Role              string                `json:"role,omitempty" jsonschema:"main or renderer; inferred from the endpoint when omitted."`
	Breakpoints       []breakpointSpecInput `json:"breakpoints,omitempty"`
	PauseOnExceptions string                `json:"pause_on_exceptions,omitempty" jsonschema:"none, uncaught (default) or all."`
}

type debugIDInput struct {
	DebugID string `json:"debug_id" jsonschema:"Session id from debug_launch, debug_attach or debug_list."`
}

type debugListInput struct {
	Host string `json:"host,omitempty" jsonschema:"Only the sessions of this host; omit for every host."`
}

type debugTargetInput struct {
	DebugID  string `json:"debug_id" jsonschema:"Session id."`
	TargetID string `json:"target_id" jsonschema:"Target id from the session's targets (main or a renderer)."`
}

type debugBreakpointSetInput struct {
	DebugID string `json:"debug_id"`
	breakpointSpecInput
}

type debugBreakpointRemoveInput struct {
	DebugID      string `json:"debug_id"`
	BreakpointID string `json:"breakpoint_id" jsonschema:"Breakpoint id from debug_breakpoint_set or the session."`
}

type debugExceptionsInput struct {
	DebugID string `json:"debug_id"`
	Mode    string `json:"mode" jsonschema:"none, uncaught or all."`
}

type debugStepInput struct {
	DebugID  string `json:"debug_id"`
	TargetID string `json:"target_id"`
	Kind     string `json:"kind" jsonschema:"over, into or out."`
}

type debugVariablesInput struct {
	DebugID  string `json:"debug_id"`
	TargetID string `json:"target_id"`
	ObjectID string `json:"object_id" jsonschema:"A scope or value object id from a call frame or a previous read."`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty" jsonschema:"1 to 1000; default 200."`
}

type debugEvaluateInput struct {
	DebugID    string `json:"debug_id"`
	TargetID   string `json:"target_id"`
	Expression string `json:"expression" jsonschema:"JavaScript to evaluate."`
	FrameID    string `json:"frame_id,omitempty" jsonschema:"Evaluate in this paused call frame; omit to evaluate in the target's global scope."`
}

type debugScriptsInput struct {
	DebugID  string `json:"debug_id"`
	TargetID string `json:"target_id,omitempty" jsonschema:"Only this target's scripts; omit for all."`
}

type debugSourceInput struct {
	DebugID  string `json:"debug_id"`
	TargetID string `json:"target_id"`
	ScriptID string `json:"script_id" jsonschema:"Script id from debug_scripts."`
}

type debugConsoleInput struct {
	DebugID string `json:"debug_id"`
	Cursor  int    `json:"cursor,omitempty" jsonschema:"Return entries after this seq (next_cursor of the previous page)."`
	Before  int    `json:"before,omitempty" jsonschema:"Return entries before this seq. Only one of cursor or before."`
	Limit   int    `json:"limit,omitempty" jsonschema:"1 to 1000; default 200."`
}

type debugCloseInput struct {
	DebugID     string `json:"debug_id"`
	KeepProcess bool   `json:"keep_process,omitempty" jsonschema:"Leave the launched app running instead of ending it."`
}

func (s *Server) registerDebug(srv *mcp.Server) {
	launchTimeout := func(map[string]any) time.Duration { return 100 * time.Second }
	add[debugLaunchInput](s, srv, "debug_launch",
		"Launch an Electron app on the PC with its inspector ports and connect the CDP debugger to it. Picks two free loopback ports on the PC, starts the app with --inspect (or --inspect-brk when pause_on_start) and --remote-debugging-port, tunnels to them and attaches the main (Node inspector) and renderer (Chromium DevTools) targets. Returns an envelope with the session: debug_id, targets (main first), ports and, when only one side answered, main_unavailable_reason or renderer_unavailable_reason. Fails with EXITED_EARLY when the app quits before any port answers (often a single-instance lock), TIMEOUT when no port answers, NOT_FOUND when the path is missing.",
		spec{Method: "debug.electron.launch", Mutating: true, Timeout: launchTimeout}, nil)
	add[debugAttachInput](s, srv, "debug_attach",
		"Attach the CDP debugger to a running inspector on the PC, by loopback port or by pid. With a port, /json/version decides whether it is a Node (main) or DevTools (renderer) endpoint unless role is given. With a pid, the agent turns on that process's Node inspector (not supported on Windows, where it returns CAPABILITY_UNSUPPORTED) and finds the port. Returns an envelope with the session.",
		spec{Method: "debug.attach", Mutating: true, Timeout: launchTimeout}, func(sc *jsonschema.Schema) {
			exactlyOne(sc, "port", "pid")
		})
	add[debugListInput](s, srv, "debug_list",
		"List the debug sessions, oldest first, including those closed within the last 10 minutes: debug_id, host_id, kind (launch or attach), name, state (running, paused or closed) and targets. Read-only.",
		spec{Method: "debug.list"}, nil)
	add[debugIDInput](s, srv, "debug_get",
		"Get one debug session: its targets (main first), breakpoints, pause-on-exceptions mode, ports and state. Read-only.",
		spec{Method: "debug.get"}, nil)
	add[debugBreakpointSetInput](s, srv, "debug_breakpoint_set",
		"Set a breakpoint on the session (all its targets). match is file_name, url or url_regex; line and column count from 1. Returns an envelope with the breakpoint and the locations it resolved to. An invalid url_regex is rejected as INVALID_ARGUMENT.",
		spec{Method: "debug.breakpoint.set"}, nil)
	add[debugBreakpointRemoveInput](s, srv, "debug_breakpoint_remove",
		"Remove a breakpoint from the session and every target.",
		spec{Method: "debug.breakpoint.remove"}, nil)
	add[debugExceptionsInput](s, srv, "debug_exceptions",
		"Set whether execution pauses on exceptions: none, uncaught (the default) or all.",
		spec{Method: "debug.exceptions"}, nil)
	add[debugTargetInput](s, srv, "debug_pause",
		"Pause a target. If it has no code to run it reports pause_pending and stops on the next JavaScript. Returns an envelope with the target read at reply time.",
		spec{Method: "debug.pause", Mutating: true}, nil)
	add[debugTargetInput](s, srv, "debug_resume",
		"Resume a paused target so it keeps running until the next breakpoint, exception or pause. Returns an envelope with the target.",
		spec{Method: "debug.resume", Mutating: true}, nil)
	add[debugStepInput](s, srv, "debug_step",
		"Run one step of a paused target: kind over, into or out. A running target is refused with STALE_TARGET.",
		spec{Method: "debug.step", Mutating: true}, nil)
	add[debugTargetInput](s, srv, "debug_state",
		"Get the pause state of a target: whether it is paused, why (breakpoint, exception, step, pause_request, start, …), the hit breakpoints, the call stack (innermost first) with each frame's scope chain, and the thrown value for an exception pause. Read-only.",
		spec{Method: "debug.state"}, nil)
	add[debugVariablesInput](s, srv, "debug_variables",
		"Read the properties of a scope or value object: name, value_preview, type and, for objects, an object_id to expand further. Accessors show (...), internal properties come last. Page with offset and limit. An object id from a finished pause is refused with STALE_TARGET. Read-only.",
		spec{Method: "debug.variables"}, nil)
	add[debugEvaluateInput](s, srv, "debug_evaluate",
		"Evaluate a JavaScript expression, in a paused call frame when frame_id is given (else in the target's global scope). The result's value survives resume and stepping. When the expression throws, the envelope still succeeds and the thrown value is in exception. A frame_id on a running target is STALE_TARGET; over 30 seconds is TIMEOUT.",
		spec{Method: "debug.evaluate", Mutating: true, Timeout: func(map[string]any) time.Duration { return 40 * time.Second }}, nil)
	add[debugScriptsInput](s, srv, "debug_scripts",
		"List the loaded scripts that have a URL, in load order: script_id, target_id and url. Read-only.",
		spec{Method: "debug.scripts"}, nil)
	add[debugSourceInput](s, srv, "debug_source",
		"Get the source text of a script by script_id, truncated at 2 MiB. Read-only.",
		spec{Method: "debug.source"}, nil)
	add[debugConsoleInput](s, srv, "debug_console",
		"Read the session console (console API calls, uncaught exceptions and browser log entries), oldest first. Page forward with cursor (next_cursor of the previous page) or backward with before; gap is true when entries were dropped between the cursor and the kept window. Read-only.",
		spec{Method: "debug.console"}, func(sc *jsonschema.Schema) {
			one, max := 1.0, 1000.0
			sc.Properties["limit"].Minimum, sc.Properties["limit"].Maximum = &one, &max
		})
	add[debugCloseInput](s, srv, "debug_close",
		"Close a debug session: disconnect the debugger and its tunnels, and end the launched process tree unless keep_process is set (an attach session always leaves the process). inspector_left_open is true when the agent could not close an inspector it turned on for a pid attach.",
		spec{Method: "debug.close", Mutating: true}, nil)
}
