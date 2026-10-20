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
	Argv      []string          `json:"argv,omitempty" jsonschema:"Program and arguments run directly without a shell, for example [\"ipconfig\",\"/all\"] or [\"C:\\Windows\\System32\\hostname.exe\"]. Prefer this. Exactly one of argv or cmd is required."`
	Cmd       string            `json:"cmd,omitempty" jsonschema:"One command line run by a shell, for example Get-Process | Select -First 3. Needs shell. Exactly one of argv or cmd is required."`
	Shell     string            `json:"shell,omitempty" jsonschema:"Shell for cmd: powershell or cmd. Leave empty with argv."`
	Cwd       string            `json:"cwd,omitempty" jsonschema:"Working directory on the PC, for example C:\\Users\\me\\project."`
	Env       map[string]string `json:"env,omitempty" jsonschema:"Extra environment variables for the command."`
	Stdin     *string           `json:"stdin,omitempty" jsonschema:"Text written to the command's standard input."`
	TimeoutMS int               `json:"timeout_ms,omitempty" jsonschema:"Command time limit in milliseconds; default 120000, maximum 600000. A command over its limit ends with status failed and error TIMEOUT."`
}

type execStartInput struct {
	Host      string            `json:"host" jsonschema:"Host to run on: host_id or name from hosts_list."`
	Argv      []string          `json:"argv,omitempty" jsonschema:"Program and arguments run directly without a shell, for example [\"ping\",\"-n\",\"20\",\"example.com\"]. Prefer this. Exactly one of argv or cmd is required."`
	Cmd       string            `json:"cmd,omitempty" jsonschema:"One command line run by a shell. Needs shell. Exactly one of argv or cmd is required."`
	Shell     string            `json:"shell,omitempty" jsonschema:"Shell for cmd: powershell or cmd. Leave empty with argv."`
	Cwd       string            `json:"cwd,omitempty" jsonschema:"Working directory on the PC, for example C:\\Users\\me\\project."`
	Env       map[string]string `json:"env,omitempty" jsonschema:"Extra environment variables for the command."`
	Stdin     *string           `json:"stdin,omitempty" jsonschema:"Text written to the command's standard input."`
	TimeoutMS int               `json:"timeout_ms,omitempty" jsonschema:"Optional time limit in milliseconds; omit for no limit."`
}

// exactlyOne requires exactly one of the named properties.
func exactlyOne(s *jsonschema.Schema, a, b string) {
	s.OneOf = []*jsonschema.Schema{{Required: []string{a}}, {Required: []string{b}}}
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
		"Run a command on the PC and wait for it to finish (up to timeout_ms, default 120000, max 600000). Returns the envelope plus code, stdout and stderr; a non-zero exit code is status failed with error null and the exit code in code. When the envelope fails with an error (for example TIMEOUT or OFFLINE) the top-level code is the error code and the exit code, if any, moves to exit_code. If the status is unknown the command may have run: use job_get or session_inspect before retrying and never resend blindly. For long commands use exec_start.",
		spec{Method: "exec.run", Mutating: true, Timeout: func(p map[string]any) time.Duration {
			ms := int64(defaultExecTimeoutMS)
			if n, ok := num(p, "timeout_ms"); ok {
				ms = n
			}
			return time.Duration(ms)*time.Millisecond + execMargin
		}},
		func(s *jsonschema.Schema) {
			exactlyOne(s, "argv", "cmd")
			timeoutProp(s.Properties, &def, maxExecTimeoutMS)
		})
	add[execStartInput](s, srv, "exec_start",
		"Start a command on the PC and return at once with a job_id. Follow it with job_output and job_get; stop it with job_cancel. Use for commands that take more than a couple of minutes.",
		spec{Method: "exec.start", Mutating: true},
		func(s *jsonschema.Schema) { exactlyOne(s, "argv", "cmd"); timeoutProp(s.Properties, nil, 24*3600*1000) })
}
