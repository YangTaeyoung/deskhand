package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func qaRun(status string, finished bool) map[string]any {
	run := map[string]any{"run_id": "q-1", "job_id": "j-1", "host_id": "win", "name": "smoke", "status": status, "rev": 3,
		"steps_total": 3, "steps_passed": 1, "steps_failed": 1, "steps_blocked": 0, "steps_not_run": 1, "unsupported": []any{},
		"steps": []any{
			map[string]any{"index": 1, "action": "exec", "name": "exec hostname", "status": "pass"},
			map[string]any{"index": 2, "action": "assert_ui", "name": "assert_ui Transcript", "status": "fail", "message": "expected [done]"},
			map[string]any{"index": 3, "action": "exec", "name": "exec next", "status": "not_run"},
		}}
	if finished {
		run["finished_at"] = "2026-10-21T01:00:00.000Z"
		run["report"] = map[string]any{"html": "/tmp/qa/report.html", "json": "/tmp/qa/report.json"}
	}
	return run
}

func TestQAToolsMapToTheQAMethods(t *testing.T) {
	f := startFakeAPI(t, map[string]handler{
		"qa.run": func(map[string]any) (any, *rpcFail) {
			return envelope(map[string]any{"status": "running", "run_id": "q-1", "job_id": "j-1", "run": qaRun("running", false)}), nil
		},
		"qa.get":  func(map[string]any) (any, *rpcFail) { return qaRun("failed", true), nil },
		"qa.list": func(map[string]any) (any, *rpcFail) { return map[string]any{"runs": []any{qaRun("failed", true)}}, nil },
		"qa.cancel": func(map[string]any) (any, *rpcFail) {
			return envelope(map[string]any{"status": "running", "run_id": "q-1", "job_id": "j-1", "run": qaRun("running", false)}), nil
		},
	})
	cs := connect(t)
	cases := []struct {
		tool   string
		args   map[string]any
		method string
		want   map[string]any
	}{
		{"qa_run", map[string]any{"host": "win", "scenario_path": "/Users/me/smoke.yaml", "vars": map[string]any{"build": "1.2"}, "timeout_ms": 60000}, "qa.run",
			map[string]any{"host_id": "win", "scenario_path": "/Users/me/smoke.yaml", "timeout_ms": float64(60000), "vars": map[string]any{"build": "1.2"}}},
		{"qa_run", map[string]any{"scenario": "name: x\nsteps:\n  - sleep: 1\n", "base_dir": "/Users/me"}, "qa.run",
			map[string]any{"base_dir": "/Users/me", "scenario": "name: x\nsteps:\n  - sleep: 1\n"}},
		{"qa_get", map[string]any{"run_id": "q-1"}, "qa.get", map[string]any{"run_id": "q-1"}},
		{"qa_list", map[string]any{"host": "win", "limit": 5}, "qa.list", map[string]any{"host_id": "win", "limit": float64(5)}},
		{"qa_cancel", map[string]any{"run_id": "q-1"}, "qa.cancel", map[string]any{"run_id": "q-1"}},
	}
	for _, c := range cases {
		res := callTool(t, cs, c.tool, c.args)
		if res.IsError {
			t.Errorf("%s: %s", c.tool, text(res))
			continue
		}
		got := f.called(c.method)
		gj, _ := json.Marshal(got[len(got)-1])
		wj, _ := json.Marshal(c.want)
		if string(gj) != string(wj) {
			t.Errorf("%s params = %s, want %s", c.tool, gj, wj)
		}
	}

	run := callTool(t, cs, "qa_run", map[string]any{"scenario_path": "/Users/me/smoke.yaml"})
	sc := run.StructuredContent.(map[string]any)
	if run.IsError || sc["next_action"] != "qa_get" || sc["run_id"] != "q-1" || !strings.Contains(text(run), "qa_get") {
		t.Fatalf("qa_run %v %s", sc, text(run))
	}
	got := callTool(t, cs, "qa_get", map[string]any{"run_id": "q-1"})
	gs := got.StructuredContent.(map[string]any)
	if got.IsError || gs["next_action"] != nil {
		t.Fatalf("qa_get of a finished run %v", gs)
	}
	for _, want := range []string{"status=failed", "1 pass, 1 fail, 0 blocked, 1 not run of 3 steps", "2 assert_ui fail: expected [done]", "/tmp/qa/report.html"} {
		if !strings.Contains(text(got), want) {
			t.Errorf("qa_get summary lacks %q: %s", want, text(got))
		}
	}
	if c := callTool(t, cs, "qa_cancel", map[string]any{"run_id": "q-1"}); c.IsError || !strings.Contains(text(c), "teardown") {
		t.Errorf("qa_cancel %s", text(c))
	}
}

func TestQARunToolRefusesBothOrNeitherScenario(t *testing.T) {
	f := startFakeAPI(t, map[string]handler{})
	cs := connect(t)
	for _, args := range []map[string]any{{"host": "win"}, {"scenario_path": "/a.yaml", "scenario": "name: x"}} {
		if res := callTool(t, cs, "qa_run", args); !res.IsError {
			t.Errorf("qa_run %v was sent", args)
		}
	}
	if len(f.called("qa.run")) != 0 {
		t.Error("a bad call reached the daemon")
	}
}

func TestQAToolDescriptionsExplainTheRun(t *testing.T) {
	cs := connect(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	desc := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		desc[tool.Name] = tool
	}
	run := desc["qa_run"].Description
	for _, want := range []string{"qa_get", "pass", "fail", "blocked", "not_run", "CONTROL_HELD_BY_HUMAN", "report", "scenario"} {
		if !strings.Contains(run, want) {
			t.Errorf("qa_run description lacks %q", want)
		}
	}
	for _, name := range []string{"qa_get", "qa_list", "qa_cancel"} {
		if desc[name] == nil || !strings.Contains(desc[name].Description, "run_id") {
			t.Errorf("%s description does not name run_id", name)
		}
	}
}
