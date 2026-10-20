package mcpserver

import (
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type procListInput struct {
	Host string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Name string `json:"name,omitempty" jsonschema:"Only processes whose name contains this text without regard to case, for example notepad."`
}

type procKillInput struct {
	Host    string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	PID     int    `json:"pid,omitempty" jsonschema:"Process id to end. Exactly one of pid or name is required."`
	Started string `json:"started,omitempty" jsonschema:"With pid: the process's started value from proc_list. The kill is refused with STALE_TARGET when the pid now belongs to a process created at another time."`
	Name    string `json:"name,omitempty" jsonschema:"Image name to end, for example notepad.exe. Exactly one of pid or name is required."`
	Tree    bool   `json:"tree,omitempty" jsonschema:"Also end the child processes."`
}

type launchInput struct {
	Host         string   `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Path         string   `json:"path" jsonschema:"Executable or document to start in the user's session, for example C:\\Windows\\System32\\notepad.exe."`
	Args         []string `json:"args,omitempty" jsonschema:"Arguments, one per element, for example [\"C:\\tmp\\a.txt\"]."`
	Cwd          string   `json:"cwd,omitempty" jsonschema:"Working directory on the PC."`
	WaitWindowMS *int     `json:"wait_window_ms,omitempty" jsonschema:"Milliseconds to wait for the app's window to appear; 0 returns at once."`
}

func (s *Server) registerProc(srv *mcp.Server) {
	add[procListInput](s, srv, "proc_list", "List processes on the PC, optionally only those whose name contains name: pid, ppid, name, path, cmd, mem_kb (working set), cpu_pct (share of all logical processors over 0.5 s, 0-100), user, started (creation time), started_by_deskhand (started by exec, app_launch or a terminal of Deskhand, or a descendant of one) and self (the Deskhand agent itself, which cannot be ended).",
		spec{Method: "proc.list"}, nil)
	add[procKillInput](s, srv, "proc_kill", "End a process by pid or image name. Pass started from proc_list with pid so that a reused pid is not ended (STALE_TARGET). The agent itself (self) is never ended (PERMISSION_DENIED). Check with proc_list afterwards if the outcome is unknown.",
		spec{Method: "proc.kill", Mutating: true}, func(s *jsonschema.Schema) { exactlyOne(s, "pid", "name") })
	add[launchInput](s, srv, "app_launch", "Start an application on the PC's interactive desktop and return its pid. Use exec_run for commands whose output you need.",
		spec{Method: "app.launch", Mutating: true}, nil)
}
