package mcpserver

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type qaRunInput struct {
	Host         string            `json:"host,omitempty" jsonschema:"Host: host_id or name from hosts_list. Omit it to use the scenario's host key."`
	ScenarioPath string            `json:"scenario_path,omitempty" jsonschema:"Absolute path of a scenario YAML file on this Mac, for example /Users/me/qa/smoke.yaml. Relative push sources start from its folder. Exactly one of scenario_path or scenario is required."`
	Scenario     string            `json:"scenario,omitempty" jsonschema:"Scenario YAML text (at most 1 MiB), for a saved scenario you hold or a short check during free exploration. Exactly one of scenario_path or scenario is required."`
	BaseDir      string            `json:"base_dir,omitempty" jsonschema:"With scenario: absolute folder on this Mac that relative push sources start from."`
	Vars         map[string]string `json:"vars,omitempty" jsonschema:"Values for the scenario's ${name} placeholders; they replace the scenario's vars. Values of names starting with secret_ are masked as *** in every record and report."`
	TimeoutMS    int64             `json:"timeout_ms,omitempty" jsonschema:"Limit for setup and steps together in milliseconds; overrides the scenario's timeout_ms. When it ends, the running step fails with TIMEOUT, the rest are not run and the teardown still runs."`
}

type qaRunIDInput struct {
	RunID string `json:"run_id" jsonschema:"run_id from qa_run or qa_list."`
}

type qaListInput struct {
	Host   string `json:"host,omitempty" jsonschema:"Only the runs of this host: host_id or name."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Runs per page, 1 to 500; default 50."`
	Cursor string `json:"cursor,omitempty" jsonschema:"next_cursor of the previous page."`
}

const qaRunDoc = "Run a QA scenario on the PC through the daemon. The daemon validates the YAML first (unknown keys, bad step kinds and undefined ${var} are refused as INVALID_ARGUMENT with file:line), then returns at once with run_id, job_id and the run with every step pending; the run goes on in the background even if this session ends, so call qa_get with run_id until finished_at is set (next_action qa_get). " +
	"Scenario keys: name, host?, vars?, timeout_ms?, setup?, steps, teardown?. Step kinds: push {from, to} (Mac to PC), launch, wait_window, click, type, key, ui_act {action, name|role|automation_id…}, assert_ui {<selector>, exists?, text?, text_contains?, text_matches?, enabled?, count?}, assert_screen_text (UI Automation text of the window), assert_file {path, exists?, contains?, matches?, sha256?, size?}, exec, screenshot, collect {file|events|crashes}, sleep, close; each step may have name, timeout_ms, continue_on_fail and optional. Full contract: docs/local-api.md §10. " +
	"Each step ends pass, fail (an expectation was not met, the PC refused, or it timed out), blocked (it could not run: CONTROL_HELD_BY_HUMAN when a person holds control — the run never takes control, CAPABILITY_UNSUPPORTED when this PC or daemon cannot do it, OFFLINE, SESSION_LOCKED, or an unknown outcome) or not_run (skipped after an earlier fail or blocked step, a cancel or the run timeout). The teardown always runs. The run ends passed, failed, blocked, cancelled or error. " +
	"Every step records assertions [{kind, expected, actual, passed}] and evidence files (a screenshot on every fail when the PC can capture, a UI snapshot on a failed UI assertion, command output, collected logs), and the finished run has a report bundle: report.json, report.md, report.html and manifest.json with sha256 of every file, at run.report. " +
	"Click, type and key steps use the same AI control lease as the input tools; a person holding control blocks these steps. One run at a time per PC: a second qa_run fails with IN_PROGRESS."

func (s *Server) registerQA(srv *mcp.Server) {
	add[qaRunInput](s, srv, "qa_run", qaRunDoc, spec{Method: "qa.run", Mutating: true, Timeout: func(map[string]any) time.Duration { return callTimeout }},
		func(sc *jsonschema.Schema) { exactlyOne(sc, "scenario_path", "scenario") })
	add[qaRunIDInput](s, srv, "qa_get", "Get a QA run by run_id with every step: status, message, error, assertions (kind, expected, actual, passed), result, duration and evidence (artifact files with path and sha256), plus the run's counts, unsupported step kinds and, once finished_at is set, report (paths of report.html, report.md, report.json and manifest.json). While status is running call it again; the run is owned by the daemon.",
		spec{Method: "qa.get"}, nil)
	add[qaListInput](s, srv, "qa_list", "List QA runs newest first without their steps: run_id, host_id, name, status, step counts, started_at, finished_at, report_dir. Pass next_cursor back as cursor for the next page.",
		spec{Method: "qa.list"}, func(sc *jsonschema.Schema) {
			one, max := 1.0, 500.0
			sc.Properties["limit"].Minimum, sc.Properties["limit"].Maximum = &one, &max
		})
	add[qaRunIDInput](s, srv, "qa_cancel", "Cancel a QA run by run_id: the running setup or steps step stops (blocked), the remaining ones are not run, then the teardown runs and the run ends cancelled. The first cancel never stops the teardown; calling qa_cancel again while the teardown runs stops the teardown too. Returns at once; poll qa_get until finished_at is set. A run that already ended otherwise is refused.",
		spec{Method: "qa.cancel", Mutating: true}, nil)
}

// qaText summarises a QA run.
func qaText(m map[string]any) string {
	var b strings.Builder
	n := func(k string) int64 { v, _ := num(m, k); return v }
	fmt.Fprintf(&b, "run %v %q status=%v: %d pass, %d fail, %d blocked, %d not run of %d steps", m["run_id"], str(m, "name"), m["status"],
		n("steps_passed"), n("steps_failed"), n("steps_blocked"), n("steps_not_run"), n("steps_total"))
	steps, _ := m["steps"].([]any)
	shown := 0
	for _, s := range steps {
		sm, _ := s.(map[string]any)
		st := str(sm, "status")
		if st != "fail" && st != "blocked" {
			continue
		}
		if shown == 10 {
			b.WriteString("\n... more steps in the structured result")
			break
		}
		fmt.Fprintf(&b, "\n%v %v %v", sm["index"], sm["action"], st)
		if msg := str(sm, "message"); msg != "" {
			b.WriteString(": " + clip(msg))
		}
		shown++
	}
	if us, _ := m["unsupported"].([]any); len(us) > 0 {
		var parts []string
		for _, u := range us {
			um, _ := u.(map[string]any)
			parts = append(parts, str(um, "action"))
		}
		fmt.Fprintf(&b, "\nnot possible on this PC or daemon: %s", strings.Join(parts, ", "))
	}
	if rep, ok := m["report"].(map[string]any); ok {
		fmt.Fprintf(&b, "\nreport: %v (machine-readable %v)", rep["html"], rep["json"])
	} else if str(m, "finished_at") == "" {
		b.WriteString("\nstill running: call qa_get again")
	}
	return b.String()
}
