package mcpserver

import "github.com/modelcontextprotocol/go-sdk/mcp"

type procListInput struct {
	Host string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Name string `json:"name,omitempty" jsonschema:"Only processes with this image name, for example notepad.exe."`
}

type procKillInput struct {
	Host string `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	PID  int    `json:"pid,omitempty" jsonschema:"Process id to end. Give pid or name."`
	Name string `json:"name,omitempty" jsonschema:"Image name to end, for example notepad.exe. Give pid or name."`
	Tree bool   `json:"tree,omitempty" jsonschema:"Also end the child processes."`
}

type launchInput struct {
	Host         string   `json:"host" jsonschema:"Host: host_id or name from hosts_list."`
	Path         string   `json:"path" jsonschema:"Executable or document to start in the user's session, for example C:\\\\Windows\\\\System32\\\\notepad.exe."`
	Args         []string `json:"args,omitempty" jsonschema:"Arguments, one per element, for example [\"C:\\\\tmp\\\\a.txt\"]."`
	Cwd          string   `json:"cwd,omitempty" jsonschema:"Working directory on the PC."`
	WaitWindowMS *int     `json:"wait_window_ms,omitempty" jsonschema:"Milliseconds to wait for the app's window to appear; 0 returns at once."`
}

func (s *Server) registerProc(srv *mcp.Server) {
	add[procListInput](s, srv, "proc_list", "List processes on the PC (pid, ppid, name, path, mem_kb, started_by_deskhand), optionally only those named name.",
		spec{Method: "proc.list"}, nil)
	add[procKillInput](s, srv, "proc_kill", "End a process by pid or image name. Check with proc_list afterwards if the outcome is unknown.",
		spec{Method: "proc.kill", Mutating: true}, nil)
	add[launchInput](s, srv, "app_launch", "Start an application on the PC's interactive desktop and return its pid. Use exec_run for commands whose output you need.",
		spec{Method: "app.launch", Mutating: true}, nil)
}
