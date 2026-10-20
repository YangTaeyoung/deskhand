package daemon_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/client"
)

// fakeUI is a UI helper for the test agent with one window: a Send button
// that writes "[done]" into the Transcript edit.
type fakeUI struct {
	mu         sync.Mutex
	transcript string
	acts       []map[string]any
	open       bool
}

func (u *fakeUI) CallContext(_ context.Context, op string, params map[string]any, _ time.Duration) (map[string]any, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	window := map[string]any{"id": "0x10", "title": "Deskhand Fixture", "pid": float64(4242), "process": "deskhand-fixture.exe",
		"x": float64(0), "y": float64(0), "w": float64(720), "h": float64(520)}
	nodes := []any{
		map[string]any{"ref": "e1", "role": "window", "name": "Deskhand Fixture", "flags": []any{}},
		map[string]any{"ref": "e2", "role": "button", "name": "Send", "flags": []any{}},
		map[string]any{"ref": "e3", "role": "edit", "name": "Transcript", "value": u.transcript, "flags": []any{}},
		map[string]any{"ref": "e4", "role": "text", "name": "한글 라벨", "flags": []any{}},
	}
	switch op {
	case "win.list":
		if !u.open {
			return map[string]any{"windows": []any{}}, nil
		}
		return map[string]any{"windows": []any{window}}, nil
	case "win.action":
		u.open = false
		return map[string]any{"window": nil}, nil
	case "ui.tree":
		return map[string]any{"window": window, "nodes": nodes, "truncated": false}, nil
	case "ui.find", "ui.text", "ui.act":
		name, _ := params["name"].(string)
		var found map[string]any
		for _, n := range nodes {
			if m := n.(map[string]any); m["name"] == name {
				found = m
			}
		}
		if op == "ui.find" {
			if found == nil {
				return map[string]any{"nodes": []any{}}, nil
			}
			return map[string]any{"nodes": []any{found}}, nil
		}
		if found == nil {
			return nil, errors.New("no element matches; call ui.tree to see what exists")
		}
		if op == "ui.text" {
			text, _ := found["value"].(string)
			return map[string]any{"text": text, "total": float64(len(text)), "source": "value"}, nil
		}
		u.acts = append(u.acts, params)
		if name == "Send" {
			u.transcript = "You: 안녕\r\nBot: echo 안녕\r\n[done]\r\n"
		}
		return map[string]any{"node": found}, nil
	}
	return nil, fmt.Errorf("unknown op '%s'", op)
}

func TestQARunThroughTheDaemon(t *testing.T) {
	ui := &fakeUI{open: true}
	e := setup(t, setupOpts{trustDaemon: true, agentUI: ui})
	e.waitState(t, "ready")
	mac := t.TempDir()
	build := filepath.Join(mac, "app.txt")
	if err := os.WriteFile(build, []byte("build 1.2.3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("build 1.2.3\n"))
	remote := filepath.Join(e.agent.home, "qa")
	scenario := fmt.Sprintf(`name: 데몬 경유
vars:
  remote: '%s'
setup:
  - push: {from: app.txt, to: '${remote}/${run_id}/app.txt'}
steps:
  - exec: {argv: [/bin/sh, -c, 'printf "send 안녕 QA\n" > "$1"; echo written', sh, '${remote}/fixture.log']}
  - assert_file: {path: '${remote}/fixture.log', contains: send 안녕 QA}
  - assert_file: {path: '${remote}/${run_id}/app.txt', sha256: %s}
  - wait_window: Deskhand Fixture
  - ui_act: {name: Send, action: invoke}
  - assert_ui: {name: Transcript, text_contains: "[done]"}
  - assert_screen_text: 한글 라벨
  - screenshot:
    optional: true
  - collect: {file: '${remote}/fixture.log'}
teardown:
  - close:
`, remote, hex.EncodeToString(sum[:]))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sub, err := client.DialOptions(ctx, "cli", "watcher", client.Options{Start: func(context.Context) error { return errors.New("no daemon") }})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	events, err := sub.Subscribe(ctx, []string{"qa.updated"})
	if err != nil {
		t.Fatal(err)
	}

	start := e.call(t, "qa.run", map[string]any{"host_id": "pc", "scenario": scenario, "base_dir": mac})
	checkEnvelope(t, start)
	runID, _ := start["run_id"].(string)
	jobID, _ := start["job_id"].(string)
	run, _ := start["run"].(map[string]any)
	if start["status"] != "running" || !strings.HasPrefix(runID, "q-") || !strings.HasPrefix(jobID, "j-") || run["run_id"] != runID ||
		len(run["steps"].([]any)) != 11 || start["action"].(map[string]any)["executed"] != true {
		t.Fatalf("qa.run %v", start)
	}

	var revs []float64
	var final map[string]any
	for final == nil {
		select {
		case ev := <-events:
			r, _ := ev.Params["run"].(map[string]any)
			if r["run_id"] != runID {
				continue
			}
			if _, ok := r["steps"]; ok {
				t.Fatalf("event run has steps: %v", r)
			}
			revs = append(revs, r["rev"].(float64))
			if r["finished_at"] != nil && r["report"] != nil {
				final = r
			}
		case <-ctx.Done():
			t.Fatalf("no final qa.updated; revs %v", revs)
		}
	}
	for i := 1; i < len(revs); i++ {
		if revs[i] <= revs[i-1] {
			t.Fatalf("revs %v", revs)
		}
	}

	got := e.call(t, "qa.get", map[string]any{"run_id": runID})
	var states []string
	for _, s := range got["steps"].([]any) {
		states = append(states, s.(map[string]any)["status"].(string))
	}
	if got["status"] != "passed" || strings.Join(states, " ") != "pass pass pass pass pass pass pass pass blocked pass pass" {
		t.Fatalf("qa.get %s %v", got["status"], got["steps"])
	}
	if b, err := os.ReadFile(filepath.Join(remote, runID, "app.txt")); err != nil || string(b) != "build 1.2.3\n" {
		t.Fatalf("pushed file %q %v", b, err)
	}
	if len(ui.acts) != 1 || ui.acts[0]["window"] != "0x10" || ui.acts[0]["name"] != "Send" {
		t.Fatalf("ui.act %v", ui.acts)
	}
	unsupported := got["unsupported"].([]any)
	if len(unsupported) != 1 || unsupported[0].(map[string]any)["action"] != "screenshot" {
		t.Fatalf("unsupported %v", unsupported)
	}

	list := e.call(t, "qa.list", map[string]any{"host_id": "pc"})
	runs := list["runs"].([]any)
	if len(runs) != 1 || runs[0].(map[string]any)["run_id"] != runID || runs[0].(map[string]any)["steps"] != nil {
		t.Fatalf("qa.list %v", list)
	}
	job := e.call(t, "jobs.get", map[string]any{"job_id": jobID})
	if job["kind"] != "qa" || job["status"] != "succeeded" || job["summary"] != "QA: 데몬 경유" {
		t.Fatalf("job %v", job)
	}

	arts := e.call(t, "artifacts.list", map[string]any{"job_id": jobID})
	names := map[string]bool{}
	for _, a := range arts["artifacts"].([]any) {
		names[a.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"report.json", "report.md", "report.html", "02-exec-stdout.txt", "10-collect-fixture.log"} {
		if !names[want] {
			t.Fatalf("artifacts.list lacks %s: %v", want, arts)
		}
	}
	out := t.TempDir()
	exp := e.call(t, "artifacts.export", map[string]any{"job_id": jobID, "dir": out})
	if exp["dir"] != filepath.Join(out, jobID) || exp["manifest"] != filepath.Join(out, jobID, "manifest.json") || exp["files"].(float64) < 6 {
		t.Fatalf("artifacts.export %v", exp)
	}
	if _, err := os.Stat(filepath.Join(out, jobID, "evidence", "02-exec-stdout.txt")); err != nil {
		t.Fatal(err)
	}
	again := e.call(t, "artifacts.export", map[string]any{"job_id": jobID, "dir": out})
	if again["dir"] != filepath.Join(out, jobID+" (1)") {
		t.Fatalf("second export %v", again)
	}

	var cerr *client.Error
	if err := e.c.Call(ctx, "qa.run", map[string]any{"host_id": "pc", "scenario": "name: x\nsteps:\n  - nope: 1\n"}, nil); !errors.As(err, &cerr) ||
		cerr.Code != "INVALID_ARGUMENT" || !strings.Contains(cerr.Message, "scenario:3:") {
		t.Fatalf("bad scenario: %v", err)
	}
	if err := e.c.Call(ctx, "qa.get", map[string]any{"run_id": "q-missing"}, nil); !errors.As(err, &cerr) || cerr.Code != "NOT_FOUND" {
		t.Fatalf("qa.get of a missing run: %v", err)
	}
	if err := e.c.Call(ctx, "qa.cancel", map[string]any{"run_id": runID}, nil); !errors.As(err, &cerr) || cerr.Code != "INVALID_ARGUMENT" {
		t.Fatalf("qa.cancel of a passed run: %v", err)
	}
}

func TestQACancelThroughJobsCancel(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true, agentUI: &fakeUI{open: true}})
	e.waitState(t, "ready")
	start := e.call(t, "qa.run", map[string]any{"host_id": "pc", "scenario": "name: long\nsteps:\n  - sleep: 30000\n  - exec: {argv: [true]}\nteardown:\n  - assert_screen_text: 한글 라벨\n"})
	runID, jobID := start["run_id"].(string), start["job_id"].(string)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		r := e.call(t, "qa.get", map[string]any{"run_id": runID})
		if s := r["steps"].([]any)[0].(map[string]any); s["status"] == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the sleep did not start: %v", r)
		}
	}
	res := e.call(t, "jobs.cancel", map[string]any{"job_id": jobID})
	checkEnvelope(t, res)
	if res["job_id"] != jobID {
		t.Fatalf("jobs.cancel %v", res)
	}
	var r map[string]any
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		r = e.call(t, "qa.get", map[string]any{"run_id": runID})
		if r["finished_at"] != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the run did not end: %v", r)
		}
	}
	var states []string
	for _, s := range r["steps"].([]any) {
		states = append(states, s.(map[string]any)["status"].(string))
	}
	if r["status"] != "cancelled" || strings.Join(states, " ") != "blocked not_run pass" || r["cancel_requested"] != true {
		t.Fatalf("cancelled run %v", r)
	}
	if job := e.call(t, "jobs.get", map[string]any{"job_id": jobID}); job["status"] != "cancelled" {
		t.Fatalf("job %v", job)
	}
	again := e.call(t, "qa.cancel", map[string]any{"run_id": runID})
	if again["status"] != "cancelled" || again["run"].(map[string]any)["status"] != "cancelled" {
		t.Fatalf("qa.cancel of a cancelled run %v", again)
	}
}
