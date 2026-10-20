package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

type rpcFail struct {
	code, msg, next string
	retryable       bool
}

type handler func(params map[string]any) (any, *rpcFail)

type fakeAPI struct {
	mu       sync.Mutex
	handlers map[string]handler
	calls    map[string][]map[string]any
	hellos   []map[string]any
}

func (f *fakeAPI) called(method string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.calls[method]...)
}

func startFakeAPI(t *testing.T, handlers map[string]handler) *fakeAPI {
	t.Helper()
	dir, err := os.MkdirTemp("", "dh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("DESKHAND_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(dir, "run", "d.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f := &fakeAPI{handlers: handlers, calls: map[string][]map[string]any{}}
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(nc)
		}
	}()
	return f
}

func (f *fakeAPI) serve(nc net.Conn) {
	defer nc.Close()
	sc := bufio.NewScanner(nc)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	send := func(v any) {
		b, _ := json.Marshal(v)
		nc.Write(append(b, '\n'))
	}
	for sc.Scan() {
		var req struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			continue
		}
		if req.Method == "hello" {
			f.mu.Lock()
			f.hellos = append(f.hellos, req.Params)
			f.mu.Unlock()
			send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"client_id": "c-1", "daemon_version": "test", "api_version": 1}})
			continue
		}
		f.mu.Lock()
		f.calls[req.Method] = append(f.calls[req.Method], req.Params)
		h := f.handlers[req.Method]
		f.mu.Unlock()
		if h == nil {
			send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "모르는 메서드",
				"data": map[string]any{"code": "INVALID_ARGUMENT"}}})
			continue
		}
		res, fail := h(req.Params)
		if fail != nil && fail.code == "DROP" {
			return
		}
		if fail != nil {
			send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32000, "message": fail.msg,
				"data": map[string]any{"code": fail.code, "retryable": fail.retryable, "next_action": fail.next}}})
			continue
		}
		send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": res})
	}
}

func connect(t *testing.T) *mcp.ClientSession {
	return connectWith(t, DefaultDialer("test"))
}

func connectWith(t *testing.T, dial Dialer) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	srv := New("test", dial)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	return res
}

func structured(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	m, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content is %T, want object", res.StructuredContent)
	}
	return m
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func envelope(extra map[string]any) map[string]any {
	m := map[string]any{"schema_version": 1, "request_id": "r-1", "job_id": nil, "host_id": "windows-dev", "session_id": nil,
		"connection_generation": 4, "status": "succeeded", "action": map[string]any{"executed": true, "outcome": "done"},
		"error": nil, "next_action": nil, "artifacts": []any{}}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

var allTools = []string{"hosts_list", "host_doctor", "session_inspect", "exec_run", "exec_start", "job_get", "job_output", "job_cancel",
	"files_list", "files_stat", "files_read", "files_write", "files_mkdir", "files_rename", "files_delete", "proc_list", "proc_kill", "app_launch",
	"files_push", "files_pull", "files_move", "files_copy", "files_trash", "files_hash", "transfer_get", "transfer_list", "transfer_cancel", "transfer_resume", "transfer_pause"}

func TestToolsListed(t *testing.T) {
	cs := connect(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
		if len(tool.Description) < 30 {
			t.Errorf("%s: description too short", tool.Name)
		}
	}
	want := append([]string(nil), allTools...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want %v", got, want)
	}
}

func TestInstructionsPlaybook(t *testing.T) {
	cs := connect(t)
	ins := cs.InitializeResult().Instructions
	for _, want := range []string{"hosts_list", "session_inspect", "argv", "job_get", "unknown"} {
		if !strings.Contains(ins, want) {
			t.Errorf("instructions lack %q: %s", want, ins)
		}
	}
}

func TestHostIsRequiredAndSchemaValidated(t *testing.T) {
	f := startFakeAPI(t, nil)
	cs := connect(t)
	for _, name := range []string{"session_inspect", "exec_run", "files_read", "proc_list", "app_launch"} {
		res := callTool(t, cs, name, map[string]any{})
		if !res.IsError {
			t.Errorf("%s without host: IsError = false", name)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("daemon called for invalid input: %v", f.calls)
	}
}

func TestExecRunToolEnvelope(t *testing.T) {
	f := startFakeAPI(t, map[string]handler{"exec.run": func(p map[string]any) (any, *rpcFail) {
		return envelope(map[string]any{"code": 0, "stdout": "hello\n", "stderr": ""}), nil
	}})
	cs := connect(t)
	res := callTool(t, cs, "exec_run", map[string]any{"host": "windows-dev", "argv": []any{"hostname"}})
	if res.IsError {
		t.Fatalf("IsError: %s", text(res))
	}
	m := structured(t, res)
	if m["host_id"] != "windows-dev" || m["status"] != "succeeded" || m["stdout"] != "hello\n" {
		t.Fatalf("structured = %v", m)
	}
	if act, _ := m["action"].(map[string]any); act["executed"] != true {
		t.Fatalf("action = %v", m["action"])
	}
	if !strings.Contains(text(res), "hello") || !strings.Contains(text(res), "succeeded") {
		t.Fatalf("text = %q", text(res))
	}
	calls := f.called("exec.run")
	if len(calls) != 1 || calls[0]["host_id"] != "windows-dev" || calls[0]["timeout_ms"] != float64(120000) {
		t.Fatalf("exec.run params = %v", calls)
	}
}

func TestExecRunNonzeroExitIsNotToolError(t *testing.T) {
	startFakeAPI(t, map[string]handler{"exec.run": func(map[string]any) (any, *rpcFail) {
		return envelope(map[string]any{"status": "failed", "code": 3, "stdout": "", "stderr": "boom"}), nil
	}})
	res := callTool(t, connect(t), "exec_run", map[string]any{"host": "h", "argv": []any{"x"}})
	if res.IsError {
		t.Fatalf("IsError for a finished command: %s", text(res))
	}
	if !strings.Contains(text(res), "exit code 3") {
		t.Fatalf("text = %q", text(res))
	}
}

func TestExecRunTimeoutBounds(t *testing.T) {
	f := startFakeAPI(t, map[string]handler{"exec.run": func(map[string]any) (any, *rpcFail) { return envelope(nil), nil }})
	cs := connect(t)
	if res := callTool(t, cs, "exec_run", map[string]any{"host": "h", "argv": []any{"x"}, "timeout_ms": 600001}); !res.IsError {
		t.Fatal("timeout_ms above 600000 accepted")
	}
	if res := callTool(t, cs, "exec_run", map[string]any{"host": "h", "argv": []any{"x"}, "timeout_ms": 600000}); res.IsError {
		t.Fatalf("timeout_ms 600000 rejected: %s", text(res))
	}
	if got := f.called("exec.run"); len(got) != 1 || got[0]["timeout_ms"] != float64(600000) {
		t.Fatalf("calls = %v", got)
	}
}

func TestExecUnknownTellsToCheckBeforeRetry(t *testing.T) {
	startFakeAPI(t, map[string]handler{"exec.run": func(map[string]any) (any, *rpcFail) {
		return envelope(map[string]any{"status": "unknown", "job_id": "j-9", "action": map[string]any{"executed": nil, "outcome": "unknown"},
			"error":       map[string]any{"code": "EXECUTION_OUTCOME_UNKNOWN", "message": "응답을 잃었습니다", "retryable": false, "next_action": "check_state"},
			"next_action": "check_state"}), nil
	}})
	res := callTool(t, connect(t), "exec_run", map[string]any{"host": "h", "argv": []any{"x"}})
	if !res.IsError {
		t.Fatal("unknown outcome is not a tool error")
	}
	m := structured(t, res)
	if m["status"] != "unknown" || m["job_id"] != "j-9" || m["code"] != "EXECUTION_OUTCOME_UNKNOWN" || m["retryable"] != false || m["message"] != "응답을 잃었습니다" {
		t.Fatalf("structured = %v", m)
	}
	tx := text(res)
	for _, want := range []string{"job_get", "session_inspect", "j-9", "do not resend"} {
		if !strings.Contains(tx, want) {
			t.Errorf("text lacks %q: %s", want, tx)
		}
	}
}

func TestErrorCarriesCodeAndNextAction(t *testing.T) {
	startFakeAPI(t, map[string]handler{"exec.run": func(map[string]any) (any, *rpcFail) {
		return nil, &rpcFail{code: "CONTROL_HELD_BY_HUMAN", msg: "사람이 PC를 쓰는 중입니다", next: "human_release"}
	}})
	res := callTool(t, connect(t), "exec_run", map[string]any{"host": "h", "argv": []any{"x"}})
	if !res.IsError {
		t.Fatal("IsError = false")
	}
	m := structured(t, res)
	if m["code"] != "CONTROL_HELD_BY_HUMAN" || m["next_action"] != "human_release" || m["retryable"] != false || m["message"] == "" {
		t.Fatalf("structured = %v", m)
	}
	if !strings.Contains(text(res), "CONTROL_HELD_BY_HUMAN") || !strings.Contains(text(res), "human_release") {
		t.Fatalf("text = %q", text(res))
	}
}

func TestFailedEnvelopeIsToolErrorWithEnvelopeIntact(t *testing.T) {
	startFakeAPI(t, map[string]handler{"files.write": func(map[string]any) (any, *rpcFail) {
		return envelope(map[string]any{"status": "failed", "action": map[string]any{"executed": false, "outcome": "not_started"},
			"error":       map[string]any{"code": "OFFLINE", "message": "PC에 연결할 수 없습니다", "retryable": true, "next_action": "host_connect"},
			"next_action": "host_connect"}), nil
	}})
	res := callTool(t, connect(t), "files_write", map[string]any{"host": "h", "path": `C:\x.txt`, "data": "aGk="})
	if !res.IsError {
		t.Fatal("IsError = false")
	}
	m := structured(t, res)
	e, _ := m["error"].(map[string]any)
	if m["status"] != "failed" || e["code"] != "OFFLINE" || m["next_action"] != "host_connect" ||
		m["code"] != "OFFLINE" || m["retryable"] != true || m["message"] != "PC에 연결할 수 없습니다" {
		t.Fatalf("structured = %v", m)
	}
}

func TestClientNameInHello(t *testing.T) {
	f := startFakeAPI(t, map[string]handler{"host.list": func(map[string]any) (any, *rpcFail) {
		return map[string]any{"hosts": []any{map[string]any{"host_id": "windows-dev", "name": "win", "state": "ready"}}}, nil
	}})
	res := callTool(t, connect(t), "hosts_list", nil)
	if res.IsError {
		t.Fatal(text(res))
	}
	if !strings.Contains(text(res), "windows-dev") {
		t.Fatalf("text = %q", text(res))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.hellos) == 0 || f.hellos[len(f.hellos)-1]["client"] != "mcp" || f.hellos[len(f.hellos)-1]["name"] != "deskhand-mcp/test-agent" {
		t.Fatalf("hello = %v", f.hellos)
	}
}

func TestFilesReadPaging(t *testing.T) {
	f := startFakeAPI(t, map[string]handler{"files.read": func(map[string]any) (any, *rpcFail) {
		return map[string]any{"host_id": "windows-dev", "path": `C:\a`, "data": "aGVsbG8=", "size": 10, "eof": false}, nil
	}})
	cs := connect(t)
	res := callTool(t, cs, "files_read", map[string]any{"host": "h", "path": `C:\a`, "offset": 5})
	if res.IsError {
		t.Fatal(text(res))
	}
	if got := f.called("files.read")[0]; got["offset"] != float64(5) || got["length"] != float64(262144) {
		t.Fatalf("params = %v", got)
	}
	if !strings.Contains(text(res), "next offset 10") {
		t.Fatalf("text = %q", text(res))
	}
	if res := callTool(t, cs, "files_read", map[string]any{"host": "h", "path": `C:\a`, "length": 262145}); !res.IsError {
		t.Fatal("length above 256 KiB accepted")
	}
}

func TestParamMapping(t *testing.T) {
	ok := func(map[string]any) (any, *rpcFail) { return envelope(nil), nil }
	methods := []string{"host.list", "host.doctor", "session.inspect", "exec.run", "exec.start", "jobs.get", "jobs.output", "jobs.cancel",
		"files.list", "files.stat", "files.write", "files.mkdir", "files.rename", "files.delete", "proc.list", "proc.kill", "app.launch"}
	h := map[string]handler{}
	for _, m := range methods {
		h[m] = ok
	}
	f := startFakeAPI(t, h)
	cs := connect(t)
	cases := []struct {
		tool   string
		args   map[string]any
		method string
		want   map[string]any
	}{
		{"host_doctor", map[string]any{"host": "win"}, "host.doctor", map[string]any{"host_id": "win"}},
		{"session_inspect", map[string]any{"host": "win"}, "session.inspect", map[string]any{"host_id": "win"}},
		{"exec_run", map[string]any{"host": "win", "cmd": "Get-Date", "shell": "powershell", "cwd": `C:\`, "env": map[string]any{"A": "1"}, "stdin": "x", "timeout_ms": 5000},
			"exec.run", map[string]any{"host_id": "win", "cmd": "Get-Date", "shell": "powershell", "cwd": `C:\`, "env": map[string]any{"A": "1"}, "stdin": "x", "timeout_ms": float64(5000)}},
		{"exec_start", map[string]any{"host": "win", "argv": []any{"a", "b"}}, "exec.start", map[string]any{"host_id": "win", "argv": []any{"a", "b"}}},
		{"job_get", map[string]any{"job_id": "j-1"}, "jobs.get", map[string]any{"job_id": "j-1"}},
		{"job_output", map[string]any{"job_id": "j-1", "cursor": "0:0"}, "jobs.output", map[string]any{"job_id": "j-1", "cursor": "0:0", "max": float64(262144)}},
		{"job_cancel", map[string]any{"job_id": "j-1"}, "jobs.cancel", map[string]any{"job_id": "j-1"}},
		{"files_list", map[string]any{"host": "win", "path": `C:\`}, "files.list", map[string]any{"host_id": "win", "path": `C:\`}},
		{"files_stat", map[string]any{"host": "win", "path": `C:\a`, "sha256": true}, "files.stat", map[string]any{"host_id": "win", "path": `C:\a`, "sha256": true}},
		{"files_write", map[string]any{"host": "win", "path": `C:\a`, "data": "aGk=", "append": true, "mkdirs": true}, "files.write",
			map[string]any{"host_id": "win", "path": `C:\a`, "data": "aGk=", "append": true, "mkdirs": true}},
		{"files_mkdir", map[string]any{"host": "win", "path": `C:\d`}, "files.mkdir", map[string]any{"host_id": "win", "path": `C:\d`}},
		{"files_rename", map[string]any{"host": "win", "from": `C:\a`, "to": `C:\b`, "overwrite": true}, "files.rename",
			map[string]any{"host_id": "win", "from": `C:\a`, "to": `C:\b`, "overwrite": true}},
		{"files_delete", map[string]any{"host": "win", "path": `C:\a`, "recursive": true}, "files.delete",
			map[string]any{"host_id": "win", "path": `C:\a`, "recursive": true, "permanent": true}},
		{"proc_list", map[string]any{"host": "win", "name": "notepad.exe"}, "proc.list", map[string]any{"host_id": "win", "name": "notepad.exe"}},
		{"proc_kill", map[string]any{"host": "win", "pid": 12, "tree": true}, "proc.kill", map[string]any{"host_id": "win", "pid": float64(12), "tree": true}},
		{"app_launch", map[string]any{"host": "win", "path": `C:\Windows\notepad.exe`, "args": []any{"a.txt"}}, "app.launch",
			map[string]any{"host_id": "win", "path": `C:\Windows\notepad.exe`, "args": []any{"a.txt"}}},
	}
	for _, c := range cases {
		res := callTool(t, cs, c.tool, c.args)
		if res.IsError {
			t.Errorf("%s: %s", c.tool, text(res))
			continue
		}
		got := f.called(c.method)
		if len(got) == 0 {
			t.Errorf("%s: %s not called", c.tool, c.method)
			continue
		}
		gj, _ := json.Marshal(got[len(got)-1])
		wj, _ := json.Marshal(c.want)
		if string(gj) != string(wj) {
			t.Errorf("%s params = %s, want %s", c.tool, gj, wj)
		}
	}
}

func TestExecTimeoutEnvelopeCodeIsErrorCode(t *testing.T) {
	startFakeAPI(t, map[string]handler{"exec.run": func(map[string]any) (any, *rpcFail) {
		return envelope(map[string]any{"status": "failed", "code": 124, "stdout": "",
			"error": map[string]any{"code": "TIMEOUT", "message": "time limit", "retryable": true, "next_action": "retry"}, "next_action": "retry"}), nil
	}})
	res := callTool(t, connect(t), "exec_run", map[string]any{"host": "h", "argv": []any{"x"}})
	m := structured(t, res)
	if !res.IsError || m["code"] != "TIMEOUT" || m["exit_code"] != float64(124) || m["retryable"] != true {
		t.Fatalf("structured = %v", m)
	}
}

func TestMutatingConnectionLossIsUnknownAndNotResent(t *testing.T) {
	f := startFakeAPI(t, map[string]handler{
		"files.write": func(map[string]any) (any, *rpcFail) { return nil, &rpcFail{code: "DROP"} },
		"host.list":   func(map[string]any) (any, *rpcFail) { return map[string]any{"hosts": []any{}}, nil },
	})
	cs := connect(t)
	res := callTool(t, cs, "files_write", map[string]any{"host": "h", "path": `C:\x`, "data": "aGk="})
	m := structured(t, res)
	if !res.IsError || m["status"] != "unknown" || m["retryable"] != false || m["next_action"] != "check_state" || m["code"] != "EXECUTION_OUTCOME_UNKNOWN" {
		t.Fatalf("structured = %v", m)
	}
	for _, want := range []string{"unknown", "job_get", "session_inspect", "files_stat", "proc_list", "before any retry"} {
		if !strings.Contains(text(res), want) || !strings.Contains(m["message"].(string)+text(res), want) {
			t.Errorf("text lacks %q: %s", want, text(res))
		}
	}
	time.Sleep(200 * time.Millisecond)
	if n := len(f.called("files.write")); n != 1 {
		t.Fatalf("files.write requests = %d, want 1", n)
	}
	if res := callTool(t, cs, "hosts_list", nil); res.IsError {
		t.Fatalf("call after reconnect failed: %s", text(res))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.hellos) != 2 {
		t.Fatalf("hellos = %d, want 2 (redial)", len(f.hellos))
	}
}

func TestReadToolConnectionLossIsRetryableOffline(t *testing.T) {
	startFakeAPI(t, map[string]handler{"files.list": func(map[string]any) (any, *rpcFail) { return nil, &rpcFail{code: "DROP"} }})
	res := callTool(t, connect(t), "files_list", map[string]any{"host": "h", "path": `C:\`})
	m := structured(t, res)
	if !res.IsError || m["code"] != "OFFLINE" || m["retryable"] != true || m["next_action"] != "retry" || m["status"] != nil {
		t.Fatalf("structured = %v", m)
	}
}

func TestMutatingTimeoutMapsToUnknown(t *testing.T) {
	res := failure(spec{Mutating: true}, apierr.New(wire.CodeTimeout, "응답 시간 초과", false, "check_state"))
	m, _ := res.StructuredContent.(map[string]any)
	if !res.IsError || m["status"] != "unknown" || m["retryable"] != false || m["next_action"] != "check_state" || m["code"] != "EXECUTION_OUTCOME_UNKNOWN" {
		t.Fatalf("structured = %v", m)
	}
	read := failure(spec{}, apierr.New(wire.CodeTimeout, "timeout", false, "check_state"))
	if rm, _ := read.StructuredContent.(map[string]any); rm["code"] != "TIMEOUT" || rm["status"] != nil {
		t.Fatalf("read timeout = %v", rm)
	}
}

func TestJobToolsReportUnknownWithoutToolError(t *testing.T) {
	startFakeAPI(t, map[string]handler{
		"jobs.get": func(map[string]any) (any, *rpcFail) {
			return map[string]any{"job_id": "j-1", "kind": "exec", "status": "unknown", "summary": "s"}, nil
		},
		"jobs.output": func(map[string]any) (any, *rpcFail) {
			return map[string]any{"chunks": []any{}, "next_cursor": "0:0", "state": "unknown", "status": "unknown"}, nil
		},
	})
	cs := connect(t)
	for tool, name := range map[string]string{"job_get": "job_get", "job_output": "job_output"} {
		res := callTool(t, cs, tool, map[string]any{"job_id": "j-1"})
		if res.IsError {
			t.Errorf("%s: IsError for job state: %s", tool, text(res))
		}
		if m := structured(t, res); m["code"] != nil || m["status"] != "unknown" {
			t.Errorf("%s: structured = %v", tool, m)
		}
		tx := text(res)
		if !strings.Contains(tx, "unknown") || !strings.Contains(tx, "files_stat") || strings.Contains(tx, name) {
			t.Errorf("%s text = %q", tool, tx)
		}
	}
}

func TestDialFailureIsEnglishWithCause(t *testing.T) {
	cs := connectWith(t, func(context.Context, string) (Caller, error) { return nil, errors.New("socket refused") })
	res := callTool(t, cs, "hosts_list", nil)
	m := structured(t, res)
	if !res.IsError || m["code"] != "OFFLINE" || m["retryable"] != true || !strings.Contains(m["message"].(string), "socket refused") {
		t.Fatalf("structured = %v", m)
	}
}

func TestSchemasRenderSingleBackslashPaths(t *testing.T) {
	res, err := connect(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range res.Tools {
		b, _ := json.Marshal(tool.InputSchema)
		var walk func(v any)
		walk = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, e := range x {
					if k == "description" {
						d, _ := e.(string)
						if strings.Contains(d, `\\`) {
							t.Errorf("%s: double backslash in %q", tool.Name, d)
						}
						if strings.Contains(d, `C:\`) {
							found = true
						}
					}
					walk(e)
				}
			case []any:
				for _, e := range x {
					walk(e)
				}
			}
		}
		var v any
		json.Unmarshal(b, &v)
		walk(v)
		if strings.Contains(tool.Description, `\\`) {
			t.Errorf("%s: double backslash in description", tool.Name)
		}
	}
	if !found {
		t.Fatal("no Windows path example found")
	}
}

func TestExclusiveAndSizeLimitsAreSchemaValidated(t *testing.T) {
	f := startFakeAPI(t, nil)
	cs := connect(t)
	bad := []struct {
		tool string
		args map[string]any
	}{
		{"exec_run", map[string]any{"host": "h"}},
		{"exec_run", map[string]any{"host": "h", "argv": []any{"a"}, "cmd": "b", "shell": "cmd"}},
		{"exec_start", map[string]any{"host": "h", "argv": []any{"a"}, "cmd": "b"}},
		{"proc_kill", map[string]any{"host": "h"}},
		{"proc_kill", map[string]any{"host": "h", "pid": 1, "name": "a.exe"}},
		{"files_write", map[string]any{"host": "h", "path": `C:\x`, "data": strings.Repeat("A", 1048577)}},
		{"job_output", map[string]any{"job_id": "j", "max": 262145}},
	}
	for _, c := range bad {
		if res := callTool(t, cs, c.tool, c.args); !res.IsError {
			t.Errorf("%s %v accepted", c.tool, c.args)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("daemon called: %v", f.calls)
	}
}

func TestSessionSummarySaysAnUnknownLockStateIsUnknown(t *testing.T) {
	res := success("session_inspect", spec{Method: "session.inspect"}, nil, map[string]any{"user": "me", "connect_state": "active"})
	if !strings.Contains(text(res), "locked=unknown") {
		t.Fatalf("text %q", text(res))
	}
	res = success("session_inspect", spec{Method: "session.inspect"}, nil, map[string]any{"user": "me", "locked": true})
	if !strings.Contains(text(res), "locked=true") {
		t.Fatalf("text %q", text(res))
	}
}

func TestExecRunCutByTheConnectionIsReportedInterrupted(t *testing.T) {
	env := map[string]any{"status": "failed", "action": map[string]any{"executed": true, "outcome": "done"}, "job_id": "j-1", "code": json.Number("-1"),
		"killed": "connection_closed", "stdout": "", "stderr": "", "next_action": "check_state",
		"error": map[string]any{"code": "OFFLINE", "message": "연결이 끊겨 중단", "retryable": false, "next_action": "check_state"}}
	res := success("exec_run", spec{Method: "exec.run", Mutating: true}, nil, env)
	if !res.IsError || !strings.Contains(text(res), "interrupted: the connection to the PC closed") {
		t.Fatalf("error %v text %q", res.IsError, text(res))
	}
}
