package qa

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parseText(t *testing.T, src string, opt ParseOptions) (*Scenario, error) {
	t.Helper()
	if opt.File == "" {
		opt.File = "smoke.yaml"
	}
	return Parse([]byte(src), opt)
}

func mustParse(t *testing.T, src string, opt ParseOptions) *Scenario {
	t.Helper()
	sc, err := parseText(t, src, opt)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return sc
}

func TestScenarioParseAndValidate(t *testing.T) {
	dir := t.TempDir()
	build := filepath.Join(dir, "build", "app.exe")
	if err := os.MkdirAll(filepath.Dir(build), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(build, []byte("exe"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("a full scenario keeps its phases, kinds and values", func(t *testing.T) {
		src := `---
# 스모크
name: fixture-smoke
description: 고정 앱 점검
host: windows-dev
vars:
  qa_dir: 'C:\qa'
  greeting: 안녕
timeout_ms: 600000
setup:
  - name: 빌드 올리기
    push: {from: build/app.exe, to: '${qa_dir}\app.exe'}
  - launch:
      path: ${qa_dir}\app.exe
      args: [--log, "${qa_dir}\\fixture.log"]
steps:
  - wait_window: Deskhand Fixture
  - ui_act: {name: Message, role: edit, action: set_value, value: "${greeting} QA"}
  - ui_act: {name: Send, action: invoke}
    continue_on_fail: true
  - assert_ui:
      automation_id: "102"
      text_contains: "[done]"
    timeout_ms: 8000
  - assert_screen_text: 한글 라벨
  - assert_file:
      path: ${qa_dir}\fixture.log
      contains: [send, done]
      matches: 'send .*QA'
  - exec: |
      Get-Process | Select-Object -First 1
  - screenshot:
    optional: true
  - collect: {file: '${qa_dir}\fixture.log'}
  - sleep: 250
teardown:
  - close:
`
		sc := mustParse(t, src, ParseOptions{Dir: dir, RunID: "q-1"})
		if sc.Name != "fixture-smoke" || sc.Description != "고정 앱 점검" || sc.Host != "windows-dev" || sc.TimeoutMS != 600000 {
			t.Fatalf("scenario %+v", sc)
		}
		var kinds, phases []string
		for _, st := range sc.Steps {
			kinds = append(kinds, st.Action)
			phases = append(phases, st.Phase)
		}
		wantKinds := "push launch wait_window ui_act ui_act assert_ui assert_screen_text assert_file exec screenshot collect sleep close"
		if strings.Join(kinds, " ") != wantKinds {
			t.Fatalf("kinds %v", kinds)
		}
		if phases[0] != PhaseSetup || phases[1] != PhaseSetup || phases[2] != PhaseSteps || phases[len(phases)-1] != PhaseTeardown {
			t.Fatalf("phases %v", phases)
		}
		push := sc.Steps[0].Spec.(*PushSpec)
		if push.From != build || push.To != `C:\qa\app.exe` || push.OnConflict != "replace" || !push.Mkdir || sc.Steps[0].Name != "빌드 올리기" || sc.Steps[0].Line != 11 {
			t.Fatalf("push %+v step %+v", push, sc.Steps[0])
		}
		launch := sc.Steps[1].Spec.(*LaunchSpec)
		if launch.Path != `C:\qa\app.exe` || strings.Join(launch.Args, "|") != `--log|C:\qa\fixture.log` {
			t.Fatalf("launch %+v", launch)
		}
		if w := sc.Steps[2].Spec.(*WaitWindowSpec); w.TitleContains != "Deskhand Fixture" {
			t.Fatalf("wait_window %+v", w)
		}
		set := sc.Steps[3].Spec.(*UIActSpec)
		if set.Action != "set_value" || set.Value != "안녕 QA" || set.Selector.Name != "Message" || set.Selector.Role != "edit" {
			t.Fatalf("ui_act %+v", set)
		}
		if !sc.Steps[4].ContinueOnFail {
			t.Fatal("continue_on_fail was not read")
		}
		au := sc.Steps[5].Spec.(*AssertUISpec)
		if au.Selector.AutomationID != "102" || au.TextContains == nil || *au.TextContains != "[done]" || sc.Steps[5].TimeoutMS != 8000 {
			t.Fatalf("assert_ui %+v", au)
		}
		if st := sc.Steps[6].Spec.(*AssertScreenTextSpec); strings.Join(st.Contains, "") != "한글 라벨" {
			t.Fatalf("assert_screen_text %+v", st)
		}
		af := sc.Steps[7].Spec.(*AssertFileSpec)
		if af.Path != `C:\qa\fixture.log` || strings.Join(af.Contains, ",") != "send,done" || af.Matches == nil || !af.Matches.MatchString("send 안녕 QA") {
			t.Fatalf("assert_file %+v", af)
		}
		if ex := sc.Steps[8].Spec.(*ExecSpec); ex.Cmd != "Get-Process | Select-Object -First 1\n" || ex.ExpectCode != 0 {
			t.Fatalf("exec %+v", ex)
		}
		if !sc.Steps[9].Optional {
			t.Fatal("optional was not read")
		}
		if c := sc.Steps[10].Spec.(*CollectSpec); c.File != `C:\qa\fixture.log` || c.MaxBytes != DefaultCollectBytes {
			t.Fatalf("collect %+v", c)
		}
		if s := sc.Steps[11].Spec.(*SleepSpec); s.MS != 250 {
			t.Fatalf("sleep %+v", s)
		}
		if c := sc.Steps[12].Spec.(*CloseSpec); c.Window != "" {
			t.Fatalf("close %+v", c)
		}
	})

	t.Run("vars of the run override the scenario and secret_ values are masked", func(t *testing.T) {
		src := `name: login
vars:
  user: alice
  secret_pw: hunter2
steps:
  - ui_act: {name: User, action: set_value, value: "${user}"}
  - ui_act: {name: Password, action: set_value, value: "${secret_pw}"}
  - exec: {argv: [echo, "${run_id}", "$${literal}"]}
`
		sc := mustParse(t, src, ParseOptions{Vars: map[string]string{"user": "bob"}, RunID: "q-7"})
		if v := sc.Steps[0].Spec.(*UIActSpec).Value; v != "bob" {
			t.Fatalf("user %q", v)
		}
		if v := sc.Steps[1].Spec.(*UIActSpec).Value; v != "hunter2" {
			t.Fatalf("password %q", v)
		}
		if v := sc.Steps[1].Params["value"]; v != "***" {
			t.Fatalf("displayed password %v", v)
		}
		if dv := sc.DisplayVars(); dv["secret_pw"] != "***" || dv["user"] != "bob" {
			t.Fatalf("display vars %v", dv)
		}
		if argv := sc.Steps[2].Spec.(*ExecSpec).Argv; strings.Join(argv, " ") != "echo q-7 ${literal}" {
			t.Fatalf("argv %v", argv)
		}
		if got := sc.Scrub("pw is hunter2"); got != "pw is ***" {
			t.Fatalf("scrub %q", got)
		}
	})

	t.Run("refusals name the file and line", func(t *testing.T) {
		cases := []struct {
			name, src, want string
			line            int
		}{
			{"unknown top-level key", "name: x\nhots: pc\nsteps:\n  - sleep: 1\n", `"hots"`, 2},
			{"unknown step kind", "name: x\nsteps:\n  - sleep: 1\n  - klick: {x: 1, y: 2}\n", `"klick"`, 4},
			{"two kinds in a step", "name: x\nsteps:\n  - sleep: 1\n    exec: dir\n", "하나", 3},
			{"no kind in a step", "name: x\nsteps:\n  - name: 빈 단계\n", "종류", 3},
			{"unknown key in a kind", "name: x\nsteps:\n  - ui_act: {name: Send, action: invoke, colour: red}\n", `"colour"`, 3},
			{"bad ui_act action", "name: x\nsteps:\n  - ui_act: {name: Send, action: smash}\n", `"smash"`, 3},
			{"selector without criteria", "name: x\nsteps:\n  - assert_ui: {window: foreground}\n", "선택자", 3},
			{"undefined var", "name: x\nsteps:\n  - launch: ${nowhere}\\app.exe\n", `${nowhere}`, 3},
			{"missing name", "steps:\n  - sleep: 1\n", "name", 1},
			{"missing steps", "name: x\n", "steps", 1},
			{"empty steps", "name: x\nsteps: []\n", "steps", 2},
			{"anchor", "name: x\nsteps:\n  - &a sleep: 1\n", "앵커", 3},
			{"alias", "name: x\nvars: {a: 1}\nsteps:\n  - sleep: *a\n", "별칭", 4},
			{"tag", "name: !!str x\nsteps:\n  - sleep: 1\n", "태그", 1},
			{"tab indentation", "name: x\nsteps:\n\t- sleep: 1\n", "탭", 3},
			{"duplicate key", "name: x\nname: y\nsteps:\n  - sleep: 1\n", `"name"`, 2},
			{"bad regex", "name: x\nsteps:\n  - assert_screen_text: {matches: '(unclosed'}\n", "정규식", 3},
			{"negative timeout", "name: x\nsteps:\n  - sleep: 1\n    timeout_ms: -5\n", "timeout_ms", 4},
			{"not a number", "name: x\nsteps:\n  - sleep: soon\n", "정수", 3},
			{"missing push source", "name: x\nsetup:\n  - push: {from: build/none.exe, to: 'C:\\qa\\none.exe'}\nsteps:\n  - sleep: 1\n", "none.exe", 3},
			{"bad indentation", "name: x\nsteps:\n  - sleep: 1\n   exec: dir\n", "들여쓰기", 4},
			{"two documents", "name: x\nsteps:\n  - sleep: 1\n---\nname: y\n", "문서", 4},
			{"click needs a target", "name: x\nsteps:\n  - click: {button: left}\n", "click", 3},
			{"set_value needs value", "name: x\nsteps:\n  - ui_act: {name: Message, action: set_value}\n", "value", 3},
			{"bad sha256", "name: x\nsteps:\n  - assert_file: {path: C:\\a.log, sha256: abc}\n", "sha256", 3},
			{"empty exec", "name: x\nsteps:\n  - exec: ''\n", "exec", 3},
			{"empty launch", "name: x\nsteps:\n  - launch: ''\n", "launch", 3},
			{"empty wait_window", "name: x\nsteps:\n  - wait_window: ''\n", "wait_window", 3},
			{"empty assert_screen_text", "name: x\nsteps:\n  - assert_screen_text: ''\n", "assert_screen_text", 3},
			{"empty key", "name: x\nsteps:\n  - key: ''\n", "key", 3},
			{"empty contains item", "name: x\nsteps:\n  - assert_file: {path: a.log, contains: [ok, '']}\n", "contains", 3},
			{"unclosed bracket names its line", "name: x\nsteps:\n  - exec: {argv: [a, b\n  - sleep: 1\n", "[", 3},
			{"unclosed brace at the end", "name: x\nsteps:\n  - exec: {argv: [a]\n", "{", 3},
			{"unclosed flow", "name: x\nsteps:\n  - exec: {argv: [a, b}\n", "]", 3},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				_, err := parseText(t, c.src, ParseOptions{Dir: dir})
				var se *ScenarioError
				if !errors.As(err, &se) {
					t.Fatalf("error %v is not a *ScenarioError", err)
				}
				if se.File != "smoke.yaml" || se.Line != c.line || !strings.Contains(se.Msg, c.want) {
					t.Fatalf("error %q (line %d), want line %d containing %q", err, se.Line, c.line, c.want)
				}
				if !strings.HasPrefix(err.Error(), "smoke.yaml:") {
					t.Fatalf("error text %q does not start with the file name", err)
				}
			})
		}
	})

	t.Run("a relative push source needs a base folder", func(t *testing.T) {
		src := "name: x\nsetup:\n  - push: {from: build/app.exe, to: 'C:\\qa\\app.exe'}\nsteps:\n  - sleep: 1\n"
		_, err := parseText(t, src, ParseOptions{File: "scenario"})
		var se *ScenarioError
		if !errors.As(err, &se) || se.Line != 3 || !strings.Contains(se.Msg, "base_dir") {
			t.Fatalf("error %v", err)
		}
	})

	t.Run("quoted scalars, block scalars and flow collections", func(t *testing.T) {
		src := `name: "따옴표 \"이름\"\t끝"
steps:
  - exec:
      argv: [
        cmd.exe, /c,
        'it''s'
      ]
      expect_code: 3
      stdout_contains: [a, "b c"]
  - exec:
      cmd: >-
        echo one
        two
      shell: cmd
  - assert_file: {path: C:\logs\a.log, exists: false}
  - assert_file: {path: C:\logs\b.log, sha256: ABABABABABABABABABABABABABABABABABABABABABABABABABABABABABABABAB, size: 12}
  - type: "줄\n바꿈"
  - key: ctrl+s
  - click: {x: 10, y: 20, button: right, count: 2}
  - click: {element: {name: OK, role: button}}
  - wait_window: {process: deskhand-fixture.exe, gone: true}
  - collect: {events: {log: Application, levels: [error, warning], max: 50}, name: app-events}
  - screenshot: {display: 1, max_width: 800}
`
		sc := mustParse(t, src, ParseOptions{})
		if sc.Name != "따옴표 \"이름\"\t끝" {
			t.Fatalf("name %q", sc.Name)
		}
		e := sc.Steps[0].Spec.(*ExecSpec)
		if strings.Join(e.Argv, "|") != "cmd.exe|/c|it's" || e.ExpectCode != 3 || strings.Join(e.StdoutContains, "|") != "a|b c" {
			t.Fatalf("exec %+v", e)
		}
		if e := sc.Steps[1].Spec.(*ExecSpec); e.Cmd != "echo one two" || e.Shell != "cmd" {
			t.Fatalf("folded exec %+v", e)
		}
		if a := sc.Steps[2].Spec.(*AssertFileSpec); a.Path != `C:\logs\a.log` || a.Exists == nil || *a.Exists {
			t.Fatalf("assert_file %+v", a)
		}
		if a := sc.Steps[3].Spec.(*AssertFileSpec); a.SHA256 != strings.Repeat("ab", 32) || a.Size == nil || *a.Size != 12 {
			t.Fatalf("assert_file %+v", a)
		}
		if ty := sc.Steps[4].Spec.(*TypeSpec); ty.Text != "줄\n바꿈" || ty.Method != "auto" {
			t.Fatalf("type %+v", ty)
		}
		if k := sc.Steps[5].Spec.(*KeySpec); k.Keys != "ctrl+s" {
			t.Fatalf("key %+v", k)
		}
		if c := sc.Steps[6].Spec.(*ClickSpec); c.X == nil || *c.X != 10 || *c.Y != 20 || c.Button != "right" || c.Count != 2 {
			t.Fatalf("click %+v", c)
		}
		if c := sc.Steps[7].Spec.(*ClickSpec); c.Element == nil || c.Element.Name != "OK" || c.Button != "left" || c.Count != 1 {
			t.Fatalf("click %+v", c)
		}
		if w := sc.Steps[8].Spec.(*WaitWindowSpec); w.Process != "deskhand-fixture.exe" || !w.Gone {
			t.Fatalf("wait_window %+v", w)
		}
		c := sc.Steps[9].Spec.(*CollectSpec)
		if c.Events == nil || c.Events.Log != "Application" || strings.Join(c.Events.Levels, ",") != "error,warning" || c.Events.Max != 50 || c.Name != "app-events" {
			t.Fatalf("collect %+v", c)
		}
		if s := sc.Steps[10].Spec.(*ScreenshotSpec); s.Display != 1 || s.MaxWidth != 800 {
			t.Fatalf("screenshot %+v", s)
		}
	})
}

func TestExampleScenariosParse(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "docs", "examples", "qa")
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("examples %v %v", files, err)
	}
	exe := filepath.Join(t.TempDir(), "fixture.exe")
	if err := os.WriteFile(exe, []byte("exe"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		sc, err := Parse(src, ParseOptions{File: f, Dir: dir, Vars: map[string]string{"fixture": exe}, RunID: "q-example"})
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if len(sc.Steps) == 0 {
			t.Fatalf("%s has no steps", f)
		}
	}
	src, _ := os.ReadFile(filepath.Join(dir, "fixture-smoke.yaml"))
	sc, err := Parse(src, ParseOptions{File: "fixture-smoke.yaml", Dir: dir, Vars: map[string]string{"fixture": exe}, RunID: "q-example"})
	if err != nil {
		t.Fatal(err)
	}
	launch := sc.Steps[1].Spec.(*LaunchSpec)
	if launch.Path != `C:\deskhand-qa\q-example\deskhand-fixture.exe` || strings.Join(launch.Args, " ") != `--log C:\deskhand-qa\q-example\fixture.log` {
		t.Fatalf("launch %+v", launch)
	}
	wait := sc.Steps[6].Spec.(*AssertUISpec)
	if wait.TextMatches == nil || !wait.TextMatches.MatchString("You: 안녕 QA\r\nBot: echo 안녕 QA echo") {
		t.Fatalf("assert_ui %+v", wait)
	}
}

func TestScenarioLimits(t *testing.T) {
	var b strings.Builder
	b.WriteString("name: many\nsetup:\n")
	for i := 0; i < 500; i++ {
		b.WriteString("  - sleep: 1\n")
	}
	b.WriteString("steps:\n")
	for i := 0; i < 500; i++ {
		b.WriteString("  - sleep: 1\n")
	}
	ok := b.String()
	if sc, err := Parse([]byte(ok), ParseOptions{File: "many.yaml"}); err != nil || len(sc.Steps) != 1000 {
		t.Fatalf("1000 steps: %v", err)
	}
	_, err := Parse([]byte(ok+"teardown:\n  - sleep: 1\n"), ParseOptions{File: "many.yaml"})
	var se *ScenarioError
	if !errors.As(err, &se) || !strings.Contains(se.Msg, "1000") {
		t.Fatalf("1001 steps: %v", err)
	}

	deepFlow := "name: x\nvars: {v: " + strings.Repeat("[", 101) + strings.Repeat("]", 101) + "}\nsteps:\n  - sleep: 1\n"
	if _, err := Parse([]byte(deepFlow), ParseOptions{File: "deep.yaml"}); !errors.As(err, &se) || se.Line != 2 || !strings.Contains(se.Msg, "100") {
		t.Fatalf("deep flow: %v", err)
	}
	deepSeq := strings.Repeat("- ", 101) + "x\n"
	if _, err := parseYAML("deep.yaml", []byte(deepSeq)); !errors.As(err, &se) || !strings.Contains(se.Msg, "100") {
		t.Fatalf("deep sequence: %v", err)
	}
	if _, err := parseYAML("ok.yaml", []byte("a: "+strings.Repeat("[", 99)+strings.Repeat("]", 99)+"\n")); err != nil {
		t.Fatalf("99 levels: %v", err)
	}
}

func TestParseErrorsMaskSecretValues(t *testing.T) {
	secret := `(a"b\<x>&`
	for _, src := range []string{
		"name: x\nsteps:\n  - sleep: '${secret_v}'\n",
		"name: x\nsteps:\n  - assert_file: {path: a.log, matches: '${secret_v}'}\n",
		"name: x\nsteps:\n  - click: {x: 1, y: 2, button: '${secret_v}'}\n",
		"name: x\nsteps:\n  - assert_file: {path: a.log, sha256: '${secret_v}'}\n",
	} {
		_, err := Parse([]byte(src), ParseOptions{File: "s.yaml", Vars: map[string]string{"secret_v": secret}})
		if err == nil {
			t.Fatalf("%q parsed", src)
		}
		if strings.Contains(err.Error(), "(a") || !strings.Contains(err.Error(), "***") {
			t.Fatalf("error %q shows the secret", err)
		}
	}
}
