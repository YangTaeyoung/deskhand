// Package mcpserver is the MCP server of `deskhand mcp`: tools that operate
// a remote PC through the daemon's local API (docs/local-api.md).
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/YangTaeyoung/deskhand/internal/client"
	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// clientName is the daemon client name; the MCP client's name follows it.
	clientName = "deskhand-mcp"
	// callTimeout bounds a call that answers at once.
	callTimeout = 60 * time.Second
	// execMargin is added to an exec timeout for the round trip.
	execMargin = 45 * time.Second
	// dialTimeout bounds the connection to (and start of) the daemon.
	dialTimeout = 15 * time.Second
)

// Caller is a connection to the daemon.
type Caller interface {
	Call(ctx context.Context, method string, params, out any) error
	Close() error
}

// Dialer opens a daemon connection named name.
type Dialer func(ctx context.Context, name string) (Caller, error)

// DefaultDialer connects as client kind "mcp", starting the daemon when it
// does not answer.
func DefaultDialer(version string) Dialer {
	return func(ctx context.Context, name string) (Caller, error) {
		return client.DialOptions(ctx, "mcp", name, client.Options{Version: version})
	}
}

const instructions = `Deskhand operates remote Windows PCs through the daemon on this Mac.

Playbook:
1. Start with hosts_list, then session_inspect for the host you will use. Pass the host as "host" (host_id or name).
2. Prefer argv over cmd+shell for exec_run/exec_start. Use cmd with shell "powershell" or "cmd" only when you need shell syntax.
3. Every result keeps the daemon envelope: status, action.executed, action.outcome, error, next_action, job_id.
4. status "unknown" or action.outcome "unknown" means the PC may or may not have done it. Never resend blindly: check with job_get (job_id) or session_inspect, then files_stat / proc_list, and only then retry.
5. Long commands: exec_start, then job_output (use next_cursor) and job_get; job_cancel stops it.
6. files_read returns at most 256 KiB of base64 per call; page with offset and length.
7. Failures come back as tool errors (IsError) whose structured content always has top-level code, message, retryable and next_action; failed envelopes keep their full envelope as well. CONTROL_HELD_BY_HUMAN means a person is using the PC: wait for them, do not retry in a loop.
8. Screen and input control tools arrive in a later version.`

// Server holds the lazily opened daemon connection shared by the tools.
type Server struct {
	dial Dialer
	mu   sync.Mutex
	c    Caller
}

// New returns the MCP server with all tools registered. The daemon is
// contacted on the first tool call and the connection outlives that call.
func New(version string, dial Dialer) *mcp.Server {
	s := &Server{dial: dial}
	srv := mcp.NewServer(&mcp.Implementation{Name: "deskhand", Version: version}, &mcp.ServerOptions{Instructions: instructions})
	s.registerHosts(srv)
	s.registerExec(srv)
	s.registerFiles(srv)
	s.registerProc(srv)
	return srv
}

// Run serves MCP over stdio until the client disconnects or ctx ends.
func Run(ctx context.Context, version string) error {
	return New(version, DefaultDialer(version)).Run(ctx, &mcp.StdioTransport{})
}

func (s *Server) conn(req *mcp.CallToolRequest) (Caller, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.c != nil {
		return s.c, nil
	}
	name := clientName
	if req != nil && req.Session != nil {
		if ip := req.Session.InitializeParams(); ip != nil && ip.ClientInfo != nil && ip.ClientInfo.Name != "" {
			name += "/" + ip.ClientInfo.Name
		}
	}
	dctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	c, err := s.dial(dctx, name)
	if err != nil {
		return nil, apierr.New(wire.CodeOffline, "Cannot reach the Deskhand daemon: "+err.Error()+
			" Ask the user to run `deskhand daemon start` and check the daemon log.", true, apierr.NextRetry)
	}
	s.c = c
	return c, nil
}

func (s *Server) drop(c Caller) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.c == c {
		s.c = nil
	}
	c.Close()
}

// spec describes how a tool maps onto a daemon method.
type spec struct {
	// Method is the local API method.
	Method string
	// Mutating marks a call that changes the remote PC.
	Mutating bool
	// Extra holds fixed params added to every call.
	Extra map[string]any
	// Timeout returns the call timeout for the params.
	Timeout func(params map[string]any) time.Duration
}

// add registers a tool whose input is In and whose call is described by sp.
func add[In any](s *Server, srv *mcp.Server, name, desc string, sp spec, tweak func(schema *jsonschema.Schema)) {
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		panic("mcpserver: schema of " + name + ": " + err.Error())
	}
	if tweak != nil {
		tweak(schema)
	}
	mcp.AddTool(srv, &mcp.Tool{Name: name, Description: desc, InputSchema: schema},
		func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			params, err := toParams(in)
			if err != nil {
				return failure(sp, apierr.Invalid("The input could not be converted to JSON; check the argument values.")), nil, nil
			}
			for k, v := range sp.Extra {
				params[k] = v
			}
			return s.invoke(ctx, req, name, sp, params), nil, nil
		})
}

// toParams converts the tool input to daemon params; the "host" field is
// the daemon's host_id.
func toParams(in any) (map[string]any, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if h, ok := m["host"]; ok {
		delete(m, "host")
		m["host_id"] = h
	}
	return m, nil
}

func (s *Server) invoke(ctx context.Context, req *mcp.CallToolRequest, tool string, sp spec, params map[string]any) *mcp.CallToolResult {
	c, err := s.conn(req)
	if err != nil {
		return failure(sp, err)
	}
	timeout := callTimeout
	if sp.Timeout != nil {
		timeout = sp.Timeout(params)
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var raw json.RawMessage
	if err := c.Call(cctx, sp.Method, params, &raw); err != nil {
		if client.IsConnectionLost(err) {
			s.drop(c)
		}
		return failure(sp, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var res any
	if err := dec.Decode(&res); err != nil {
		return failure(sp, apierr.Internal("The daemon result could not be read; check that deskhand and the daemon are the same version."))
	}
	return success(tool, sp, params, res)
}

// failure turns a call error into a tool error result.
func failure(sp spec, err error) *mcp.CallToolResult {
	lost := client.IsConnectionLost(err)
	ae := apierr.As(err)
	if ae == nil {
		ae = apierr.Internal("Unknown error; check the daemon log.")
	}
	var e map[string]any
	switch {
	case sp.Mutating && (lost || ae.Code == wire.CodeTimeout):
		e = map[string]any{"status": "unknown", "code": wire.CodeExecutionOutcomeUnknown, "retryable": false, "next_action": apierr.NextCheckState,
			"message": "The connection to the daemon ended or timed out before the result arrived, so the outcome is unknown: the PC may have done it. " +
				"Check with job_get, session_inspect, files_stat or proc_list before any retry; do not resend blindly."}
		e["error"] = map[string]any{"code": e["code"], "message": e["message"], "retryable": false, "next_action": apierr.NextCheckState}
	case lost:
		e = map[string]any{"code": wire.CodeOffline, "retryable": true, "next_action": apierr.NextRetry,
			"message": "The connection to the daemon ended before the call ran. Call the tool again; it reconnects."}
	default:
		e = map[string]any{"code": ae.Code, "message": ae.Message, "retryable": ae.Retryable, "next_action": ae.NextAction}
	}
	return &mcp.CallToolResult{IsError: true, StructuredContent: e, Content: []mcp.Content{&mcp.TextContent{Text: errorText(e)}}}
}
