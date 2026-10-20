package mcpserver

import (
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultExecTimeoutMS = 120000
	maxExecTimeoutMS     = 600000
)

type execInput struct {
	Host      string            `json:"host" jsonschema:"Host to run on: host_id or name from hosts_list."`
	Argv      []string          `json:"argv,omitempty" jsonschema:"Program and arguments run directly without a shell, for example [\"ipconfig\",\"/all\"] or [\"C:\\\\Windows\\\\System32\\\\hostname.exe\"]. Prefer this. Give argv or cmd, not both."`
	Cmd       string            `json:"cmd,omitempty" jsonschema:"One command line run by a shell, for example Get-Process | Select -First 3. Needs shell. Give argv or cmd, not both."`
	Shell     string            `json:"shell,omitempty" jsonschema:"Shell for cmd: powershell or cmd. Leave empty with argv."`
	Cwd       string            `json:"cwd,omitempty" jsonschema:"Working directory on the PC, for example C:\\\\Users\\\\me\\\\project."`
	Env       map[string]string `json:"env,omitempty" jsonschema:"Extra environment variables for the command."`
	Stdin     *string           `json:"stdin,omitempty" jsonschema:"Text written to the command's standard input."`
	TimeoutMS int               `json:"timeout_ms,omitempty" jsonschema:"Command time limit in milliseconds; default 120000, maximum 600000. A command over its limit ends with status failed and error TIMEOUT."`
}

type execStartInput struct {
	Host      string            `json:"host" jsonschema:"Host to run on: host_id or name from hosts_list."`
	Argv      []string          `json:"argv,omitempty" jsonschema:"Program and arguments run directly without a shell, for example [\"ping\",\"-n\",\"20\",\"example.com\"]. Prefer this. Give argv or cmd, not both."`
	Cmd       string            `json:"cmd,omitempty" jsonschema:"One command line run by a shell. Needs shell. Give argv or cmd, not both."`
	Shell     string            `json:"shell,omitempty" jsonschema:"Shell for cmd: powershell or cmd. Leave empty with argv."`
	Cwd       string            `json:"cwd,omitempty" jsonschema:"Working directory on the PC, for example C:\\\\Users\\\\me\\\\project."`
	Env       map[string]string `json:"env,omitempty" jsonschema:"Extra environment variables for the command."`
	Stdin     *string           `json:"stdin,omitempty" jsonschema:"Text written to the command's standard input."`
	TimeoutMS int               `json:"timeout_ms,omitempty" jsonschema:"Optional time limit in milliseconds; omit for no limit."`
}

func timeoutProp(props map[string]*jsonschema.Schema, def *int, max float64) {
	p := props["timeout_ms"]
	zero := 1.0
	p.Minimum = &zero
	p.Maximum = &max
	if def != nil {
		p.Default = []byte("120000")
	}
}

func (s *Server) registerExec(srv *mcp.Server) {
	def := defaultExecTimeoutMS
	add[execInput](s, srv, "exec_run",
		"Run a command on the PC and wait for it to finish (up to timeout_ms, default 120000, max 600000). Returns the envelope plus code, stdout and stderr; a non-zero exit code is status failed with error null. If the status is unknown the command may have run: use job_get or session_inspect before retrying and never resend blindly. For long commands use exec_start.",
		spec{Method: "exec.run", Mutating: true, Timeout: func(p map[string]any) time.Duration {
			ms := int64(defaultExecTimeoutMS)
			if n, ok := num(p, "timeout_ms"); ok {
				ms = n
			}
			return time.Duration(ms)*time.Millisecond + execMargin
		}},
		func(props map[string]*jsonschema.Schema) { timeoutProp(props, &def, maxExecTimeoutMS) })
	add[execStartInput](s, srv, "exec_start",
		"Start a command on the PC and return at once with a job_id. Follow it with job_output and job_get; stop it with job_cancel. Use for commands that take more than a couple of minutes.",
		spec{Method: "exec.start", Mutating: true},
		func(props map[string]*jsonschema.Schema) { timeoutProp(props, nil, 24*3600*1000) })
}
