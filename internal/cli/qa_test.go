package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func qaStep(i int, phase, action, status, msg string) map[string]any {
	s := map[string]any{"index": i, "phase": phase, "name": action + " 단계", "action": action, "status": status, "assertions": []any{}, "evidence": []any{}}
	if status != "pending" && status != "not_run" {
		s["duration_ms"] = 1200
	}
	if msg != "" {
		s["message"] = msg
	}
	return s
}

func qaRunObj(status string, finished bool, steps ...map[string]any) map[string]any {
	run := map[string]any{"run_id": "q-1", "job_id": "j-9", "host_id": "pc", "name": "스모크", "status": status, "rev": 1,
		"started_at": "2026-10-21T01:00:00.000Z", "steps_total": len(steps), "unsupported": []any{}, "cancel_requested": false}
	counts := map[string]int{}
	var list []any
	for _, s := range steps {
		counts[s["status"].(string)]++
		list = append(list, s)
	}
	run["steps"] = list
	run["steps_passed"], run["steps_failed"], run["steps_blocked"], run["steps_not_run"] = counts["pass"], counts["fail"], counts["blocked"], counts["not_run"]
	if finished {
		run["finished_at"] = "2026-10-21T01:00:09.000Z"
		run["rev"] = 9
		run["report_dir"] = "/tmp/qa"
		run["report"] = map[string]any{"json": "/tmp/qa/report.json", "md": "/tmp/qa/report.md", "html": "/tmp/qa/report.html", "manifest": "/tmp/qa/manifest.json"}
	}
	return run
}

// qaFake answers qa.run with a running run and qa.get first with the run
// half done, then with final.
func qaFake(final map[string]any, extra map[string]handler) map[string]handler {
	var mu sync.Mutex
	gets := 0
	h := map[string]handler{
		"qa.run": ok(envelope("running", map[string]any{"run_id": "q-1", "job_id": "j-9",
			"run": qaRunObj("running", false, qaStep(1, "setup", "push", "pending", ""), qaStep(2, "steps", "assert_ui", "pending", ""), qaStep(3, "teardown", "close", "pending", ""))})),
		"qa.get": func(map[string]any, func(any)) (any, *rpcFail) {
			mu.Lock()
			defer mu.Unlock()
			gets++
			if gets == 1 {
				r := qaRunObj("running", false, qaStep(1, "setup", "push", "pass", ""), qaStep(2, "steps", "assert_ui", "running", ""), qaStep(3, "teardown", "close", "pending", ""))
				r["rev"] = 4
				return r, nil
			}
			return final, nil
		},
	}
	for k, v := range extra {
		h[k] = v
	}
	return h
}

func TestQARunPrintsStepsAndExitsByStatus(t *testing.T) {
	dir := t.TempDir()
	scenario := filepath.Join(dir, "smoke.yaml")
	if err := os.WriteFile(scenario, []byte("name: x\nsteps:\n  - sleep: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	varFile := filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(varFile, []byte("# 비밀\nsecret_pw=hunter2\nuser = file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	failed := qaRunObj("failed", true, qaStep(1, "setup", "push", "pass", ""),
		qaStep(2, "steps", "assert_ui", "fail", `기대와 다릅니다: text_contains 기대 "[done]", 실제 ""`), qaStep(3, "teardown", "close", "pass", ""))
	f := startFake(t, qaFake(failed, nil))
	res := runCLI(t, false, "", "qa", "run", scenario, "--host", "pc", "--var", "user=cli", "--var-file", varFile, "--timeout", "2m")
	if res.code != exitFailure {
		t.Fatalf("exit %d\n%s\n%s", res.code, res.stdout, res.err)
	}
	for _, want := range []string{"QA 실행 q-1: 스모크", "[통과] 1/3 준비 · push 단계 (push, 1.2초)", `[실패] 2/3 단계 · assert_ui 단계 (assert_ui, 1.2초) — 기대와 다릅니다`,
		"[통과] 3/3 정리 · close 단계", "결과: 실패 — 통과 2 · 실패 1 · 차단 0 · 실행 안 함 0, 전체 3단계", "보고서: /tmp/qa/report.html"} {
		if !strings.Contains(res.stdout, want) {
			t.Fatalf("stdout lacks %q:\n%s", want, res.stdout)
		}
	}
	if strings.Count(res.stdout, "1/3") != 1 {
		t.Fatalf("step 1 printed more than once:\n%s", res.stdout)
	}
	call := f.called("qa.run")[0]
	vars := call["vars"].(map[string]any)
	if call["scenario_path"] != scenario || call["host_id"] != "pc" || vars["user"] != "cli" || vars["secret_pw"] != "hunter2" || call["timeout_ms"] != float64(120000) {
		t.Fatalf("qa.run params %v", call)
	}

	for status, want := range map[string]int{"passed": exitOK, "blocked": exitNeedsUser, "error": exitUnknown, "cancelled": exitFailure} {
		startFake(t, qaFake(qaRunObj(status, true, qaStep(1, "steps", "exec", "pass", "")), nil))
		if res := runCLI(t, false, "", "qa", "run", scenario); res.code != want {
			t.Fatalf("%s: exit %d, want %d\n%s", status, res.code, want, res.stdout)
		}
	}

	startFake(t, qaFake(failed, nil))
	res = runCLI(t, false, "", "--json", "qa", "run", scenario)
	if res.code != exitFailure || !strings.HasPrefix(res.stdout, "{") || strings.Count(res.stdout, "\n") != 1 ||
		!strings.Contains(res.stdout, `"schema_version":1`) || !strings.Contains(res.stdout, `"status":"failed"`) {
		t.Fatalf("--json %d %q", res.code, res.stdout)
	}

	f = startFake(t, qaFake(failed, nil))
	res = runCLI(t, false, "", "qa", "run", scenario, "--detach")
	if res.code != exitOK || strings.TrimSpace(res.stdout) != "q-1" || len(f.called("qa.get")) != 0 {
		t.Fatalf("--detach %d %q %q", res.code, res.stdout, res.err)
	}

	startFake(t, map[string]handler{"qa.run": func(map[string]any, func(any)) (any, *rpcFail) {
		return nil, &rpcFail{code: "INVALID_ARGUMENT", msg: scenario + ":3: 단계에 모르는 키 \"slep\"가 있습니다"}
	}})
	res = runCLI(t, false, "", "qa", "run", scenario)
	if res.code != exitFailure || !strings.Contains(res.err, scenario+":3:") {
		t.Fatalf("bad scenario %d %q", res.code, res.err)
	}
	if res := runCLI(t, false, "", "qa", "run"); res.code != exitUsage {
		t.Fatalf("no scenario: exit %d", res.code)
	}
	if res := runCLI(t, false, "", "qa", "run", scenario, "--var", "novalue"); res.code != exitUsage {
		t.Fatalf("bad --var: exit %d", res.code)
	}
}

func TestQARunCtrlCCancelsTheRun(t *testing.T) {
	dir := t.TempDir()
	scenario := filepath.Join(dir, "smoke.yaml")
	if err := os.WriteFile(scenario, []byte("name: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	cancelled := false
	running := qaRunObj("running", false, qaStep(1, "steps", "sleep", "running", ""))
	done := qaRunObj("cancelled", true, qaStep(1, "steps", "sleep", "blocked", "취소로 멈췄습니다"))
	f := startFake(t, map[string]handler{
		"qa.run": ok(envelope("running", map[string]any{"run_id": "q-1", "job_id": "j-9", "run": running})),
		"qa.get": func(map[string]any, func(any)) (any, *rpcFail) {
			mu.Lock()
			defer mu.Unlock()
			if !cancelled {
				cancel()
				return running, nil
			}
			return done, nil
		},
		"qa.cancel": func(map[string]any, func(any)) (any, *rpcFail) {
			mu.Lock()
			cancelled = true
			mu.Unlock()
			return envelope("running", map[string]any{"run_id": "q-1", "run": running}), nil
		},
	})
	res := runCtx(t, ctx, "qa", "run", scenario)
	if res.code != exitFailure || len(f.called("qa.cancel")) != 1 || !strings.Contains(res.err, "취소를 요청했습니다") ||
		!strings.Contains(res.stdout, "결과: 취소됨") {
		t.Fatalf("Ctrl-C %d\n%s\n%s", res.code, res.stdout, res.err)
	}
}

func TestQAListGetReportCancel(t *testing.T) {
	failedStep := qaStep(2, "steps", "assert_ui", "fail", "기대와 다릅니다")
	failedStep["assertions"] = []any{map[string]any{"kind": "text_contains", "expected": "[done]", "actual": "You: hi", "passed": false},
		map[string]any{"kind": "exists", "expected": true, "actual": true, "passed": true}}
	failedStep["evidence"] = []any{map[string]any{"path": "/tmp/qa/evidence/02-assert_ui-ui-tree.json"}}
	final := qaRunObj("failed", true, qaStep(1, "steps", "exec", "pass", ""), failedStep)
	listed := qaRunObj("failed", true)
	delete(listed, "steps")
	f := startFake(t, map[string]handler{
		"qa.list":   ok(map[string]any{"runs": []any{listed}, "next_cursor": "q-0"}),
		"qa.get":    ok(final),
		"qa.cancel": ok(envelope("cancelled", map[string]any{"run_id": "q-1", "run": final})),
	})
	res := runCLI(t, false, "", "qa", "list", "--host", "pc", "--limit", "1")
	if res.code != exitOK || !strings.Contains(res.stdout, "q-1") || !strings.Contains(res.stdout, "실패") || !strings.Contains(res.stdout, "더 있습니다") {
		t.Fatalf("qa list %d\n%s", res.code, res.stdout)
	}
	if p := f.called("qa.list")[0]; p["host_id"] != "pc" || p["limit"] != float64(1) {
		t.Fatalf("qa.list params %v", p)
	}
	res = runCLI(t, false, "", "qa", "get", "q-1")
	if res.code != exitOK || !strings.Contains(res.stdout, `text_contains: 기대 "[done]", 실제 "You: hi"`) || strings.Contains(res.stdout, "exists: 기대") ||
		!strings.Contains(res.stdout, "증거: /tmp/qa/evidence/02-assert_ui-ui-tree.json") || !strings.Contains(res.stdout, "보고서: /tmp/qa/report.html") {
		t.Fatalf("qa get %d\n%s", res.code, res.stdout)
	}

	var out, errb bytes.Buffer
	app := New("test")
	app.Stdout, app.Stderr = &out, &errb
	var opened []string
	app.Open = func(p string) error { opened = append(opened, p); return nil }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if code := app.Run(ctx, []string{"qa", "report", "q-1", "--open"}); code != exitOK || len(opened) != 1 || opened[0] != "/tmp/qa/report.html" ||
		!strings.Contains(out.String(), "manifest: /tmp/qa/manifest.json") {
		t.Fatalf("qa report %d %v\n%s\n%s", code, opened, out.String(), errb.String())
	}

	res = runCLI(t, false, "", "qa", "cancel", "q-1")
	if res.code != exitOK || !strings.Contains(res.stdout, "이미 취소됐습니다") {
		t.Fatalf("qa cancel %d\n%s", res.code, res.stdout)
	}

	startFake(t, map[string]handler{"qa.get": ok(qaRunObj("running", false, qaStep(1, "steps", "exec", "running", "")))})
	if res := runCLI(t, false, "", "qa", "report", "q-1"); res.code != exitFailure || !strings.Contains(res.err, "보고서가 아직 없습니다") {
		t.Fatalf("report of a running run %d %q", res.code, res.err)
	}
}

func TestQARunSecondCtrlCCancelsTheTeardown(t *testing.T) {
	dir := t.TempDir()
	scenario := filepath.Join(dir, "smoke.yaml")
	if err := os.WriteFile(scenario, []byte("name: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	var mu sync.Mutex
	cancels := 0
	teardown := qaRunObj("running", false, qaStep(1, "steps", "sleep", "blocked", "취소로 멈췄습니다"), qaStep(2, "teardown", "exec", "running", ""))
	teardown["cancel_requested"] = true
	done := qaRunObj("cancelled", true, qaStep(1, "steps", "sleep", "blocked", "취소로 멈췄습니다"), qaStep(2, "teardown", "exec", "blocked", "취소로 멈췄습니다"))
	f := startFake(t, map[string]handler{
		"qa.run": ok(envelope("running", map[string]any{"run_id": "q-1", "job_id": "j-9", "run": teardown})),
		"qa.get": func(map[string]any, func(any)) (any, *rpcFail) {
			mu.Lock()
			defer mu.Unlock()
			switch cancels {
			case 0:
				cancel()
			case 1:
				select {
				case sigs <- os.Interrupt:
				default:
				}
			default:
				return done, nil
			}
			return teardown, nil
		},
		"qa.cancel": func(map[string]any, func(any)) (any, *rpcFail) {
			mu.Lock()
			cancels++
			mu.Unlock()
			return envelope("running", map[string]any{"run_id": "q-1", "run": teardown}), nil
		},
	})
	var out, errb bytes.Buffer
	app := New("test")
	app.Stdout, app.Stderr = &out, &errb
	app.PollInterval = time.Millisecond
	app.Signals = func() (<-chan os.Signal, func()) { return sigs, func() {} }
	code := app.Run(ctx, []string{"qa", "run", scenario})
	if code != exitFailure || len(f.called("qa.cancel")) != 2 || !strings.Contains(errb.String(), "정리 단계도 멈추도록") ||
		!strings.Contains(out.String(), "결과: 취소됨") {
		t.Fatalf("second Ctrl-C %d\n%s\n%s", code, out.String(), errb.String())
	}
}
