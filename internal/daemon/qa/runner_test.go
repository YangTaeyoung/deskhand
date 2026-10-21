package qa

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/artifacts"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func fixturePC() *fakePC {
	pc := newFakePC()
	pc.addWindow("0x10", "Deskhand Fixture", "deskhand-fixture.exe", 4242)
	pc.addElem(&fakeElem{window: "Deskhand Fixture", role: "edit", name: "Message", aid: "101"})
	pc.addElem(&fakeElem{window: "Deskhand Fixture", role: "button", name: "Send", aid: "1"})
	pc.addElem(&fakeElem{window: "Deskhand Fixture", role: "edit", name: "Transcript", aid: "103"})
	pc.addElem(&fakeElem{window: "Deskhand Fixture", role: "text", name: "한글 라벨", aid: "104"})
	pc.addElem(&fakeElem{window: "Deskhand Fixture", role: "button", name: "Same"})
	pc.addElem(&fakeElem{window: "Deskhand Fixture", role: "button", name: "Same"})
	return pc
}

func TestRunnerStatuses(t *testing.T) {
	t.Run("every step passes", func(t *testing.T) {
		e := newEnv(t, fixturePC(), testOpts{})
		run := e.run(t, `name: ok
steps:
  - exec: hostname
  - assert_ui: {name: Send}
teardown:
  - exec: cleanup
`)
		if run.Status != RunPassed || statuses(run) != "pass pass pass" || run.StepsPassed != 3 || run.StepsTotal != 3 || run.StepsNotRun != 0 {
			t.Fatalf("run %s %s %+v", run.Status, statuses(run), run)
		}
		if run.Steps[2].Phase != PhaseTeardown || run.Steps[0].Index != 1 || run.Steps[2].Index != 3 {
			t.Fatalf("steps %+v", run.Steps)
		}
		job, err := e.jr.Get(context.Background(), run.JobID)
		if err != nil || job.Kind != KindQA || job.Status != model.StatusSucceeded || job.Summary != "QA: ok" {
			t.Fatalf("job %+v %v", job, err)
		}
	})

	t.Run("a failed step leaves the later steps not run and the teardown runs", func(t *testing.T) {
		pc := fixturePC()
		pc.exec = func(p proto.Message) (proto.Message, error) {
			if p.String("cmd") == "broken" {
				return proto.Message{"code": float64(2), "stdout": "", "stderr": "nope\n"}, nil
			}
			return proto.Message{"code": float64(0), "stdout": "ok\n"}, nil
		}
		e := newEnv(t, pc, testOpts{})
		run := e.run(t, `name: fails
setup:
  - exec: prepare
steps:
  - exec: broken
  - exec: after
  - assert_ui: {name: Send}
teardown:
  - exec: broken
  - exec: cleanup
`)
		if run.Status != RunFailed || statuses(run) != "pass fail not_run not_run fail pass" {
			t.Fatalf("run %s %s", run.Status, statuses(run))
		}
		if run.StepsFailed != 2 || run.StepsNotRun != 2 || run.StepsPassed != 2 {
			t.Fatalf("counts %+v", run)
		}
		if !strings.Contains(run.Steps[2].Message, "2단계") {
			t.Fatalf("not_run message %q", run.Steps[2].Message)
		}
		a := run.Steps[1].Assertions
		if len(a) != 1 || a[0].Kind != "exit_code" || a[0].Passed || !same(a[0].Expected, 0) || !same(a[0].Actual, 2) {
			t.Fatalf("assertions %+v", a)
		}
		job, _ := e.jr.Get(context.Background(), run.JobID)
		if job.Status != model.StatusFailed {
			t.Fatalf("job %+v", job)
		}
	})

	t.Run("continue_on_fail lets the next step run", func(t *testing.T) {
		pc := fixturePC()
		e := newEnv(t, pc, testOpts{})
		run := e.run(t, `name: keep going
steps:
  - assert_ui: {name: Missing}
    timeout_ms: 50
    continue_on_fail: true
  - exec: next
`)
		if run.Status != RunFailed || statuses(run) != "fail pass" {
			t.Fatalf("run %s %s", run.Status, statuses(run))
		}
	})

	t.Run("a step the PC cannot do is blocked and later steps are not run", func(t *testing.T) {
		e := newEnv(t, fixturePC(), testOpts{})
		run := e.run(t, `name: blocked
steps:
  - screenshot:
  - exec: next
teardown:
  - exec: cleanup
`)
		if run.Status != RunBlocked || statuses(run) != "blocked not_run pass" {
			t.Fatalf("run %s %s", run.Status, statuses(run))
		}
		s := run.Steps[0]
		if s.Error == nil || s.Error.Code != wire.CodeCapabilityUnsupported || !strings.Contains(s.Message, "not implemented") {
			t.Fatalf("step %+v", s)
		}
		if len(run.Unsupported) != 1 || run.Unsupported[0].Action != "screenshot" || run.Unsupported[0].Code != wire.CodeCapabilityUnsupported {
			t.Fatalf("unsupported %+v", run.Unsupported)
		}
		if ops := strings.Join(e.pc.ops(), " "); strings.Contains(ops, "screen.capture") {
			t.Fatalf("screen.capture was sent: %s", ops)
		}
		job, _ := e.jr.Get(context.Background(), run.JobID)
		if job.Status != model.StatusFailed {
			t.Fatalf("job %+v", job)
		}
	})

	t.Run("input steps are blocked when the daemon has no input", func(t *testing.T) {
		e := newEnv(t, fixturePC(), testOpts{})
		run := e.run(t, `name: input
steps:
  - click: {element: {name: Send}}
    continue_on_fail: true
  - type: hello
    continue_on_fail: true
  - key: enter
    continue_on_fail: true
`)
		if run.Status != RunBlocked || statuses(run) != "blocked blocked blocked" {
			t.Fatalf("run %s %s", run.Status, statuses(run))
		}
		for _, s := range run.Steps {
			if s.Error == nil || s.Error.Code != wire.CodeCapabilityUnsupported {
				t.Fatalf("step %+v", s)
			}
		}
		if len(run.Unsupported) != 3 {
			t.Fatalf("unsupported %+v", run.Unsupported)
		}
	})

	t.Run("an optional step does not change the run status", func(t *testing.T) {
		e := newEnv(t, fixturePC(), testOpts{})
		run := e.run(t, `name: optional
steps:
  - screenshot:
    optional: true
  - exec: next
`)
		if run.Status != RunPassed || statuses(run) != "blocked pass" {
			t.Fatalf("run %s %s", run.Status, statuses(run))
		}
	})

	t.Run("a failed setup leaves the steps not run", func(t *testing.T) {
		pc := fixturePC()
		pc.exec = func(p proto.Message) (proto.Message, error) {
			return proto.Message{"code": float64(1)}, nil
		}
		e := newEnv(t, pc, testOpts{})
		run := e.run(t, `name: setup fails
setup:
  - exec: prepare
steps:
  - exec: never
teardown:
  - sleep: 1
`)
		if run.Status != RunFailed || statuses(run) != "fail not_run pass" {
			t.Fatalf("run %s %s", run.Status, statuses(run))
		}
	})

	t.Run("an offline PC blocks the step", func(t *testing.T) {
		pc := fixturePC()
		pc.fail["exec.run"] = apierr.New(wire.CodeOffline, "pc PC에 연결돼 있지 않습니다. deskhand host connect pc로 연결하세요", true, apierr.NextHostConnect)
		e := newEnv(t, pc, testOpts{})
		run := e.run(t, "name: offline\nsteps:\n  - exec: hostname\n")
		if run.Status != RunBlocked || run.Steps[0].Error.Code != wire.CodeOffline {
			t.Fatalf("run %s %+v", run.Status, run.Steps[0])
		}
	})

	t.Run("a step that times out fails", func(t *testing.T) {
		pc := fixturePC()
		pc.blockOp = "ui.act"
		e := newEnv(t, pc, testOpts{})
		run := e.run(t, "name: slow\nsteps:\n  - ui_act: {name: Send, action: invoke}\n    timeout_ms: 100\n")
		if run.Status != RunFailed || run.Steps[0].Status != StepFail || run.Steps[0].Error == nil || run.Steps[0].Error.Code != wire.CodeTimeout {
			t.Fatalf("run %s %+v", run.Status, run.Steps[0])
		}
	})

	t.Run("the run timeout between two steps fails the run", func(t *testing.T) {
		pc := fixturePC()
		pc.exec = func(p proto.Message) (proto.Message, error) {
			time.Sleep(150 * time.Millisecond)
			return proto.Message{"code": float64(0)}, nil
		}
		e := newEnv(t, pc, testOpts{})
		run := e.run(t, "name: late\ntimeout_ms: 50\nsteps:\n  - exec: slow\n  - exec: never\n")
		if run.Status != RunFailed || statuses(run) != "pass not_run" || run.Error == nil || run.Error.Code != wire.CodeTimeout {
			t.Fatalf("run %s %s %+v", run.Status, statuses(run), run.Error)
		}
	})

	t.Run("the run timeout does not cover the teardown", func(t *testing.T) {
		e := newEnv(t, fixturePC(), testOpts{})
		run := e.run(t, "name: td late\ntimeout_ms: 200\nsteps:\n  - sleep: 1\nteardown:\n  - sleep: 400\n")
		if run.Status != RunPassed || statuses(run) != "pass pass" || run.Error != nil {
			t.Fatalf("run %s %s %+v", run.Status, statuses(run), run.Error)
		}
	})

	t.Run("the run timeout fails the running step and skips the rest", func(t *testing.T) {
		e := newEnv(t, fixturePC(), testOpts{})
		run := e.run(t, `name: run timeout
timeout_ms: 100
steps:
  - sleep: 5000
  - exec: never
teardown:
  - exec: cleanup
`)
		if run.Status != RunFailed || statuses(run) != "fail not_run pass" || run.Error == nil || run.Error.Code != wire.CodeTimeout {
			t.Fatalf("run %s %s %+v", run.Status, statuses(run), run.Error)
		}
	})
}

func TestAssertions(t *testing.T) {
	t.Run("assert_ui waits for the element and its text", func(t *testing.T) {
		pc := fixturePC()
		pc.addElem(&fakeElem{window: "Deskhand Fixture", role: "button", name: "Later", hiddenFor: 3})
		pc.onAct = func(pc *fakePC, p proto.Message) {
			if p.String("name") == "Send" {
				for _, e := range pc.elems {
					if e.name == "Transcript" {
						e.text = "You: 안녕\r\nBot: echo 안녕\r\n[done]\r\n"
					}
				}
			}
		}
		e := newEnv(t, pc, testOpts{})
		run := e.run(t, `name: ui
steps:
  - assert_ui: {name: Later}
  - ui_act: {name: Send, action: invoke}
  - assert_ui: {name: Transcript, text_contains: "[done]", text_matches: 'Bot: echo \S+'}
  - assert_ui: {name: Same, count: 2}
  - assert_ui: {name: Send, enabled: true}
  - assert_ui: {name: Gone, exists: false}
  - assert_ui: {name: Same, index: 1, exists: true}
`)
		if run.Status != RunPassed {
			t.Fatalf("run %s %s %+v", run.Status, statuses(run), run.Steps)
		}
		kinds := func(s StepResult) string {
			var k []string
			for _, a := range s.Assertions {
				k = append(k, a.Kind)
			}
			return strings.Join(k, ",")
		}
		if k := kinds(run.Steps[2]); k != "exists,text_contains,text_matches" {
			t.Fatalf("assertions %s", k)
		}
		if k := kinds(run.Steps[3]); k != "count" {
			t.Fatalf("assertions %s", k)
		}
		if !same(run.Steps[3].Assertions[0].Actual, 2) {
			t.Fatalf("count %+v", run.Steps[3].Assertions)
		}
		act := pc.callsOf("ui.act")
		if len(act) != 1 || act[0].params.String("window") != "" || act[0].params.String("name") != "Send" || act[0].rid == "" {
			t.Fatalf("ui.act %+v", act)
		}
		if run.Steps[1].Result["request_id"] != act[0].rid {
			t.Fatalf("ui_act result %+v", run.Steps[1].Result)
		}
	})

	t.Run("a failing assert_ui keeps the last value and a UI snapshot", func(t *testing.T) {
		pc := fixturePC()
		pc.setText("Transcript", "You: hi\r\nBot: ")
		pc.setCap("screen", "ready")
		e := newEnv(t, pc, testOpts{})
		run := e.run(t, `name: ui fails
steps:
  - assert_ui: {name: Transcript, text_contains: "[done]"}
    timeout_ms: 60
`)
		s := run.Steps[0]
		if run.Status != RunFailed || s.Status != StepFail {
			t.Fatalf("run %s %+v", run.Status, s)
		}
		last := s.Assertions[len(s.Assertions)-1]
		if last.Kind != "text_contains" || last.Expected != "[done]" || last.Actual != "You: hi\r\nBot: " || last.Passed {
			t.Fatalf("assertion %+v", last)
		}
		if !strings.Contains(s.Message, "[done]") {
			t.Fatalf("message %q", s.Message)
		}
		var kinds []string
		for _, a := range s.Evidence {
			kinds = append(kinds, a.Kind+":"+a.Name)
			if _, err := os.Stat(a.Path); err != nil {
				t.Fatalf("evidence %s: %v", a.Path, err)
			}
		}
		if strings.Join(kinds, " ") != "screenshot:01-assert_ui-screenshot.png ui_tree:01-assert_ui-ui-tree.json" {
			t.Fatalf("evidence %v", kinds)
		}
	})

	t.Run("assert_screen_text reads the window's UI text", func(t *testing.T) {
		pc := fixturePC()
		pc.setText("Transcript", "Fetch: 200")
		e := newEnv(t, pc, testOpts{})
		run := e.run(t, `name: screen text
steps:
  - assert_screen_text: 한글 라벨
  - assert_screen_text: {contains: [Send, "Fetch: 200"], not_contains: 오류, matches: 'Fetch: \d+'}
  - assert_screen_text: {contains: 없는 글}
    timeout_ms: 50
`)
		if statuses(run) != "pass pass fail" {
			t.Fatalf("statuses %s %+v", statuses(run), run.Steps)
		}
		s := run.Steps[2]
		if len(s.Evidence) != 1 || s.Evidence[0].Kind != "ui_tree" {
			t.Fatalf("evidence %+v", s.Evidence)
		}
		if a := s.Assertions[0]; a.Kind != "contains" || a.Expected != "없는 글" || !strings.Contains(a.Actual.(string), "한글 라벨") {
			t.Fatalf("assertion %+v", a)
		}
		for _, c := range pc.callsOf("ui.tree") {
			if c.params.String("filter") == "interactive" && c.params.Int("max") == screenTreeMax {
				t.Fatalf("assert_screen_text read the interactive tree: %+v", c.params)
			}
		}
	})

	t.Run("assert_file checks existence, size, hash and content", func(t *testing.T) {
		pc := fixturePC()
		log := []byte("2026-10-21T10:00:00Z start pid 4242\n2026-10-21T10:00:01Z send 안녕 QA\n2026-10-21T10:00:04Z done\n")
		pc.files[`C:\qa\fixture.log`] = log
		var wide []byte
		for _, u := range utf16.Encode([]rune("\ufeff유니코드 로그\n")) {
			wide = append(wide, byte(u), byte(u>>8))
		}
		pc.files[`C:\qa\wide.log`] = wide
		pc.utf16[`C:\qa\wide.log`] = true
		sum := sha256.Sum256(log)
		e := newEnv(t, pc, testOpts{})
		run := e.run(t, `name: files
vars:
  sum: `+hex.EncodeToString(sum[:])+`
steps:
  - assert_file: {path: 'C:\qa\fixture.log', contains: [send 안녕 QA, done], not_contains: error, matches: 'start pid \d+'}
  - assert_file: {path: 'C:\qa\fixture.log', sha256: '${sum}', size: `+itoa(len(log))+`}
  - assert_file: {path: 'C:\qa\none.log', exists: false}
  - assert_file: {path: 'C:\qa\wide.log', contains: 유니코드 로그}
  - assert_file: {path: 'C:\qa\fixture.log', contains: crash}
    timeout_ms: 50
`)
		if statuses(run) != "pass pass pass pass fail" {
			t.Fatalf("statuses %s %+v", statuses(run), run.Steps)
		}
		s := run.Steps[4]
		if len(s.Evidence) != 1 || s.Evidence[0].Name != "05-assert_file-content.txt" {
			t.Fatalf("evidence %+v", s.Evidence)
		}
		data, _ := os.ReadFile(s.Evidence[0].Path)
		if string(data) != string(log) {
			t.Fatalf("content evidence %q", data)
		}
	})

	t.Run("wait_window finds the window that later steps use", func(t *testing.T) {
		pc := fixturePC()
		pc.windowsAfter = 2
		e := newEnv(t, pc, testOpts{})
		run := e.run(t, `name: windows
steps:
  - wait_window: {title: Deskhand Fixture}
  - ui_act: {name: Send, action: invoke}
  - close:
  - wait_window: {title_contains: fixture, gone: true}
`)
		if run.Status != RunPassed {
			t.Fatalf("run %s %s %+v", run.Status, statuses(run), run.Steps)
		}
		act := pc.callsOf("ui.act")
		if len(act) != 1 || act[0].params.String("window") != "0x10" {
			t.Fatalf("ui.act %+v", act)
		}
		closeCalls := pc.callsOf("win.action")
		if len(closeCalls) != 1 || closeCalls[0].params.String("window") != "0x10" || closeCalls[0].params.String("action") != "close" {
			t.Fatalf("win.action %+v", closeCalls)
		}
		if a := run.Steps[0].Assertions; len(a) != 1 || a[0].Kind != "window" || !a[0].Passed {
			t.Fatalf("assertions %+v", a)
		}
	})

	t.Run("exec checks the exit code and output and keeps them as evidence", func(t *testing.T) {
		pc := fixturePC()
		pc.exec = func(p proto.Message) (proto.Message, error) {
			return proto.Message{"code": float64(3), "stdout": "build 1.2.3\n", "stderr": "warn\n"}, nil
		}
		e := newEnv(t, pc, testOpts{})
		run := e.run(t, `name: exec
steps:
  - exec: {argv: [app.exe, --version], expect_code: 3, stdout_contains: "1.2.3", stderr_contains: warn}
    timeout_ms: 9000
`)
		s := run.Steps[0]
		if s.Status != StepPass || len(s.Assertions) != 3 || len(s.Evidence) != 2 {
			t.Fatalf("step %+v", s)
		}
		call := pc.callsOf("exec.run")[0]
		if call.params.Int("timeout_ms") != 9000 || strings.Join(call.params.Strings("argv"), " ") != "app.exe --version" {
			t.Fatalf("exec.run %+v", call.params)
		}
	})

	t.Run("push makes the folder and sends the file", func(t *testing.T) {
		pc := fixturePC()
		push := &fakePush{}
		e := newEnv(t, pc, testOpts{push: push})
		if err := os.WriteFile(filepath.Join(e.dir, "app.exe"), []byte("exe"), 0o600); err != nil {
			t.Fatal(err)
		}
		run := e.run(t, `name: push
setup:
  - push: {from: app.exe, to: 'C:\qa\${run_id}\app.exe'}
steps:
  - exec: next
`)
		if run.Status != RunPassed {
			t.Fatalf("run %s %+v", run.Status, run.Steps)
		}
		mk := pc.callsOf("fs.mkdir")
		if len(mk) != 1 || mk[0].params.String("path") != `C:\qa\`+run.RunID {
			t.Fatalf("fs.mkdir %+v", mk)
		}
		if len(push.calls) != 1 || push.calls[0].remote != `C:\qa\`+run.RunID+`\app.exe` || push.calls[0].onConflict != "replace" {
			t.Fatalf("push %+v", push.calls)
		}
		if r := run.Steps[0].Result; r["transfer_id"] != "t-1" || r["state"] != "done" {
			t.Fatalf("push result %+v", r)
		}
	})

	t.Run("collect keeps the file tail and the event log as evidence", func(t *testing.T) {
		pc := fixturePC()
		pc.files[`C:\qa\fixture.log`] = []byte("0123456789")
		e := newEnv(t, pc, testOpts{})
		run := e.run(t, `name: collect
steps:
  - collect: {file: 'C:\qa\fixture.log', max_bytes: 4}
  - collect: {events: {log: Application, levels: [error]}}
`)
		if run.Status != RunPassed {
			t.Fatalf("run %s %+v", run.Status, run.Steps)
		}
		a := run.Steps[0].Evidence[0]
		data, _ := os.ReadFile(a.Path)
		if a.Name != "01-collect-fixture.log" || string(data) != "6789" || a.Kind != "log" || run.Steps[0].Result["truncated"] != true {
			t.Fatalf("collect file %+v %q %+v", a, data, run.Steps[0].Result)
		}
		ev := pc.callsOf("diag.events")[0].params
		if ev.String("log") != "Application" || ev.String("since") == "" {
			t.Fatalf("diag.events %+v", ev)
		}
		if b := run.Steps[1].Evidence[0]; b.Name != "02-collect-events-Application.json" || b.Kind != "report" {
			t.Fatalf("collect events %+v", b)
		}
	})

	t.Run("click aims at the element center through the input", func(t *testing.T) {
		pc := fixturePC()
		pc.setCap("input", "ready")
		in := &fakeInput{}
		e := newEnv(t, pc, testOpts{input: in})
		run := e.run(t, `name: click
steps:
  - wait_window: Deskhand Fixture
  - click: {element: {name: Send}}
  - click: {x: 5, y: 7}
  - type: 안녕
  - key: enter
`)
		if run.Status != RunPassed {
			t.Fatalf("run %s %+v", run.Status, run.Steps)
		}
		if len(in.clicks) != 2 || in.clicks[0] != [2]int{150, 85} || in.clicks[1] != [2]int{105, 57} {
			t.Fatalf("clicks %v", in.clicks)
		}
		if strings.Join(in.typed, "") != "안녕" || strings.Join(in.keys, "") != "enter" {
			t.Fatalf("typed %v keys %v", in.typed, in.keys)
		}
	})
}

func itoa(n int) string { return fmt.Sprint(n) }

// same reports whether a JSON value decoded from a run equals want.
func same(v, want any) bool { return fmt.Sprint(v) == fmt.Sprint(want) }

func TestInputStepsBlockedWhileAHumanHoldsControl(t *testing.T) {
	pc := fixturePC()
	held := apierr.New(wire.CodeControlHeldByHuman, "사람이 PC를 쓰는 중입니다", false, "human_release")
	e := newEnv(t, pc, testOpts{control: fakeControl{err: held}})
	run := e.run(t, `name: human
steps:
  - assert_ui: {name: Send}
  - ui_act: {name: Send, action: invoke}
  - exec: never
teardown:
  - close:
  - exec: cleanup
`)
	if run.Status != RunBlocked || statuses(run) != "pass blocked not_run blocked pass" {
		t.Fatalf("run %s %s", run.Status, statuses(run))
	}
	s := run.Steps[1]
	if s.Error == nil || s.Error.Code != wire.CodeControlHeldByHuman || !strings.Contains(s.Message, "사람이 조작 중") {
		t.Fatalf("step %+v", s)
	}
	if ops := strings.Join(pc.ops(), " "); strings.Contains(ops, "ui.act") || strings.Contains(ops, "win.action") {
		t.Fatalf("input reached the PC: %s", ops)
	}

	t.Run("a preempted request from the agent is blocked too", func(t *testing.T) {
		pc := fixturePC()
		pc.fail["ui.act"] = apierr.New(wire.CodeControlPreempted, "a newer writer holds the input", false, "")
		e := newEnv(t, pc, testOpts{})
		run := e.run(t, "name: preempted\nsteps:\n  - ui_act: {name: Send, action: invoke}\n")
		if run.Status != RunBlocked || run.Steps[0].Error.Code != wire.CodeControlPreempted {
			t.Fatalf("run %s %+v", run.Status, run.Steps[0])
		}
	})
}

func TestReportBundleManifestHashes(t *testing.T) {
	pc := fixturePC()
	pc.setCap("screen", "ready")
	pc.files[`C:\qa\fixture.log`] = []byte("start\nsend 안녕\n")
	e := newEnv(t, pc, testOpts{})
	src := `name: 보고서 점검
vars:
  secret_token: s3cr3t-value
steps:
  - exec: {cmd: 'echo s3cr3t-value', stdout_contains: s3cr3t}
  - screenshot: 첫 화면
  - collect: {file: 'C:\qa\fixture.log'}
  - assert_ui: {name: Transcript, text_contains: "${secret_token}"}
    timeout_ms: 40
`
	pc.exec = func(p proto.Message) (proto.Message, error) {
		return proto.Message{"code": float64(0), "stdout": "token=s3cr3t-value\n"}, nil
	}
	run := e.run(t, src)
	if run.Status != RunFailed || run.ReportDir == "" || run.Report == nil {
		t.Fatalf("run %s %+v", run.Status, run)
	}
	dir := run.ReportDir
	for _, p := range []string{run.Report.JSON, run.Report.MD, run.Report.HTML, run.Report.Manifest} {
		if filepath.Dir(p) != dir {
			t.Fatalf("report file %s outside %s", p, dir)
		}
	}

	var rep struct {
		SchemaVersion int               `json:"schema_version"`
		Run           Run               `json:"run"`
		Scenario      map[string]any    `json:"scenario"`
		Vars          map[string]string `json:"vars"`
		Host          map[string]any    `json:"host"`
		Environment   map[string]any    `json:"environment"`
		Daemon        string            `json:"daemon_version"`
	}
	data, err := os.ReadFile(run.Report.JSON)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatal(err)
	}
	srcSum := sha256.Sum256([]byte(src))
	if rep.SchemaVersion != 1 || len(rep.Run.Steps) != 4 || rep.Run.Status != RunFailed || rep.Run.Rev != run.Rev || rep.Scenario["sha256"] != hex.EncodeToString(srcSum[:]) ||
		rep.Scenario["source"] != strings.ReplaceAll(src, "s3cr3t-value", "***") || rep.Vars["secret_token"] != "***" || rep.Host["host_id"] != "pc" || rep.Daemon != "test" ||
		rep.Environment["computer_name"] != "DEV-PC" {
		t.Fatalf("report.json %+v", rep)
	}
	for _, f := range []string{run.Report.JSON, run.Report.MD, run.Report.HTML} {
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), "s3cr3t-value") {
			t.Fatalf("%s holds the secret value", f)
		}
	}
	walkSecret := func(path string) {
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), "s3cr3t-value") {
			t.Fatalf("evidence %s holds the secret value", path)
		}
	}

	md, _ := os.ReadFile(run.Report.MD)
	for _, want := range []string{"# QA 결과: 보고서 점검", "실패", "통과", "evidence/02-screenshot-", "기대"} {
		if !strings.Contains(string(md), want) {
			t.Fatalf("report.md lacks %q:\n%s", want, md)
		}
	}
	html, _ := os.ReadFile(run.Report.HTML)
	h := string(html)
	if !strings.Contains(h, "<style>") || strings.Contains(h, "<script") || regexp.MustCompile(`(src|href)="(https?:)?//`).MatchString(h) ||
		!strings.Contains(h, `src="evidence/02-screenshot-`) || !strings.Contains(h, `lang="ko"`) || !strings.Contains(h, "QA 결과") {
		t.Fatalf("report.html is not a self-contained Korean page:\n%s", h)
	}

	m, ok, err := artifacts.ReadManifest(dir)
	if err != nil || !ok {
		t.Fatalf("manifest %v %v", ok, err)
	}
	if m.SchemaVersion != 1 || m.RunID != run.RunID || m.JobID != run.JobID {
		t.Fatalf("manifest %+v", m)
	}
	listed := map[string]artifacts.ManifestFile{}
	for _, f := range m.Files {
		listed[f.Path] = f
	}
	var onDisk []string
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == "manifest.json" {
			return nil
		}
		onDisk = append(onDisk, rel)
		walkSecret(p)
		f, ok := listed[rel]
		if !ok {
			t.Errorf("%s is not in the manifest", rel)
			return nil
		}
		b, _ := os.ReadFile(p)
		sum := sha256.Sum256(b)
		if f.SHA256 != hex.EncodeToString(sum[:]) || f.Size != int64(len(b)) || f.ArtifactID == "" || f.MIME == "" || f.Kind == "" {
			t.Errorf("manifest entry %+v does not match %s", f, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(onDisk) != len(m.Files) {
		t.Fatalf("files %v manifest %d", onDisk, len(m.Files))
	}
	for _, s := range run.Steps {
		for _, a := range s.Evidence {
			rel, _ := filepath.Rel(dir, a.Path)
			if f := listed[filepath.ToSlash(rel)]; f.ArtifactID != a.ArtifactID || f.SHA256 != a.SHA256 {
				t.Fatalf("evidence %+v vs manifest %+v", a, f)
			}
		}
	}
	for _, rel := range []string{"report.json", "report.md", "report.html"} {
		if f, ok := listed[rel]; !ok || f.Kind != "report" {
			t.Fatalf("manifest lacks %s: %+v", rel, f)
		}
	}
}

func TestQARunCancel(t *testing.T) {
	pc := fixturePC()
	pc.blockOp = "exec.run"
	e := newEnv(t, pc, testOpts{})
	run := e.start(t, `name: cancel me
steps:
  - exec: slow
  - exec: never
teardown:
  - assert_ui: {name: Send}
`, nil)
	<-pc.started
	got, err := e.m.Cancel(run.RunID, tester)
	if err != nil || !got.CancelRequested {
		t.Fatalf("Cancel %+v %v", got, err)
	}
	done := e.wait(t, run.RunID)
	if done.Status != RunCancelled || statuses(done) != "blocked not_run pass" || !strings.Contains(done.Steps[0].Message, "취소") {
		t.Fatalf("run %s %s %+v", done.Status, statuses(done), done.Steps[0])
	}
	job, _ := e.jr.Get(context.Background(), done.JobID)
	if job.Status != model.StatusCancelled {
		t.Fatalf("job %+v", job)
	}
	again, err := e.m.Cancel(run.RunID, tester)
	if err != nil || again.Status != RunCancelled {
		t.Fatalf("second Cancel %+v %v", again, err)
	}

	passed := e.run(t, "name: done\nsteps:\n  - sleep: 1\n")
	if _, err := e.m.Cancel(passed.RunID, tester); apierr.CodeOf(err) != wire.CodeInvalidArgument {
		t.Fatalf("Cancel of a finished run: %v", err)
	}
	if _, err := e.m.Cancel("q-none", tester); apierr.CodeOf(err) != wire.CodeNotFound {
		t.Fatalf("Cancel of an unknown run: %v", err)
	}

	t.Run("a push in progress is cancelled with the run", func(t *testing.T) {
		push := &fakePush{wait: true, started: make(chan struct{}), ended: make(chan error, 1)}
		e := newEnv(t, fixturePC(), testOpts{push: push})
		if err := os.WriteFile(filepath.Join(e.dir, "app.exe"), []byte("exe"), 0o600); err != nil {
			t.Fatal(err)
		}
		run := e.start(t, "name: push\nsetup:\n  - push: {from: app.exe, to: 'C:\\qa\\app.exe'}\nsteps:\n  - sleep: 1\n", nil)
		<-push.started
		if _, err := e.m.Cancel(run.RunID, tester); err != nil {
			t.Fatal(err)
		}
		if err := <-push.ended; !errors.Is(err, context.Canceled) {
			t.Fatalf("push ended with %v", err)
		}
		if done := e.wait(t, run.RunID); done.Status != RunCancelled {
			t.Fatalf("run %s", done.Status)
		}
	})

	t.Run("the first cancel during the teardown lets the teardown finish", func(t *testing.T) {
		pc := fixturePC()
		started, release := make(chan struct{}), make(chan struct{})
		pc.exec = func(p proto.Message) (proto.Message, error) {
			close(started)
			<-release
			return proto.Message{"code": float64(0)}, nil
		}
		e := newEnv(t, pc, testOpts{})
		run := e.start(t, "name: td\nsteps:\n  - sleep: 1\nteardown:\n  - exec: cleanup\n  - sleep: 1\n", nil)
		<-started
		got, err := e.m.Cancel(run.RunID, tester)
		if err != nil || !got.CancelRequested {
			t.Fatalf("Cancel %+v %v", got, err)
		}
		close(release)
		done := e.wait(t, run.RunID)
		if done.Status != RunPassed || statuses(done) != "pass pass pass" || !done.CancelRequested {
			t.Fatalf("run %s %s %v", done.Status, statuses(done), done.CancelRequested)
		}
	})
}

func TestASecondCancelDuringTheTeardownStopsIt(t *testing.T) {
	pc := fixturePC()
	pc.blockOp = "exec.run"
	e := newEnv(t, pc, testOpts{})
	run := e.start(t, "name: td\nsteps:\n  - sleep: 1\nteardown:\n  - exec: slow cleanup\n  - sleep: 1\n", nil)
	<-pc.started
	if _, err := e.m.Cancel(run.RunID, tester); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if cur, _ := e.m.Get(run.RunID); cur.FinishedAt != "" {
		t.Fatalf("the first cancel stopped the teardown: %s %s", cur.Status, statuses(cur))
	}
	if _, err := e.m.Cancel(run.RunID, tester); err != nil {
		t.Fatal(err)
	}
	done := e.wait(t, run.RunID)
	if done.Status != RunCancelled || statuses(done) != "pass blocked not_run" || !strings.Contains(done.Steps[1].Message, "취소") {
		t.Fatalf("run %s %s", done.Status, statuses(done))
	}
}

func TestOneRunPerHost(t *testing.T) {
	pc := fixturePC()
	pc.blockOp = "exec.run"
	e := newEnv(t, pc, testOpts{})
	first := e.start(t, "name: a\nsteps:\n  - exec: slow\n", nil)
	<-pc.started
	_, err := e.m.Start(context.Background(), StartParams{HostRef: "pc", Text: []byte("name: b\nsteps:\n  - sleep: 1\n")}, tester)
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != wire.CodeInProgress || !ae.Retryable || !strings.Contains(ae.Message, first.RunID) {
		t.Fatalf("second Start: %v", err)
	}
	if _, err := e.m.Cancel(first.RunID, tester); err != nil {
		t.Fatal(err)
	}
	e.wait(t, first.RunID)
	if run := e.run(t, "name: c\nsteps:\n  - sleep: 1\n"); run.Status != RunPassed {
		t.Fatalf("run after the first %s", run.Status)
	}
}

func TestStartRefusesBadRequests(t *testing.T) {
	e := newEnv(t, fixturePC(), testOpts{})
	cases := []struct {
		p    StartParams
		code string
		want string
	}{
		{StartParams{HostRef: "pc"}, wire.CodeInvalidArgument, "scenario"},
		{StartParams{HostRef: "pc", Path: "/x.yaml", Text: []byte("name: x")}, wire.CodeInvalidArgument, "scenario"},
		{StartParams{HostRef: "pc", Path: "relative.yaml"}, wire.CodeInvalidArgument, "절대 경로"},
		{StartParams{HostRef: "pc", Path: filepath.Join(e.dir, "none.yaml")}, wire.CodeNotFound, "none.yaml"},
		{StartParams{Text: []byte("name: x\nsteps:\n  - sleep: 1\n")}, wire.CodeInvalidArgument, "host"},
		{StartParams{HostRef: "nowhere", Text: []byte("name: x\nsteps:\n  - sleep: 1\n")}, wire.CodeNotFound, "nowhere"},
		{StartParams{HostRef: "pc", Text: []byte("name: x\nsteps:\n  - slep: 1\n")}, wire.CodeInvalidArgument, "scenario:3:"},
		{StartParams{HostRef: "pc", Text: []byte("name: x\nsteps:\n  - sleep: 1\n"), TimeoutMS: -1}, wire.CodeInvalidArgument, "timeout_ms"},
	}
	for _, c := range cases {
		_, err := e.m.Start(context.Background(), c.p, tester)
		var ae *apierr.Error
		if !errors.As(err, &ae) || ae.Code != c.code || !strings.Contains(ae.Message, c.want) {
			t.Errorf("Start(%+v) = %v, want %s containing %q", c.p, err, c.code, c.want)
		}
	}
	path := filepath.Join(e.dir, "smoke.yaml")
	if err := os.WriteFile(path, []byte("name: from file\nhost: pc\nsteps:\n  - bogus: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := e.m.Start(context.Background(), StartParams{Path: path}, tester)
	if err == nil || !strings.Contains(err.Error(), path+":4:") {
		t.Fatalf("file scenario error %v", err)
	}
}

func TestQAUpdatedRevGrowsAndSurvivesReload(t *testing.T) {
	e := newEnv(t, fixturePC(), testOpts{})
	sub := e.bus.Subscribe()
	defer sub.Close()
	run := e.run(t, "name: revs\nsteps:\n  - exec: a\n  - exec: b\n")
	var revs []int64
	var last Run
	stepEvents := 0
	timeout := time.After(5 * time.Second)
	for last.FinishedAt == "" || last.Report == nil {
		select {
		case ev := <-sub.C:
			if ev.Kind != events.QAUpdated {
				continue
			}
			r := ev.Fields["run"].(Run)
			if r.RunID != run.RunID {
				continue
			}
			if len(r.Steps) != 0 {
				t.Fatalf("event run carries steps")
			}
			if s, ok := ev.Fields["step"].(StepResult); ok && s.Index > 0 {
				stepEvents++
			}
			revs = append(revs, r.Rev)
			last = r
		case <-timeout:
			t.Fatalf("events %v", revs)
		}
	}
	for i, r := range revs {
		if r != int64(i+1) {
			t.Fatalf("revs %v", revs)
		}
	}
	if stepEvents != 4 {
		t.Fatalf("step events %d", stepEvents)
	}
	stored, err := e.m.Get(run.RunID)
	if err != nil || stored.Rev != last.Rev {
		t.Fatalf("stored rev %d, last event %d", stored.Rev, last.Rev)
	}
	other := e.manager(testOpts{push: &fakePush{}})
	defer other.Close()
	if n := other.Load(); n != 0 {
		t.Fatalf("Load settled %d finished runs", n)
	}
	again, err := other.Get(run.RunID)
	if err != nil || again.Rev != last.Rev || again.Status != RunPassed || len(again.Steps) != 2 {
		t.Fatalf("reloaded %+v %v", again, err)
	}
}

func TestUnfinishedRunEndsAsErrorOnLoad(t *testing.T) {
	pc := fixturePC()
	pc.blockOp = "exec.run"
	e := newEnv(t, pc, testOpts{})
	run := e.start(t, "name: interrupted\nsteps:\n  - exec: slow\n  - exec: next\nteardown:\n  - exec: cleanup\n", nil)
	<-pc.started
	e.m.Close()
	before, err := e.st.GetQARun(run.RunID)
	if err != nil || before.Status != RunRunning {
		t.Fatalf("stored run %+v %v", before, err)
	}
	m := e.manager(testOpts{push: &fakePush{}})
	e.m = m
	if n := m.Load(); n != 1 {
		t.Fatalf("Load settled %d runs", n)
	}
	got, err := m.Get(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != RunError || got.Error == nil || got.Error.Code != wire.CodeExecutionOutcomeUnknown || statuses(got) != "blocked not_run not_run" ||
		got.Rev <= before.Rev || got.Report == nil {
		t.Fatalf("settled %s %s %+v", got.Status, statuses(got), got)
	}
	if _, err := os.Stat(got.Report.HTML); err != nil {
		t.Fatalf("report %v", err)
	}
	job, _ := e.jr.Get(context.Background(), got.JobID)
	if job.Status != model.StatusUnknown {
		t.Fatalf("job %+v", job)
	}
}

func TestQAListPagesNewestFirst(t *testing.T) {
	e := newEnv(t, fixturePC(), testOpts{})
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, e.run(t, "name: r\nsteps:\n  - sleep: 1\n").RunID)
	}
	page, next, err := e.m.List("", 2, "")
	if err != nil || len(page) != 2 || page[0].RunID != ids[4] || page[1].RunID != ids[3] || next == "" || len(page[0].Steps) != 0 {
		t.Fatalf("page 1 %+v %q %v", page, next, err)
	}
	page, next, err = e.m.List("pc", 2, next)
	if err != nil || len(page) != 2 || page[0].RunID != ids[2] || next == "" {
		t.Fatalf("page 2 %+v %q %v", page, next, err)
	}
	page, next, err = e.m.List("", 2, next)
	if err != nil || len(page) != 1 || page[0].RunID != ids[0] || next != "" {
		t.Fatalf("page 3 %+v %q %v", page, next, err)
	}
	for _, bad := range []int{-1, 501} {
		if _, _, err := e.m.List("", bad, ""); apierr.CodeOf(err) != wire.CodeInvalidArgument {
			t.Fatalf("limit %d: %v", bad, err)
		}
	}
	if page, _, _ := e.m.List("other", 0, ""); len(page) != 0 {
		t.Fatalf("other host %+v", page)
	}
}

func TestSecretValuesStayOutOfTheRunRecord(t *testing.T) {
	e := newEnv(t, fixturePC(), testOpts{})
	run := e.wait(t, e.start(t, "name: 로그인 ${secret_pw}\nsteps:\n  - ui_act: {name: Message, action: set_value, value: '${secret_pw}'}\n",
		map[string]string{"secret_pw": "hunter2"}).RunID)
	b, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "hunter2") || run.Name != "로그인 ***" {
		t.Fatalf("run %s", b)
	}
	job, _ := e.jr.Get(context.Background(), run.JobID)
	if strings.Contains(job.Summary, "hunter2") || strings.Contains(string(job.Result), "hunter2") {
		t.Fatalf("job %+v", job)
	}
	entries, err := e.st.ListTimeline(store.TimelineFilter{HostID: "pc", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range entries {
		if strings.Contains(en.Summary, "hunter2") {
			t.Fatalf("timeline %+v", en)
		}
	}
	report, _ := os.ReadFile(run.Report.JSON)
	if strings.Contains(string(report), "hunter2") {
		t.Fatalf("report.json holds the secret")
	}
}

func TestSecretValuesStayMaskedWhenEscapedOrCut(t *testing.T) {
	pc := fixturePC()
	secret := `Zq9p&ss"w\rd<x>`
	pc.setText("Transcript", strings.Repeat("가", 1997)+secret+" 끝")
	pc.setCap("screen", "ready")
	e := newEnv(t, pc, testOpts{})
	run := e.wait(t, e.start(t, `name: leak
steps:
  - ui_act: {name: Message, action: set_value, value: '${secret_pw}'}
  - assert_ui: {name: Message, text: nope}
    timeout_ms: 50
    continue_on_fail: true
  - assert_ui: {name: Transcript, text: nope}
    timeout_ms: 50
    continue_on_fail: true
  - wait_window: {title: '${secret_pw}'}
    timeout_ms: 50
`, map[string]string{"secret_pw": secret}).RunID)
	if statuses(run) != "pass fail fail fail" {
		t.Fatalf("statuses %s %+v", statuses(run), run.Steps)
	}
	b, _ := json.Marshal(run)
	if strings.Contains(string(b), "Zq9") {
		t.Fatalf("the run holds a part of the secret: %s", b)
	}
	err := filepath.WalkDir(run.ReportDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if data, _ := os.ReadFile(p); strings.Contains(string(data), "Zq9") {
			t.Errorf("%s holds a part of the secret", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBundleFailureEndsTheRunInError(t *testing.T) {
	pc := fixturePC()
	pc.blockOp = "ui.act"
	e := newEnv(t, pc, testOpts{})
	run := e.start(t, "name: no bundle\nsteps:\n  - ui_act: {name: Send, action: invoke}\n    timeout_ms: 200\n", nil)
	<-pc.started
	if err := os.WriteFile(artifacts.Dir(run.JobID), []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := e.wait(t, run.RunID)
	if done.Status != RunError || done.Error == nil || done.Error.Code != wire.CodeInternal || done.Report != nil || done.ReportDir != "" {
		t.Fatalf("run %s %+v %+v", done.Status, done.Error, done.Report)
	}
	job, _ := e.jr.Get(context.Background(), done.JobID)
	if job.Status != model.StatusUnknown {
		t.Fatalf("job %+v", job)
	}
}

// evidenceImage decodes the PNG evidence a.
func evidenceImage(t *testing.T, a model.Artifact) image.Image {
	t.Helper()
	f, err := os.Open(a.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("evidence %s: %v", a.Name, err)
	}
	return img
}

// evidenceNames are the kind:name pairs of the evidence of s.
func evidenceNames(s StepResult) string {
	var out []string
	for _, a := range s.Evidence {
		out = append(out, a.Kind+":"+a.Name)
	}
	return strings.Join(out, " ")
}

// desktopPC is the fixture PC with a ready screen on two displays and the
// foreground window 메모장 at (700, 40), 120×80, on the second display.
func desktopPC() *fakePC {
	pc := fixturePC()
	pc.setCap("screen", "ready")
	pc.displays = []fakeDisplay{newFakeDisplay(0, 0, 0, 640, 360), newFakeDisplay(1, 640, 0, 400, 300)}
	pc.mu.Lock()
	pc.windows = append(pc.windows, map[string]any{"id": "0x20", "title": "메모장", "process": "notepad.exe", "pid": float64(77),
		"x": float64(700), "y": float64(40), "w": float64(120), "h": float64(80), "foreground": true})
	pc.mu.Unlock()
	return pc
}

func TestAWindowScreenshotIsItsDisplayCroppedToTheWindow(t *testing.T) {
	pc := desktopPC()
	e := newEnv(t, pc, testOpts{})
	run := e.run(t, `name: window shot
steps:
  - screenshot: {window: 메모장, max_width: 200}
  - screenshot: {name: 앞 창, window: foreground}
  - assert_ui: {name: Missing}
    timeout_ms: 40
`)
	if statuses(run) != "pass pass fail" {
		t.Fatalf("statuses %s %+v", statuses(run), run.Steps)
	}
	shots := pc.callsOf("screen.capture")
	if len(shots) != 3 {
		t.Fatalf("screen.capture calls %+v", shots)
	}
	for _, c := range shots[:2] {
		if _, ok := c.params["window"]; ok || c.params.Int("display") != 1 {
			t.Fatalf("screen.capture %v", c.params)
		}
	}
	if shots[0].params.Int("max_width") != 200 {
		t.Fatalf("screen.capture %v", shots[0].params)
	}
	for i, want := range []struct{ w, h, lastX, lastY int }{{60, 40, 818, 118}, {120, 80, 819, 119}} {
		s := run.Steps[i]
		if len(s.Evidence) != 1 || s.Evidence[0].Kind != "screenshot" {
			t.Fatalf("step %d evidence %+v", i+1, s.Evidence)
		}
		img := evidenceImage(t, s.Evidence[0])
		b := img.Bounds()
		if b.Dx() != want.w || b.Dy() != want.h || s.Result["width"] != float64(want.w) || s.Result["height"] != float64(want.h) {
			t.Fatalf("step %d: image %v, result %+v", i+1, b, s.Result)
		}
		if got := color.NRGBAModel.Convert(img.At(b.Min.X, b.Min.Y)); got != desktopPixel(700, 40) {
			t.Fatalf("step %d: top left %v, want the desktop pixel (700, 40) %v", i+1, got, desktopPixel(700, 40))
		}
		if got := color.NRGBAModel.Convert(img.At(b.Max.X-1, b.Max.Y-1)); got != desktopPixel(want.lastX, want.lastY) {
			t.Fatalf("step %d: bottom right %v, want the desktop pixel (%d, %d)", i+1, got, want.lastX, want.lastY)
		}
	}
	if got := evidenceNames(run.Steps[2]); !strings.Contains(got, "screenshot:03-assert_ui-screenshot.png") {
		t.Fatalf("failure evidence %s", got)
	}
}

func TestAFailedWindowScreenshotKeepsTheFailureScreenshots(t *testing.T) {
	pc := desktopPC()
	e := newEnv(t, pc, testOpts{})
	run := e.run(t, `name: missing window
steps:
  - screenshot: {window: 없는 창}
    continue_on_fail: true
  - assert_ui: {name: Missing}
    timeout_ms: 40
`)
	if statuses(run) != "fail fail" || run.Steps[0].Error == nil || run.Steps[0].Error.Code != wire.CodeNotFound {
		t.Fatalf("statuses %s %+v", statuses(run), run.Steps)
	}
	if got := evidenceNames(run.Steps[0]); got != "screenshot:01-screenshot-screenshot.png" {
		t.Fatalf("step 1 evidence %s", got)
	}
	if got := evidenceNames(run.Steps[1]); !strings.Contains(got, "screenshot:02-assert_ui-screenshot.png") {
		t.Fatalf("step 2 evidence %s", got)
	}
}

func TestAWindowScreenshotWithoutUIAIsBlocked(t *testing.T) {
	pc := desktopPC()
	pc.setCap("uia", "unavailable")
	e := newEnv(t, pc, testOpts{})
	run := e.run(t, `name: no uia
steps:
  - screenshot: {window: 메모장}
`)
	s := run.Steps[0]
	if run.Status != RunBlocked || s.Status != StepBlocked || s.Error == nil || s.Error.Code != wire.CodeCapabilityUnsupported {
		t.Fatalf("run %s %+v", run.Status, s)
	}
	if ops := strings.Join(pc.ops(), " "); strings.Contains(ops, "win.list") || strings.Contains(ops, "screen.capture") {
		t.Fatalf("ops sent %s", ops)
	}
}

func TestWriterOpsCarryTheGenOfTheirStepsControlCheck(t *testing.T) {
	pc := fixturePC()
	gens := []uint64{7, 8, 9}
	e := newEnv(t, pc, testOpts{control: fakeControl{gens: &gens}})
	run := e.run(t, `name: gens
steps:
  - launch: C:\fixture\deskhand-fixture.exe
  - ui_act: {name: Send, action: invoke}
  - assert_ui: {name: Send}
teardown:
  - close:
`)
	if run.Status != RunPassed {
		t.Fatalf("run %s %s %+v", run.Status, statuses(run), run.Steps)
	}
	for op, want := range map[string]float64{"app.launch": 7, "ui.act": 8, "win.action": 9} {
		calls := pc.callsOf(op)
		if len(calls) != 1 || calls[0].params["gen"] != want {
			t.Fatalf("%s calls %+v, want gen %v", op, calls, want)
		}
	}
	for _, c := range pc.callsOf("ui.find") {
		if _, ok := c.params["gen"]; ok {
			t.Fatalf("ui.find carries gen: %+v", c)
		}
	}
}
