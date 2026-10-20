package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func xfer(extra map[string]any) map[string]any {
	m := map[string]any{"transfer_id": "t-1", "host_id": "pc1", "direction": "push", "local": "/tmp/build", "remote": `C:\qa\build`,
		"on_conflict": "fail", "state": "done", "bytes_done": 1000, "bytes_total": 1000, "files_done": 2, "files_failed": 0, "files_total": 2,
		"rate_bps": 0, "outcome": "written", "rev": 5, "updated_at": "2026-10-20T00:00:00.000Z", "files": []any{}}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func startOK() handler {
	return ok(envelope("running", map[string]any{"transfer_id": "t-1", "job_id": "j-7"}))
}

func runProgress(t *testing.T, progress bool, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	app := New("test")
	app.Stdin, app.Stdout, app.Stderr = strings.NewReader(""), &out, &errb
	app.Interactive = func() bool { return false }
	app.Progress = func() bool { return progress }
	app.PollInterval = time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	code := app.Run(ctx, args)
	return result{code, out.String(), errb.String()}
}

func TestFilesPushMapsToTransferStart(t *testing.T) {
	src := filepath.Join(t.TempDir(), "build")
	f := startFake(t, map[string]handler{"transfer.start": startOK(), "transfer.get": ok(xfer(nil))})
	r := runCLI(t, false, "", "files", "push", src, `pc1:C:\qa\build`, "--on-conflict", "replace")
	if r.code != 0 || !strings.Contains(r.stdout, "올렸습니다") {
		t.Fatalf("code %d stdout %q stderr %q", r.code, r.stdout, r.err)
	}
	p := f.called("transfer.start")[0]
	if p["direction"] != "push" || p["host_id"] != "pc1" || p["local"] != src || p["remote"] != `C:\qa\build` || p["on_conflict"] != "replace" {
		t.Fatalf("params = %v", p)
	}
}

func TestFilesPushToFolderTargetAppendsSourceName(t *testing.T) {
	src := filepath.Join(t.TempDir(), "build")
	f := startFake(t, map[string]handler{"transfer.start": startOK(), "transfer.get": ok(xfer(nil))})
	runCLI(t, false, "", "files", "push", src, `pc1:C:\qa\`)
	if p := f.called("transfer.start")[0]; p["remote"] != `C:\qa\build` {
		t.Fatalf("remote = %v", p["remote"])
	}
}

func TestFilesPullIntoExistingFolderAppendsRemoteName(t *testing.T) {
	dir := t.TempDir()
	f := startFake(t, map[string]handler{"transfer.start": startOK(), "transfer.get": ok(xfer(map[string]any{"direction": "pull"}))})
	r := runCLI(t, false, "", "files", "pull", `pc1:C:\logs\app.log`, dir)
	if r.code != 0 || !strings.Contains(r.stdout, "받았습니다") {
		t.Fatalf("code %d stdout %q stderr %q", r.code, r.stdout, r.err)
	}
	p := f.called("transfer.start")[0]
	if p["direction"] != "pull" || p["local"] != filepath.Join(dir, "app.log") || p["remote"] != `C:\logs\app.log` {
		t.Fatalf("params = %v", p)
	}
}

func TestFilesMoveDirectionFollowsWhichSideIsRemote(t *testing.T) {
	f := startFake(t, map[string]handler{"transfer.start": startOK(), "transfer.get": ok(xfer(map[string]any{"direction": "move_pull"}))})
	runCLI(t, false, "", "files", "move", `pc1:C:\out\a.zip`, "/tmp/a.zip")
	runCLI(t, false, "", "files", "move", "/tmp/b.zip", `pc1:C:\in\b.zip`)
	got := f.called("transfer.start")
	if len(got) != 2 || got[0]["direction"] != "move_pull" || got[1]["direction"] != "move_push" {
		t.Fatalf("starts = %v", got)
	}
	if r := runCLI(t, false, "", "files", "move", "/tmp/a", "/tmp/b"); r.code != exitUsage {
		t.Errorf("both local: exit %d", r.code)
	}
	if r := runCLI(t, false, "", "files", "move", "pc1:/a", "pc1:/b"); r.code != exitUsage {
		t.Errorf("both remote: exit %d", r.code)
	}
}

func TestMoveResultSentenceStatesSourceOutcome(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string]any
		want  string
		code  int
	}{
		{"trashed", map[string]any{"direction": "move_pull"}, "원본을 휴지통으로 옮겼습니다", 0},
		{"deleted", map[string]any{"direction": "move_push"}, "원본을 지웠습니다", 0},
		{"kept", map[string]any{"direction": "move_pull", "outcome": "copied_source_retained", "files_total": 3, "files_done": 3,
			"files": []any{map[string]any{"rel": "a", "state": "done", "outcome": "skipped"}, map[string]any{"rel": "b", "state": "failed"},
				map[string]any{"rel": "c", "state": "done", "outcome": "skipped"}}}, "원본을 남겼습니다: 3개 파일이 옮겨지지 않았습니다", 1},
	}
	for _, c := range cases {
		startFake(t, map[string]handler{"transfer.start": startOK(), "transfer.get": ok(xfer(c.extra))})
		args := []string{"files", "move", `pc1:C:\x`, "/tmp/x"}
		if c.extra["direction"] == "move_push" {
			args = []string{"files", "move", "/tmp/x", `pc1:C:\x`}
		}
		r := runCLI(t, false, "", args...)
		if r.code != c.code || !strings.Contains(r.stdout+r.err, c.want) {
			t.Errorf("%s: code %d stdout %q stderr %q", c.name, r.code, r.stdout, r.err)
		}
	}
}

func TestPartialFailureExitsOneAndJSONHasFileDetail(t *testing.T) {
	files := []any{map[string]any{"rel": "a.txt", "state": "done", "outcome": "written"},
		map[string]any{"rel": "b.txt", "state": "failed", "error": map[string]any{"code": "PERMISSION_DENIED", "message": "접근이 거부됐습니다"}}}
	startFake(t, map[string]handler{"transfer.start": startOK(), "transfer.get": ok(xfer(map[string]any{"state": "failed", "outcome": "partial",
		"files_failed": 1, "files": files}))})
	r := runCLI(t, false, "", "files", "push", "/tmp/build", `pc1:C:\qa\build`)
	if r.code != 1 || !strings.Contains(r.err, "b.txt: 접근이 거부됐습니다") || !strings.Contains(r.err, "transfer resume t-1") {
		t.Fatalf("human: code %d stderr %q", r.code, r.err)
	}
	r = runCLI(t, false, "", "--json", "files", "push", "/tmp/build", `pc1:C:\qa\build`)
	var m map[string]any
	if r.code != 1 || json.Unmarshal([]byte(r.stdout), &m) != nil || m["state"] != "failed" || len(m["files"].([]any)) != 2 || m["job_id"] != "j-7" {
		t.Fatalf("json: code %d stdout %q", r.code, r.stdout)
	}
}

func TestMoveJSONCarriesSourceOutcome(t *testing.T) {
	startFake(t, map[string]handler{"transfer.start": startOK(), "transfer.get": ok(xfer(map[string]any{"direction": "move_pull"}))})
	r := runCLI(t, false, "", "--json", "files", "move", `pc1:C:\x`, "/tmp/x")
	var m map[string]any
	if r.code != 0 || json.Unmarshal([]byte(r.stdout), &m) != nil || m["source"] != "trashed" {
		t.Fatalf("code %d stdout %q", r.code, r.stdout)
	}
}

func TestStartRefusedExitsOneWithoutPolling(t *testing.T) {
	f := startFake(t, map[string]handler{"transfer.start": ok(envelope("failed", map[string]any{"transfer_id": nil,
		"action": map[string]any{"executed": false, "outcome": "not_started"},
		"error":  map[string]any{"code": "NOT_FOUND", "message": "원본이 없습니다"}}))})
	r := runCLI(t, false, "", "files", "push", "/tmp/none", `pc1:C:\x`)
	if r.code != 1 || !strings.Contains(r.err, "원본이 없습니다") || len(f.called("transfer.get")) != 0 {
		t.Fatalf("code %d stderr %q", r.code, r.err)
	}
}

func TestProgressBarOnTerminalOnly(t *testing.T) {
	var mu sync.Mutex
	n := 0
	startFake(t, map[string]handler{"transfer.start": startOK(), "transfer.get": func(map[string]any, func(any)) (any, *rpcFail) {
		mu.Lock()
		defer mu.Unlock()
		n++
		if n < 3 {
			return xfer(map[string]any{"state": "running", "bytes_done": 500, "rate_bps": 2000000, "rev": n}), nil
		}
		return xfer(map[string]any{"rev": 10}), nil
	}})
	r := runProgress(t, true, "files", "push", "/tmp/build", `pc1:C:\x`)
	if r.code != 0 || !strings.Contains(r.err, "50%") || !strings.Contains(r.err, "MB/s") || !strings.Contains(r.err, "남은 시간") {
		t.Fatalf("tty: code %d stderr %q", r.code, r.err)
	}
	n = 0
	r = runProgress(t, false, "files", "push", "/tmp/build", `pc1:C:\x`)
	if r.code != 0 || r.err != "" || !strings.Contains(r.stdout, "올렸습니다") {
		t.Fatalf("no tty: code %d stdout %q stderr %q", r.code, r.stdout, r.err)
	}
}

func TestJSONLStreamsTransferUpdatedEvents(t *testing.T) {
	var mu sync.Mutex
	n := 0
	startFake(t, map[string]handler{
		"transfer.start": startOK(),
		"events.subscribe": func(_ map[string]any, notify func(any)) (any, *rpcFail) {
			notify(map[string]any{"kind": "transfer.updated", "transfer": xfer(map[string]any{"state": "running", "rev": 2, "bytes_done": 400}),
				"changed_files": []any{map[string]any{"rel": "a", "state": "running"}}})
			notify(map[string]any{"kind": "transfer.updated", "transfer": xfer(map[string]any{"transfer_id": "t-other", "rev": 99})})
			return map[string]any{"subscribed": true}, nil
		},
		"transfer.get": func(map[string]any, func(any)) (any, *rpcFail) {
			mu.Lock()
			defer mu.Unlock()
			n++
			if n == 1 {
				return xfer(map[string]any{"state": "queued", "rev": 1, "bytes_done": 0}), nil
			}
			return xfer(map[string]any{"rev": 3}), nil
		}})
	r := runCLI(t, false, "", "files", "push", "/tmp/build", `pc1:C:\x`, "--jsonl")
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if r.code != 0 || len(lines) < 2 {
		t.Fatalf("code %d stdout %q stderr %q", r.code, r.stdout, r.err)
	}
	var revs []float64
	for _, l := range lines {
		var m map[string]any
		if json.Unmarshal([]byte(l), &m) != nil || m["kind"] != "transfer.updated" {
			t.Fatalf("line %q", l)
		}
		tr := m["transfer"].(map[string]any)
		if tr["transfer_id"] != "t-1" {
			t.Fatalf("foreign transfer in %q", l)
		}
		revs = append(revs, tr["rev"].(float64))
	}
	for i := 1; i < len(revs); i++ {
		if revs[i] <= revs[i-1] {
			t.Fatalf("revs not increasing: %v", revs)
		}
	}
	if revs[len(revs)-1] != 3 {
		t.Fatalf("last rev %v", revs)
	}
}

func TestDetachPrintsTransferIDWithoutFollowing(t *testing.T) {
	f := startFake(t, map[string]handler{"transfer.start": startOK()})
	r := runCLI(t, false, "", "files", "push", "/tmp/build", `pc1:C:\x`, "--detach")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "t-1" || len(f.called("transfer.get")) != 0 {
		t.Fatalf("code %d stdout %q", r.code, r.stdout)
	}
}

func TestPushRejectsBadConflictPolicyAndRemoteFirstArgument(t *testing.T) {
	for _, args := range [][]string{
		{"files", "push", "/tmp/a", "pc1:/x", "--on-conflict", "merge"},
		{"files", "push", "pc1:/a", "pc1:/x"},
		{"files", "pull", "pc1:/a", "pc1:/b"},
		{"files", "push", "/tmp/a"},
	} {
		if r := runCLI(t, false, "", args...); r.code != exitUsage {
			t.Errorf("%v: exit %d stderr %q", args, r.code, r.err)
		}
	}
}

func TestTransferInterruptedKeepsTransferRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startFake(t, map[string]handler{"transfer.start": startOK(), "transfer.get": func(map[string]any, func(any)) (any, *rpcFail) {
		cancel()
		return xfer(map[string]any{"state": "running"}), nil
	}})
	r := runCtx(t, ctx, "files", "push", "/tmp/build", `pc1:C:\x`)
	if r.code != exitUnknown || !strings.Contains(r.err, "transfer watch t-1") {
		t.Fatalf("code %d stderr %q", r.code, r.err)
	}
}

func TestTransferResumeFollowsAndMapsParams(t *testing.T) {
	f := startFake(t, map[string]handler{"transfer.resume": ok(xfer(map[string]any{"state": "running"})), "transfer.get": ok(xfer(nil))})
	r := runCLI(t, false, "", "transfer", "resume", "t-1")
	if r.code != 0 || f.called("transfer.resume")[0]["transfer_id"] != "t-1" || len(f.called("transfer.get")) == 0 {
		t.Fatalf("code %d stdout %q stderr %q", r.code, r.stdout, r.err)
	}
	r = runCLI(t, false, "", "transfer", "resume", "t-1", "--detach")
	if r.code != 0 || !strings.Contains(r.stdout, "다시 시작했습니다") {
		t.Fatalf("detach: code %d stdout %q", r.code, r.stdout)
	}
}

func TestTransferWatchOnPausedTransferExitsOneWithResumeHint(t *testing.T) {
	startFake(t, map[string]handler{"transfer.get": ok(xfer(map[string]any{"state": "paused"}))})
	r := runCLI(t, false, "", "transfer", "watch", "t-1")
	if r.code != 1 || !strings.Contains(r.err, "transfer resume t-1") {
		t.Fatalf("code %d stderr %q", r.code, r.err)
	}
}

func TestTransferPauseCancelListGet(t *testing.T) {
	f := startFake(t, map[string]handler{
		"transfer.pause":  ok(xfer(map[string]any{"state": "paused"})),
		"transfer.cancel": ok(xfer(map[string]any{"state": "cancelled", "files_done": 1})),
		"transfer.list":   ok(map[string]any{"transfers": []any{xfer(nil)}}),
		"transfer.get":    ok(xfer(map[string]any{"files": []any{map[string]any{"rel": "a.txt", "size": 10, "state": "done", "outcome": "written"}}})),
	})
	if r := runCLI(t, false, "", "transfer", "pause", "t-1"); r.code != 0 || !strings.Contains(r.stdout, "일시 정지") {
		t.Errorf("pause: %d %q %q", r.code, r.stdout, r.err)
	}
	if r := runCLI(t, false, "", "transfer", "cancel", "t-1"); r.code != 0 || !strings.Contains(r.stdout, "취소했습니다") || !strings.Contains(r.stdout, "1개") {
		t.Errorf("cancel: %d %q %q", r.code, r.stdout, r.err)
	}
	if r := runCLI(t, false, "", "transfer", "list", "--host", "pc1"); r.code != 0 || !strings.Contains(r.stdout, "t-1") || f.called("transfer.list")[0]["host_id"] != "pc1" {
		t.Errorf("list: %d %q", r.code, r.stdout)
	}
	if r := runCLI(t, false, "", "transfer", "get", "t-1"); r.code != 0 || !strings.Contains(r.stdout, "a.txt") {
		t.Errorf("get: %d %q", r.code, r.stdout)
	}
	if r := runCLI(t, false, "", "transfer", "bogus"); r.code != exitUsage {
		t.Errorf("bogus: %d", r.code)
	}
}

func TestFilesRmSucceedsOnlyWhenAgentReportsTrashed(t *testing.T) {
	startFake(t, map[string]handler{"files.trash": ok(envelope("succeeded", map[string]any{"trashed": false}))})
	r := runCLI(t, false, "", "files", "rm", `pc1:C:\gone`)
	if r.code != exitFailure || strings.Contains(r.stdout, "휴지통으로 보냈습니다") || !strings.Contains(r.err, "아무것도 지우지 않았습니다") {
		t.Fatalf("code %d stdout %q stderr %q", r.code, r.stdout, r.err)
	}
	startFake(t, map[string]handler{"files.trash": ok(envelope("failed", map[string]any{"trashed": false,
		"error": map[string]any{"code": "CAPABILITY_UNSUPPORTED", "message": "휴지통이 이 항목을 받지 않습니다"}}))})
	r = runCLI(t, false, "", "files", "trash", `pc1:C:\net`)
	if r.code != exitFailure || !strings.Contains(r.err, "휴지통이 이 항목을 받지 않습니다") {
		t.Fatalf("refused: code %d stderr %q", r.code, r.err)
	}
}

func TestFilesRmPermanentNeverImpliedByTrash(t *testing.T) {
	f := startFake(t, map[string]handler{"files.trash": ok(envelope("succeeded", map[string]any{"trashed": true})), "files.delete": ok(envelope("succeeded", map[string]any{"deleted": true}))})
	if r := runCLI(t, false, "", "files", "trash", "--permanent", "pc1:/x"); r.code != exitUsage {
		t.Errorf("trash --permanent: exit %d", r.code)
	}
	if r := runCLI(t, false, "", "files", "trash", "pc1:/x"); r.code != 0 || len(f.called("files.delete")) != 0 || len(f.called("files.trash")) != 1 {
		t.Errorf("trash: %d", r.code)
	}
}

func TestFilesLsReadsAllPagesByDefault(t *testing.T) {
	f := startFake(t, map[string]handler{"files.list": func(p map[string]any, _ func(any)) (any, *rpcFail) {
		if p["cursor"] == nil {
			return map[string]any{"entries": []any{map[string]any{"name": "a", "dir": true, "size": 0, "mtime": "t"}}, "next_cursor": "c1", "total": 2}, nil
		}
		return map[string]any{"entries": []any{map[string]any{"name": "b.txt", "size": 5, "mtime": "t"}}, "total": 2}, nil
	}})
	r := runCLI(t, false, "", "files", "ls", `pc1:C:\x`)
	if r.code != 0 || !strings.Contains(r.stdout, "a/") || !strings.Contains(r.stdout, "b.txt") || len(f.called("files.list")) != 2 {
		t.Fatalf("code %d stdout %q calls %v", r.code, r.stdout, f.called("files.list"))
	}
	if f.called("files.list")[1]["cursor"] != "c1" {
		t.Fatalf("second call = %v", f.called("files.list")[1])
	}
	r = runCLI(t, false, "", "--json", "files", "ls", `pc1:C:\x`)
	var m map[string]any
	if json.Unmarshal([]byte(r.stdout), &m) != nil || len(m["entries"].([]any)) != 2 || m["next_cursor"] != nil {
		t.Fatalf("json stdout %q", r.stdout)
	}
}

func TestFilesLsLimitStopsAfterLimit(t *testing.T) {
	f := startFake(t, map[string]handler{"files.list": ok(map[string]any{"entries": []any{map[string]any{"name": "a", "size": 1, "mtime": "t"}, map[string]any{"name": "b", "size": 1, "mtime": "t"}},
		"next_cursor": "more", "total": 9})})
	r := runCLI(t, false, "", "files", "ls", "--limit", "2", `pc1:C:\x`)
	if r.code != 0 || len(f.called("files.list")) != 1 || f.called("files.list")[0]["limit"] != float64(2) || !strings.Contains(r.err, "9개 중 2개") {
		t.Fatalf("code %d calls %v stderr %q", r.code, f.called("files.list"), r.err)
	}
}

func TestFilesCpAndHashMapping(t *testing.T) {
	f := startFake(t, map[string]handler{
		"files.copy": ok(envelope("succeeded", map[string]any{"path": `C:\b`, "copied": 3, "outcome": "written"})),
		"files.hash": ok(envelope("succeeded", map[string]any{"sha256": strings.Repeat("ab", 32), "offset": 0, "length": 5, "size": 5})),
	})
	r := runCLI(t, false, "", "files", "cp", "-r", "--on-conflict", "rename", `pc1:C:\a`, `pc1:C:\b`)
	p := f.called("files.copy")[0]
	if r.code != 0 || p["from"] != `C:\a` || p["to"] != `C:\b` || p["on_conflict"] != "rename" || p["recursive"] != true || !strings.Contains(r.stdout, "파일 3개") {
		t.Fatalf("cp: code %d params %v stdout %q", r.code, p, r.stdout)
	}
	if r := runCLI(t, false, "", "files", "cp", `pc1:C:\a`, `pc2:C:\b`); r.code != exitUsage {
		t.Errorf("cross host cp exit %d", r.code)
	}
	r = runCLI(t, false, "", "files", "hash", "--offset", "4", `pc1:C:\a`)
	if r.code != 0 || !strings.HasPrefix(r.stdout, strings.Repeat("ab", 32)+"  ") || f.called("files.hash")[0]["offset"] != float64(4) {
		t.Fatalf("hash: code %d stdout %q params %v", r.code, r.stdout, f.called("files.hash"))
	}
}

func TestPushRelativeLocalPathBecomesAbsolute(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	f := startFake(t, map[string]handler{"transfer.start": startOK(), "transfer.get": ok(xfer(nil))})
	runCLI(t, false, "", "files", "push", "./build/", `pc1:C:\x`)
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(filepath.Dir(f.called("transfer.start")[0]["local"].(string)))
	if got != want {
		t.Fatalf("local = %v, want under %s", f.called("transfer.start")[0]["local"], want)
	}
}
