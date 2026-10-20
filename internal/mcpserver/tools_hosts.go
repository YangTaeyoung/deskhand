package mcpserver

import (
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type hostInput struct {
	Host string `json:"host" jsonschema:"Host to operate on: the host_id or the name from hosts_list, for example windows-dev."`
}

type jobInput struct {
	JobID string `json:"job_id" jsonschema:"Job id returned by exec_start or by any envelope that carries job_id, for example j-01JB."`
}

type jobOutputInput struct {
	JobID  string `json:"job_id" jsonschema:"Job id of a command started with exec_start or exec_run."`
	Cursor string `json:"cursor,omitempty" jsonschema:"The next_cursor of the previous job_output call; omit to read from the start."`
	Max    int    `json:"max,omitempty" jsonschema:"Maximum bytes to return in this call; default and maximum 262144 (256 KiB)."`
}

func (s *Server) registerHosts(srv *mcp.Server) {
	add[struct{}](s, srv, "hosts_list",
		"List the registered remote PCs with host_id, name, os, connection state, capabilities, session and control owner. Start here to find the host to use.",
		spec{Method: "host.list"}, nil)
	add[hostInput](s, srv, "host_doctor",
		"Run connectivity checks on a PC (ssh_reachable, agent_version, quic_connect, fingerprint, session, firewall_rule). Each check has status pass/fail/warn/skip and a fix. Use it when a host is offline or a call fails with OFFLINE or AUTH_REQUIRED.",
		spec{Method: "host.doctor"}, nil)
	add[hostInput](s, srv, "session_inspect",
		"Show the interactive Windows session of a PC: user, connect_state, locked, input_desktop, displays, capabilities, control owner (none/human/ai) and connection_generation. Call it before acting and whenever an outcome is unknown.",
		spec{Method: "session.inspect"}, nil)
	add[jobInput](s, srv, "job_get",
		"Get a job by id: kind, status (running/succeeded/failed/waiting_for_user/unknown/cancelled), summary and result. Use it to follow exec_start and to check what happened when an outcome is unknown.",
		spec{Method: "jobs.get"}, nil)
	add[jobOutputInput](s, srv, "job_output",
		"Read the stdout/stderr chunks of a job. Pass the returned next_cursor to read on and keep reading until next_cursor is \"end\", which means the output is complete; a job that has exited can still have output left (state exited with a numeric cursor).",
		spec{Method: "jobs.output"}, func(s *jsonschema.Schema) {
			one, max := 1.0, float64(maxReadBytes)
			s.Properties["max"].Minimum = &one
			s.Properties["max"].Maximum = &max
			s.Properties["max"].Default = []byte("262144")
		})
	add[jobInput](s, srv, "job_cancel",
		"Cancel a running job. The result envelope tells whether the cancel took effect.",
		spec{Method: "jobs.cancel", Mutating: true}, nil)
}
