package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/client"
)

type rpcFail struct {
	code string
	msg  string
}

type handler func(params map[string]any, notify func(v any)) (any, *rpcFail)

type fake struct {
	mu       sync.Mutex
	handlers map[string]handler
	calls    []call
}

type call struct {
	method string
	params map[string]any
}

func (f *fake) called(method string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, c := range f.calls {
		if c.method == method {
			out = append(out, c.params)
		}
	}
	return out
}

func startFake(t *testing.T, handlers map[string]handler) *fake {
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
	f := &fake{handlers: handlers}
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

func (f *fake) serve(nc net.Conn) {
	defer nc.Close()
	var wmu sync.Mutex
	send := func(v any) {
		b, _ := json.Marshal(v)
		wmu.Lock()
		nc.Write(append(b, '\n'))
		wmu.Unlock()
	}
	sc := bufio.NewScanner(nc)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
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
			send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"client_id": "c-1", "daemon_version": "test", "api_version": 1}})
			continue
		}
		f.mu.Lock()
		f.calls = append(f.calls, call{req.Method, req.Params})
		h := f.handlers[req.Method]
		f.mu.Unlock()
		if req.Method == "events.subscribe" && h == nil {
			send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"subscribed": true}})
			continue
		}
		if h == nil {
			send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "모르는 메서드 " + req.Method,
				"data": map[string]any{"code": "INVALID_ARGUMENT"}}})
			continue
		}
		res, fail := h(req.Params, func(v any) { send(map[string]any{"jsonrpc": "2.0", "method": "event", "params": v}) })
		if fail != nil && fail.code == "DROP" {
			return
		}
		if fail != nil {
			send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32000, "message": fail.msg,
				"data": map[string]any{"code": fail.code}}})
			continue
		}
		send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": res})
	}
}

func ok(v any) handler {
	return func(map[string]any, func(any)) (any, *rpcFail) { return v, nil }
}

func envelope(status string, extra map[string]any) map[string]any {
	m := map[string]any{"schema_version": 1, "request_id": "r-1", "job_id": "j-1", "host_id": "windows-dev", "session_id": nil,
		"connection_generation": 1, "status": status, "action": map[string]any{"executed": true, "outcome": "done"},
		"error": nil, "next_action": nil, "artifacts": []any{}}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// execFake answers exec.start with a running job and jobs.output with the
// given final output.
func execFake(final map[string]any, extra map[string]handler) map[string]handler {
	h := map[string]handler{
		"exec.start":  ok(envelope("running", map[string]any{"pid": 1})),
		"jobs.output": ok(final),
	}
	for k, v := range extra {
		h[k] = v
	}
	return h
}

func exited(code int, stdout, stderr string) map[string]any {
	chunks := []any{}
	if stdout != "" {
		chunks = append(chunks, map[string]any{"stream": "stdout", "data": stdout})
	}
	if stderr != "" {
		chunks = append(chunks, map[string]any{"stream": "stderr", "data": stderr})
	}
	status := "succeeded"
	if code != 0 {
		status = "failed"
	}
	return map[string]any{"chunks": chunks, "next_cursor": "end", "state": "exited", "status": status, "code": code}
}

func runCtx(t *testing.T, ctx context.Context, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	app := New("test")
	app.Stdin, app.Stdout, app.Stderr = strings.NewReader(""), &out, &errb
	app.Interactive = func() bool { return false }
	app.PollInterval = time.Millisecond
	code := app.Run(ctx, args)
	return result{code, out.String(), errb.String()}
}

type result struct {
	code        int
	stdout, err string
}

func runCLI(t *testing.T, interactive bool, stdin string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	app := New("test")
	app.Stdin, app.Stdout, app.Stderr = strings.NewReader(stdin), &out, &errb
	app.Interactive = func() bool { return interactive }
	app.PollInterval = time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	code := app.Run(ctx, args)
	return result{code, out.String(), errb.String()}
}

func TestExecExitCodePassthrough(t *testing.T) {
	f := startFake(t, execFake(exited(7, "out", "err"), nil))
	r := runCLI(t, false, "", "exec", "windows-dev", "--cwd", `C:\w`, "--", "tool.exe", "--json", "a b")
	if r.code != 7 {
		t.Fatalf("exit code = %d, want 7 (stderr %q)", r.code, r.err)
	}
	if r.stdout != "out" || r.err != "err" {
		t.Fatalf("stdout %q stderr %q, want exact outputs", r.stdout, r.err)
	}
	p := f.called("exec.start")[0]
	argv, _ := p["argv"].([]any)
	if len(argv) != 3 || argv[1] != "--json" || argv[2] != "a b" || p["cwd"] != `C:\w` || p["host_id"] != "windows-dev" {
		t.Fatalf("exec.run params = %v", p)
	}
}

func TestExecFollowsACommandThatEndedBeforeTheStartAnswer(t *testing.T) {
	for _, status := range []string{"failed", "succeeded"} {
		code := 7
		if status == "succeeded" {
			code = 0
		}
		startFake(t, execFake(exited(code, "안녕\r\n", ""), map[string]handler{
			"exec.start": ok(envelope(status, map[string]any{"pid": 1, "remote_job": "j3"})),
		}))
		r := runCLI(t, false, "", "exec", "windows-dev", "--", "cmd", "/c", "echo 안녕 & exit 7")
		if r.code != code || r.stdout != "안녕\r\n" || r.err != "" {
			t.Fatalf("start %s: code %d stdout %q stderr %q", status, r.code, r.stdout, r.err)
		}
		r = runCLI(t, false, "", "exec", "windows-dev", "--detach", "--", "cmd", "/c", "exit 7")
		if r.code != 0 || r.stdout != "j-1\n" {
			t.Fatalf("detached start %s: code %d stdout %q stderr %q", status, r.code, r.stdout, r.err)
		}
	}
}

func TestExecShellTakesOneCommandString(t *testing.T) {
	f := startFake(t, execFake(exited(0, "", ""), nil))
	if r := runCLI(t, false, "", "exec", "h1", "--shell", "powershell", "--timeout", "5s", "--", "Get-Date -Format o"); r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	p := f.called("exec.start")[0]
	if p["cmd"] != "Get-Date -Format o" || p["shell"] != "powershell" || p["timeout_ms"] != float64(5000) || p["argv"] != nil {
		t.Fatalf("params = %v", p)
	}
	if r := runCLI(t, false, "", "exec", "h1", "--shell", "cmd", "--", "dir", "C:\\"); r.code != exitUsage {
		t.Fatalf("several args with --shell: exit %d, want 2", r.code)
	}
}

func TestExecDetachPrintsJobID(t *testing.T) {
	f := startFake(t, map[string]handler{"exec.start": ok(envelope("running", map[string]any{"pid": 9, "remote_job": "x"}))})
	r := runCLI(t, false, "", "exec", "h1", "--detach", "--", "long.exe")
	if r.code != 0 || r.stdout != "j-1\n" {
		t.Fatalf("code %d stdout %q stderr %q", r.code, r.stdout, r.err)
	}
	if !strings.Contains(r.err, "jobs watch j-1") {
		t.Fatalf("stderr %q lacks the next step", r.err)
	}
	if len(f.called("exec.start")) != 1 {
		t.Fatal("exec.start not called")
	}
}

func TestStatusExitCodes(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]any
		want int
		msg  string
	}{
		{"unknown", envelope("unknown", map[string]any{"error": map[string]any{"code": "EXECUTION_OUTCOME_UNKNOWN", "message": "응답을 잃었습니다"}}), 4, "jobs get j-1"},
		{"offline", envelope("failed", map[string]any{"error": map[string]any{"code": "OFFLINE", "message": "PC가 꺼져 있습니다"}}), 5, "PC가 꺼져 있습니다"},
		{"waiting", envelope("waiting_for_user", map[string]any{"error": map[string]any{"code": "AUTH_REQUIRED", "message": "UAC 승인이 필요합니다"}}), 3, "사람의 확인"},
		{"failed-unknown", envelope("failed", map[string]any{"error": map[string]any{"code": "EXECUTION_OUTCOME_UNKNOWN", "message": "응답을 잃었습니다"}}), 4, "j-1"},
		{"failed", envelope("failed", map[string]any{"error": map[string]any{"code": "INTERNAL", "message": "실패"}}), 1, "실패"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			startFake(t, map[string]handler{"exec.run": ok(tc.env), "proc.kill": ok(tc.env)})
			r := runCLI(t, false, "", "proc", "kill", "h1", "42")
			if r.code != tc.want || !strings.Contains(r.err, tc.msg) {
				t.Fatalf("code %d stderr %q, want %d containing %q", r.code, r.err, tc.want, tc.msg)
			}
		})
	}
}

func TestJSONHasSchemaVersion(t *testing.T) {
	startFake(t, map[string]handler{
		"host.list":   ok(map[string]any{"hosts": []any{map[string]any{"host_id": "windows-dev", "name": "windows-dev"}}}),
		"exec.start":  ok(envelope("running", nil)),
		"jobs.output": ok(exited(0, "hi", "")),
	})
	for _, args := range [][]string{{"--json", "host", "list"}, {"exec", "h1", "--json", "--", "echo"}} {
		r := runCLI(t, false, "", args...)
		var m map[string]any
		if err := json.Unmarshal([]byte(r.stdout), &m); err != nil {
			t.Fatalf("%v: stdout %q is not JSON: %v", args, r.stdout, err)
		}
		if m["schema_version"] != float64(1) {
			t.Fatalf("%v: schema_version = %v", args, m["schema_version"])
		}
	}
}

func TestHostPathParsing(t *testing.T) {
	good := []struct{ in, host, path string }{
		{`windows-dev:C:\qa\a b.txt`, "windows-dev", `C:\qa\a b.txt`},
		{`windows-dev:'C:\한글\파일.txt'`, "windows-dev", `C:\한글\파일.txt`},
		{`windows-dev:"C:\Users\me\😀 folder"`, "windows-dev", `C:\Users\me\😀 folder`},
		{`pc1:/tmp/x`, "pc1", "/tmp/x"},
		{`host:C:\a:b`, "host", `C:\a:b`},
		{`host:\\srv\share\x`, "host", `\\srv\share\x`},
	}
	for _, g := range good {
		got, err := parseRemote(g.in)
		if err != nil || got.Host != g.host || got.Path != g.path {
			t.Errorf("parseRemote(%q) = %+v, %v; want %s %s", g.in, got, err, g.host, g.path)
		}
	}
	for _, bad := range []string{`C:\x`, `c:/x`, `nocolon`, `:path`, `host:`, `pc1.local:`, `a1:`, `/tmp/a:b`, `\\server\share`, "a b:c"} {
		if _, err := parseRemote(bad); err == nil {
			t.Errorf("parseRemote(%q) succeeded, want usage error", bad)
		}
	}
	if _, err := parseRemote(`C:\x`); err == nil || !strings.Contains(err.Error(), "호스트") {
		t.Errorf("bare drive error = %v, want a sentence about the host prefix", err)
	}
	r := runCLI(t, false, "", "files", "ls", `C:\x`)
	if r.code != exitUsage {
		t.Errorf("bare drive exit = %d, want 2", r.code)
	}
}

func TestDiagnosticsOnStderr(t *testing.T) {
	startFake(t, map[string]handler{
		"jobs.output": func(map[string]any, func(any)) (any, *rpcFail) {
			return nil, &rpcFail{"NOT_FOUND", "job j-9를 찾을 수 없습니다. jobs list로 확인하세요"}
		},
		"host.add": func(p map[string]any, notify func(any)) (any, *rpcFail) {
			notify(map[string]any{"kind": "host.install.progress", "name": "pc9", "step": "copy", "status": "active"})
			notify(map[string]any{"kind": "host.install.progress", "name": "pc9", "step": "copy", "status": "done"})
			return envelope("succeeded", map[string]any{"host": map[string]any{"state": "ready"}}), nil
		},
	})
	r := runCLI(t, false, "", "jobs", "watch", "j-9")
	if r.code != 1 || r.stdout != "" || !strings.Contains(r.err, "j-9") {
		t.Fatalf("code %d stdout %q stderr %q", r.code, r.stdout, r.err)
	}
	r = runCLI(t, false, "", "host", "add", "pc9", "--ssh", "pc9")
	if r.code != 0 || r.stdout != "PC 등록 완료: pc9 (상태 ready)\n" {
		t.Fatalf("host add: code %d stdout %q stderr %q", r.code, r.stdout, r.err)
	}
	for _, want := range []string{"[진행] 에이전트 복사", "[완료] 에이전트 복사"} {
		if !strings.Contains(r.err, want) {
			t.Errorf("stderr %q lacks %q", r.err, want)
		}
	}
}

func TestInstallProgressShowsADifferingDetailOnce(t *testing.T) {
	startFake(t, map[string]handler{
		"host.add": func(p map[string]any, notify func(any)) (any, *rpcFail) {
			for _, ev := range [][3]string{{"copy", "active", "에이전트 복사"}, {"copy", "done", "에이전트 복사"},
				{"init", "active", "에이전트 키 만들기"}, {"init", "done", "에이전트 키 만들기"}} {
				notify(map[string]any{"kind": "host.install.progress", "name": "pc9", "step": ev[0], "status": ev[1], "detail": ev[2]})
			}
			return envelope("succeeded", map[string]any{"host": map[string]any{"state": "ready"}}), nil
		},
	})
	r := runCLI(t, false, "", "host", "add", "pc9", "--ssh", "pc9")
	want := "[진행] 에이전트 복사 ...\n[완료] 에이전트 복사\n[진행] 에이전트 초기화 ...\n       에이전트 키 만들기\n[완료] 에이전트 초기화\n"
	if r.code != 0 || r.err != want {
		t.Fatalf("code %d stderr %q, want %q", r.code, r.err, want)
	}
}

func TestHostAddForceAndItsNextStep(t *testing.T) {
	f := startFake(t, map[string]handler{
		"host.add": func(p map[string]any, notify func(any)) (any, *rpcFail) {
			if p["force"] == true {
				return envelope("succeeded", map[string]any{"host": map[string]any{"state": "ready"}}), nil
			}
			env := envelope("failed", nil)
			env["action"] = map[string]any{"executed": false, "outcome": "not_started"}
			env["error"] = map[string]any{"code": "INVALID_ARGUMENT", "message": "이 PC에는 이미 Deskhand 에이전트가 페어링돼 있습니다",
				"next_action": "host_add_force"}
			return env, nil
		},
	})
	r := runCLI(t, false, "", "host", "add", "사무실 PC", "--ssh", "win")
	if r.code != 1 || !strings.Contains(r.err, "다음 단계: deskhand host add '사무실 PC' --ssh win --force 를 실행하세요") {
		t.Fatalf("code %d stderr %q", r.code, r.err)
	}
	if p := f.called("host.add")[0]; p["force"] != nil {
		t.Fatalf("plain add sent force: %v", p)
	}
	r = runCLI(t, false, "", "host", "add", "사무실 PC", "--ssh", "win", "--force")
	if r.code != 0 {
		t.Fatalf("add --force: code %d stderr %q", r.code, r.err)
	}
	if p := f.called("host.add")[1]; p["force"] != true || p["name"] != "사무실 PC" || p["ssh"] != "win" {
		t.Fatalf("add --force params %v", p)
	}
}

func TestJobsWatchFollowsCursor(t *testing.T) {
	var mu sync.Mutex
	var cursors []string
	startFake(t, map[string]handler{"jobs.output": func(p map[string]any, _ func(any)) (any, *rpcFail) {
		mu.Lock()
		defer mu.Unlock()
		c, _ := p["cursor"].(string)
		cursors = append(cursors, c)
		switch c {
		case "":
			return map[string]any{"chunks": []any{map[string]any{"stream": "stdout", "data": "a"}}, "next_cursor": "1:0", "state": "running", "status": "running"}, nil
		case "1:0":
			return map[string]any{"chunks": []any{map[string]any{"stream": "stderr", "data": "e"}, map[string]any{"stream": "stdout", "data": "b"}},
				"next_cursor": "end", "state": "exited", "status": "failed", "code": 3}, nil
		}
		return nil, &rpcFail{"INTERNAL", "unexpected cursor " + c}
	}})
	r := runCLI(t, false, "", "jobs", "watch", "j-1")
	if r.code != 3 || r.stdout != "ab" || r.err != "e" {
		t.Fatalf("code %d stdout %q stderr %q", r.code, r.stdout, r.err)
	}
	if strings.Join(cursors, ",") != ",1:0" {
		t.Fatalf("cursors = %v", cursors)
	}
	r = runCLI(t, false, "", "jobs", "watch", "j-1", "--jsonl")
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if len(lines) != 5 {
		t.Fatalf("jsonl lines = %d: %q", len(lines), r.stdout)
	}
	for _, l := range lines {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) != nil || m["schema_version"] != float64(1) {
			t.Fatalf("line %q is not a JSON object with schema_version", l)
		}
	}
}

func TestFilesRmTrashesByDefault(t *testing.T) {
	f := startFake(t, map[string]handler{"files.trash": ok(envelope("succeeded", nil)), "files.delete": ok(envelope("succeeded", nil))})
	r := runCLI(t, false, "", "files", "rm", `pc1:C:\old\a b.txt`)
	if r.code != 0 || len(f.called("files.trash")) != 1 || len(f.called("files.delete")) != 0 {
		t.Fatalf("code %d trash %v delete %v", r.code, f.called("files.trash"), f.called("files.delete"))
	}
	if p := f.called("files.trash")[0]; p["path"] != `C:\old\a b.txt` || p["host_id"] != "pc1" {
		t.Fatalf("params = %v", p)
	}
}

func TestFilesRmPermanentNeedsConfirmation(t *testing.T) {
	f := startFake(t, map[string]handler{"files.delete": ok(envelope("succeeded", nil))})
	r := runCLI(t, false, "", "files", "rm", "--permanent", "pc1:C:\\x")
	if r.code != exitUsage || len(f.called("files.delete")) != 0 || !strings.Contains(r.err, "--yes") {
		t.Fatalf("non-interactive: code %d stderr %q", r.code, r.err)
	}
	r = runCLI(t, true, "n\n", "files", "rm", "--permanent", "pc1:C:\\x")
	if r.code == 0 || len(f.called("files.delete")) != 0 {
		t.Fatalf("declined: code %d calls %v", r.code, f.called("files.delete"))
	}
	r = runCLI(t, true, "y\n", "files", "rm", "--permanent", "pc1:C:\\x")
	if r.code != 0 || len(f.called("files.delete")) != 1 || f.called("files.delete")[0]["permanent"] != true {
		t.Fatalf("confirmed: code %d calls %v", r.code, f.called("files.delete"))
	}
	r = runCLI(t, false, "", "files", "rm", "--permanent", "--yes", "pc1:C:\\y")
	if r.code != 0 || len(f.called("files.delete")) != 2 {
		t.Fatalf("--yes: code %d", r.code)
	}
}

func TestFilesCatDecodesChunks(t *testing.T) {
	var offsets []float64
	startFake(t, map[string]handler{"files.read": func(p map[string]any, _ func(any)) (any, *rpcFail) {
		off, _ := p["offset"].(float64)
		offsets = append(offsets, off)
		if off == 0 {
			return map[string]any{"data": "aGVsbG8g", "size": 12, "eof": false}, nil
		}
		return map[string]any{"data": "d29ybGQh", "size": 12, "eof": true}, nil
	}})
	r := runCLI(t, false, "", "files", "cat", "pc1:C:\\a.txt")
	if r.code != 0 || r.stdout != "hello world!" || len(offsets) != 2 || offsets[1] != 6 {
		t.Fatalf("code %d stdout %q offsets %v", r.code, r.stdout, offsets)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	for _, args := range [][]string{{}, {"nope"}, {"exec", "h1"}, {"host", "add", "x"}, {"jobs", "watch"}, {"files", "mv", "a:b", "c:d"}} {
		if r := runCLI(t, false, "", args...); r.code != exitUsage {
			t.Errorf("%v: exit %d, want 2 (stderr %q)", args, r.code, r.err)
		}
	}
}

func TestExecWaitingForUserExitsThree(t *testing.T) {
	startFake(t, map[string]handler{"exec.start": ok(envelope("waiting_for_user", map[string]any{
		"error": map[string]any{"code": "ELEVATION_REQUIRED", "message": "UAC 승인이 필요합니다"}}))})
	r := runCLI(t, false, "", "exec", "h1", "--", "installer.exe")
	if r.code != exitNeedsUser || r.stdout != "" || !strings.Contains(r.err, "사람의 확인") {
		t.Fatalf("code %d stdout %q stderr %q", r.code, r.stdout, r.err)
	}
	r = runCLI(t, false, "", "exec", "h1", "--json", "--", "installer.exe")
	var m map[string]any
	if r.code != exitNeedsUser || json.Unmarshal([]byte(r.stdout), &m) != nil || m["status"] != "waiting_for_user" {
		t.Fatalf("json: code %d stdout %q", r.code, r.stdout)
	}
}

// timedOut is the last jobs.output page of a job the daemon ended at its
// timeout.
var timedOut = map[string]any{"chunks": []any{map[string]any{"stream": "stdout", "data": "tail"}}, "next_cursor": "end", "state": "exited",
	"status": "failed", "code": 1, "timed_out": true, "error": map[string]any{"code": "TIMEOUT", "message": "시간 초과"}}

func TestExecTimeoutIsEnforcedByTheDaemonAndExits124(t *testing.T) {
	f := startFake(t, execFake(nil, map[string]handler{"jobs.output": runningThenFinal(timedOut), "jobs.cancel": ok(envelope("cancelled", nil))}))
	r := runCLI(t, false, "", "exec", "h1", "--timeout", "30ms", "--", "slow")
	if r.code != 124 || r.stdout != "early tail" || !strings.Contains(r.err, "30ms 안에 끝나지 않아 종료했습니다") {
		t.Fatalf("code %d stdout %q stderr %q", r.code, r.stdout, r.err)
	}
	if len(f.called("jobs.cancel")) != 0 || f.called("exec.start")[0]["timeout_ms"] != float64(30) {
		t.Fatalf("cancel %v start %v", f.called("jobs.cancel"), f.called("exec.start"))
	}
	startFake(t, execFake(nil, map[string]handler{"jobs.output": runningThenFinal(timedOut)}))
	r = runCLI(t, false, "", "--json", "exec", "h1", "--timeout", "30ms", "--", "slow")
	var m map[string]any
	if r.code != 124 || json.Unmarshal([]byte(r.stdout), &m) != nil || m["timed_out"] != true || m["status"] != "failed" ||
		m["error"].(map[string]any)["code"] != "TIMEOUT" || m["stdout"] != "early tail" {
		t.Fatalf("json: code %d stdout %q", r.code, r.stdout)
	}
	r = runCLI(t, false, "", "jobs", "watch", "j-1")
	if r.code != 124 || !strings.Contains(r.err, "j-1") {
		t.Fatalf("watch: code %d stderr %q", r.code, r.err)
	}
}

func TestExecPastItsTimeoutWaitsForTheDaemon(t *testing.T) {
	f := startFake(t, execFake(nil, map[string]handler{"jobs.output": runningThenFinal(exited(5, "late", ""))}))
	r := runCLI(t, false, "", "exec", "h1", "--timeout", "30ms", "--", "slow")
	if r.code != 5 || r.stdout != "early late" || strings.Contains(r.err, "종료했습니다") || len(f.called("jobs.cancel")) != 0 {
		t.Fatalf("code %d stdout %q stderr %q", r.code, r.stdout, r.err)
	}
}

func TestExecDetachRejectsTimeout(t *testing.T) {
	if r := runCLI(t, false, "", "exec", "h1", "--detach", "--timeout", "5s", "--", "x"); r.code != exitUsage {
		t.Fatalf("exit %d, want 2", r.code)
	}
}

func TestExecTimeoutZeroNeverTimesOut(t *testing.T) {
	var mu sync.Mutex
	n := 0
	f := startFake(t, execFake(nil, map[string]handler{"jobs.output": func(map[string]any, func(any)) (any, *rpcFail) {
		mu.Lock()
		defer mu.Unlock()
		n++
		if n < 5 {
			time.Sleep(10 * time.Millisecond)
			return map[string]any{"chunks": []any{}, "next_cursor": "0:0", "state": "running", "status": "running"}, nil
		}
		return exited(9, "done", ""), nil
	}}))
	r := runCLI(t, false, "", "exec", "h1", "--timeout", "0", "--", "x")
	if r.code != 9 || r.stdout != "done" || len(f.called("jobs.cancel")) != 0 {
		t.Fatalf("code %d stdout %q stderr %q", r.code, r.stdout, r.err)
	}
	if p := f.called("exec.start")[0]; p["timeout_ms"] != nil {
		t.Fatalf("params = %v", p)
	}
}

func TestExecCtrlCExitsFourWithJobID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startFake(t, execFake(nil, map[string]handler{"jobs.output": func(map[string]any, func(any)) (any, *rpcFail) {
		cancel()
		return map[string]any{"chunks": []any{}, "next_cursor": "0:0", "state": "running", "status": "running"}, nil
	}}))
	r := runCtx(t, ctx, "exec", "h1", "--", "x")
	if r.code != exitUnknown || !strings.Contains(r.err, "deskhand jobs get j-1") || strings.Contains(r.err, "<job-id>") {
		t.Fatalf("code %d stderr %q", r.code, r.err)
	}
}

func TestExecLostConnectionDuringFollowExitsFourWithJobID(t *testing.T) {
	startFake(t, execFake(nil, map[string]handler{"jobs.output": func(map[string]any, func(any)) (any, *rpcFail) { return nil, &rpcFail{"DROP", ""} }}))
	r := runCLI(t, false, "", "exec", "h1", "--json", "--", "x")
	if r.code != exitUnknown || !strings.Contains(r.err, "deskhand jobs get j-1") {
		t.Fatalf("code %d stderr %q", r.code, r.err)
	}
	var m map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(r.stdout)), &m) != nil || m["job_id"] != "j-1" || m["request_id"] != "r-1" {
		t.Fatalf("stdout %q lacks ids", r.stdout)
	}
}

func TestStartedExecWithUnexpectedStatusExitsFour(t *testing.T) {
	for _, st := range []string{"cancelled", "bogus"} {
		startFake(t, map[string]handler{"exec.start": ok(envelope(st, nil))})
		r := runCLI(t, false, "", "exec", "h1", "--", "x")
		want := exitUnknown
		if st == "cancelled" {
			want = exitFailure
		}
		if r.code != want {
			t.Errorf("%s: code %d stderr %q, want %d", st, r.code, r.err, want)
		}
	}
}

func TestFinalCallWithRunningOrCancelledStatus(t *testing.T) {
	startFake(t, map[string]handler{"proc.kill": ok(envelope("running", nil)), "files.mkdir": ok(envelope("cancelled", nil))})
	if r := runCLI(t, false, "", "proc", "kill", "h1", "5"); r.code != exitUnknown || !strings.Contains(r.err, "예상하지 못한 상태") {
		t.Errorf("running: code %d stderr %q", r.code, r.err)
	}
	if r := runCLI(t, false, "", "files", "mkdir", "h1:/x"); r.code != exitFailure || !strings.Contains(r.err, "취소") {
		t.Errorf("cancelled: code %d stderr %q", r.code, r.err)
	}
}

func TestLostConnectionDuringMutationExitsFour(t *testing.T) {
	startFake(t, map[string]handler{"proc.kill": func(map[string]any, func(any)) (any, *rpcFail) {
		return nil, &rpcFail{"EXECUTION_OUTCOME_UNKNOWN", "응답을 잃었습니다"}
	}})
	r := runCLI(t, false, "", "proc", "kill", "h1", "5")
	if r.code != exitUnknown || !strings.Contains(r.err, "jobs list --host h1") || strings.Contains(r.err, "<job-id>") {
		t.Fatalf("code %d stderr %q", r.code, r.err)
	}
}

func TestDroppedConnectionDuringMutationExitsFour(t *testing.T) {
	startFake(t, map[string]handler{"files.mkdir": func(map[string]any, func(any)) (any, *rpcFail) { return nil, &rpcFail{"DROP", ""} }})
	r := runCLI(t, false, "", "files", "mkdir", `pc1:C:\new`)
	if r.code != exitUnknown || !strings.Contains(r.err, "jobs list --host pc1") {
		t.Fatalf("code %d stderr %q", r.code, r.err)
	}
}

func TestFollowReadsPastTheExitUntilTheEndCursor(t *testing.T) {
	pages := map[string]map[string]any{
		"":    {"chunks": []any{map[string]any{"stream": "stdout", "data": "first "}}, "next_cursor": "6:0", "state": "exited", "status": "succeeded", "code": 0},
		"6:0": {"chunks": []any{map[string]any{"stream": "stdout", "data": "second "}}, "next_cursor": "13:0", "state": "exited", "status": "succeeded", "code": 0},
		"13:0": {"chunks": []any{map[string]any{"stream": "stdout", "data": "last"}, map[string]any{"stream": "stderr", "data": "e"}},
			"next_cursor": "end", "state": "exited", "status": "succeeded", "code": 0},
	}
	output := func(p map[string]any, _ func(any)) (any, *rpcFail) {
		c, _ := p["cursor"].(string)
		if page, ok := pages[c]; ok {
			return page, nil
		}
		return nil, &rpcFail{"INTERNAL", "unexpected cursor " + c}
	}
	startFake(t, execFake(nil, map[string]handler{"jobs.output": output}))
	for _, args := range [][]string{{"exec", "h1", "--", "x"}, {"jobs", "watch", "j-1"}} {
		r := runCLI(t, false, "", args...)
		if r.code != 0 || r.stdout != "first second last" || r.err != "e" {
			t.Fatalf("%v: code %d stdout %q stderr %q", args, r.code, r.stdout, r.err)
		}
	}
}

func TestJobsWatchWaitingForUserExitsThree(t *testing.T) {
	startFake(t, map[string]handler{"jobs.output": ok(map[string]any{"chunks": []any{}, "next_cursor": "0:0", "state": "running", "status": "waiting_for_user"})})
	r := runCLI(t, false, "", "jobs", "watch", "j-5")
	if r.code != exitNeedsUser || !strings.Contains(r.err, "j-5") {
		t.Fatalf("code %d stderr %q", r.code, r.err)
	}
}

func TestRemoteExitCodeNeverBecomesZero(t *testing.T) {
	for in, want := range map[int64]int{0: 0, 7: 7, 255: 255, 256: 1, 512: 1, -1: 1, -256: 1, 0xC0000005: 1} {
		if got := remoteExitCode(in); got != want {
			t.Errorf("remoteExitCode(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestExecHelpDocumentsExitCodes(t *testing.T) {
	r := runCLI(t, false, "", "exec", "--help")
	if r.code != 0 || !strings.Contains(r.stdout, "1-255") || !strings.Contains(r.stdout, "--json") {
		t.Fatalf("code %d stdout %q", r.code, r.stdout)
	}
}

func TestErrorShowsNextAction(t *testing.T) {
	startFake(t, map[string]handler{"jobs.get": func(map[string]any, func(any)) (any, *rpcFail) {
		return nil, &rpcFail{"NOT_FOUND", "job을 찾을 수 없습니다"}
	},
		"host.list": ok(map[string]any{"hosts": []any{}}),
		"proc.kill": ok(envelope("failed", map[string]any{"next_action": "host_repair",
			"error": map[string]any{"code": "INVALID_ARGUMENT", "message": "이미 페어링됐습니다", "next_action": "host_repair"}}))})
	r := runCLI(t, false, "", "jobs", "get", "j-x")
	if r.code != 1 || !strings.Contains(r.err, "job을 찾을 수 없습니다") {
		t.Fatalf("code %d stderr %q", r.code, r.err)
	}
	r = runCLI(t, false, "", "proc", "kill", "h1", "5")
	if r.code != 1 || !strings.Contains(r.err, "다음 단계: deskhand host repair <이름> 을 실행하세요") {
		t.Fatalf("code %d stderr %q", r.code, r.err)
	}
}

func TestConnectionLostIsDetectedThroughWrapping(t *testing.T) {
	startFake(t, map[string]handler{"files.mkdir": func(map[string]any, func(any)) (any, *rpcFail) { return nil, &rpcFail{"DROP", ""} }})
	c, err := client.DialOptions(context.Background(), "cli", "t", client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	err = c.Call(context.Background(), "files.mkdir", map[string]any{}, nil)
	if !client.IsConnectionLost(err) || !client.IsConnectionLost(fmt.Errorf("wrapped: %w", err)) {
		t.Fatalf("IsConnectionLost(%v) = false", err)
	}
}

func runningThenFinal(final map[string]any) handler {
	var mu sync.Mutex
	n := 0
	return func(map[string]any, func(any)) (any, *rpcFail) {
		mu.Lock()
		defer mu.Unlock()
		n++
		if n == 1 {
			time.Sleep(60 * time.Millisecond)
			return map[string]any{"chunks": []any{map[string]any{"stream": "stdout", "data": "early "}}, "next_cursor": "6:0", "state": "running", "status": "running"}, nil
		}
		return final, nil
	}
}

func TestFollowErrorExitsFourWithJobID(t *testing.T) {
	startFake(t, execFake(nil, map[string]handler{
		"jobs.output": func(map[string]any, func(any)) (any, *rpcFail) { return nil, &rpcFail{"INTERNAL", "내부 오류"} }}))
	r := runCLI(t, false, "", "exec", "h1", "--", "x")
	if r.code != exitUnknown || !strings.Contains(r.err, "내부 오류") || !strings.Contains(r.err, "deskhand jobs get j-1") {
		t.Fatalf("code %d stderr %q", r.code, r.err)
	}
}

func TestWatchEndedWithoutExitStatusExitsFour(t *testing.T) {
	startFake(t, map[string]handler{"jobs.output": ok(map[string]any{"chunks": []any{}, "next_cursor": "end", "state": "exited", "status": "running"})})
	r := runCLI(t, false, "", "jobs", "watch", "j-7")
	if r.code != exitUnknown || !strings.Contains(r.err, "deskhand jobs get j-7") {
		t.Fatalf("code %d stderr %q", r.code, r.err)
	}
}

func TestJobsCancelLostConnectionNamesJob(t *testing.T) {
	startFake(t, map[string]handler{"jobs.cancel": func(map[string]any, func(any)) (any, *rpcFail) { return nil, &rpcFail{"DROP", ""} }})
	r := runCLI(t, false, "", "jobs", "cancel", "j-8")
	if r.code != exitUnknown || !strings.Contains(r.err, "deskhand jobs get j-8") {
		t.Fatalf("code %d stderr %q", r.code, r.err)
	}
}

func TestJobsGetShowsTheOutputSizeNotTheOutput(t *testing.T) {
	startFake(t, map[string]handler{"jobs.get": ok(map[string]any{"job_id": "j-3", "host_id": "pc", "kind": "exec", "status": "succeeded",
		"summary": "echo hi", "updated_at": "2026-10-20T00:00:00.000Z", "result": map[string]any{"code": 0}, "output_bytes": 12})})
	r := runCLI(t, false, "", "jobs", "get", "j-3")
	if r.code != 0 || !strings.Contains(r.stdout, "출력: 12바이트 (deskhand jobs watch j-3 로 보기)") {
		t.Fatalf("code %d stdout %q", r.code, r.stdout)
	}
}

func TestJobsGetSaysACommandCutByTheConnectionWasInterrupted(t *testing.T) {
	startFake(t, map[string]handler{"jobs.get": ok(map[string]any{"job_id": "j-4", "host_id": "pc", "kind": "exec", "status": "failed",
		"summary": "sleep", "updated_at": "2026-10-20T00:00:00.000Z", "result": map[string]any{"code": -1, "killed": "connection_closed"}})})
	r := runCLI(t, false, "", "jobs", "get", "j-4")
	if r.code != 0 || !strings.Contains(r.stdout, "중단됨: PC와의 연결이 끊겨") {
		t.Fatalf("code %d stdout %q", r.code, r.stdout)
	}
}
