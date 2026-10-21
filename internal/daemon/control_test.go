package daemon_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/client"
	"github.com/YangTaeyoung/deskhand/internal/daemon"
	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// callErr calls method and returns its result or its coded error.
func callErr(t *testing.T, c *client.Client, method string, params any) (map[string]any, *apierr.Error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out map[string]any
	err := c.Call(ctx, method, params, &out)
	if err == nil {
		return out, nil
	}
	var e *apierr.Error
	if !errors.As(err, &e) {
		t.Fatalf("%s: %v", method, err)
	}
	return nil, e
}

// mustCall calls method and fails the test on an error.
func mustCall(t *testing.T, c *client.Client, method string, params any) map[string]any {
	t.Helper()
	out, e := callErr(t, c, method, params)
	if e != nil {
		t.Fatalf("%s: %s %s (%s)", method, e.Code, e.Message, e.NextAction)
	}
	return out
}

// waitFor waits up to 10 s for cond.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

var pc = map[string]any{"host_id": "pc"}

func acquireAs(as string, force bool) map[string]any {
	return map[string]any{"host_id": "pc", "as": as, "force": force}
}

// launchFile is the app.launch params of a program that writes file.
func launchFile(file string) map[string]any {
	return map[string]any{"host_id": "pc", "path": "/bin/sh", "args": []string{"-c", "echo ran > \"$0\"", file}, "wait_window_ms": 0}
}

func TestControlMethodsOverTheLocalAPI(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	app := dialAs(t, "app", "Deskhand")
	evs, err := app.SubscribeHost(context.Background(), "pc", []string{"control.changed"})
	if err != nil {
		t.Fatal(err)
	}
	if s := mustCall(t, app, "control.get", pc); s["owner"] != "none" || s["gen"] != float64(0) || s["ai_paused"] != false {
		t.Fatalf("initial state %v", s)
	}
	s := mustCall(t, app, "control.acquire", acquireAs("human", false))
	if s["owner"] != "human" || s["owner_client"] != "Deskhand" || s["owner_client_id"] != app.ClientID || s["gen"] != float64(1) {
		t.Fatalf("human acquire %v", s)
	}
	h := mustCall(t, app, "host.list", nil)["hosts"].([]any)[0].(map[string]any)
	if h["owner"] != "human" || h["owner_client"] != "Deskhand" {
		t.Fatalf("host %v", h)
	}
	if in := mustCall(t, app, "session.inspect", pc); in["owner"] != "human" || in["gen"] != float64(1) || in["ai_paused"] != false {
		t.Fatalf("session.inspect %v", in)
	}
	ai := dialAs(t, "mcp", "claude")
	if _, err := callErr(t, ai, "control.acquire", acquireAs("ai", false)); err == nil || err.Code != wire.CodeControlHeldByHuman ||
		err.NextAction != apierr.NextHumanRelease {
		t.Fatalf("AI acquire while a person holds: %+v", err)
	}
	if s := mustCall(t, app, "control.release", pc); s["owner"] != "none" || s["gen"] != float64(2) {
		t.Fatalf("release %v", s)
	}
	if s := mustCall(t, ai, "control.acquire", acquireAs("ai", false)); s["owner"] != "ai" || s["owner_client"] != "claude" ||
		s["owner_client_id"] != ai.ClientID || s["gen"] != float64(3) {
		t.Fatalf("AI acquire %v", s)
	}
	if _, err := callErr(t, app, "control.acquire", acquireAs("human", false)); err == nil || err.Code != wire.CodeInvalidArgument {
		t.Fatalf("a person without force while an AI holds: %+v", err)
	}
	if s := mustCall(t, app, "control.acquire", acquireAs("human", true)); s["owner"] != "human" || s["gen"] != float64(4) {
		t.Fatalf("forced takeover %v", s)
	}
	if s := mustCall(t, app, "control.stop", pc); s["owner"] != "human" || s["ai_paused"] != true || s["gen"] != float64(5) {
		t.Fatalf("stop %v", s)
	}
	if _, err := callErr(t, ai, "control.resume_ai", pc); err == nil || err.Code != wire.CodePermissionDenied {
		t.Fatalf("AI resume: %+v", err)
	}
	if s := mustCall(t, app, "control.resume_ai", pc); s["owner"] != "none" || s["ai_paused"] != false || s["gen"] != float64(6) {
		t.Fatalf("resume %v", s)
	}
	if s := mustCall(t, ai, "control.acquire", acquireAs("ai", false)); s["gen"] != float64(7) {
		t.Fatalf("AI acquire %v", s)
	}
	ai.Close()
	waitFor(t, "the closed AI client's control to be released", func() bool {
		s := mustCall(t, app, "control.get", pc)
		return s["owner"] == "none" && s["gen"] == float64(8)
	})
	waitFor(t, "writer generation 8 at the agent", func() bool { return e.agent.srv.Input().Gen() == 8 })
	var gens []float64
	timeout := time.After(5 * time.Second)
	for len(gens) < 8 {
		select {
		case ev := <-evs:
			gens = append(gens, ev.Params["gen"].(float64))
		case <-timeout:
			t.Fatalf("control.changed gens %v", gens)
		}
	}
	if !slices.Equal(gens, []float64{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatalf("control.changed gens %v", gens)
	}
	if _, err := callErr(t, app, "control.get", map[string]any{"host_id": "nowhere"}); err == nil || err.Code != wire.CodeNotFound {
		t.Fatalf("unknown host: %+v", err)
	}
	if _, err := callErr(t, app, "control.acquire", acquireAs("robot", false)); err == nil || err.Code != wire.CodeInvalidArgument {
		t.Fatalf("unknown owner: %+v", err)
	}
	tl := mustCall(t, app, "timeline.list", map[string]any{"host_id": "pc", "limit": 1})["entries"].([]any)
	if last := tl[0].(map[string]any); last["kind"] != "control" || last["actor"] != "system" {
		t.Fatalf("last timeline entry %v", last)
	}
}

func TestAppLaunchCarriesTheWriterGen(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	mustCall(t, e.c, "control.acquire", acquireAs("human", false))
	mustCall(t, e.c, "control.release", pc)
	waitFor(t, "writer generation 2 at the agent", func() bool { return e.agent.srv.Input().Gen() == 2 })

	first := filepath.Join(e.agent.home, "first.txt")
	res := mustCall(t, e.c, "app.launch", launchFile(first))
	if res["status"] != "succeeded" {
		t.Fatalf("a person's launch at writer generation 2: %v", res)
	}
	waitFor(t, "the launched program", func() bool { _, err := os.Stat(first); return err == nil })

	app := dialAs(t, "app", "Deskhand")
	mustCall(t, app, "control.acquire", acquireAs("human", false))
	ai := dialAs(t, "mcp", "claude")
	blocked := filepath.Join(e.agent.home, "blocked.txt")
	res = mustCall(t, ai, "app.launch", launchFile(blocked))
	checkEnvelope(t, res)
	errv, _ := res["error"].(map[string]any)
	if res["status"] != "failed" || errv["code"] != wire.CodeControlHeldByHuman || res["action"].(map[string]any)["executed"] != false {
		t.Fatalf("AI launch while a person holds: %v", res)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(blocked); err == nil {
		t.Fatal("the refused AI launch ran on the PC")
	}

	mustCall(t, app, "control.release", pc)
	second := filepath.Join(e.agent.home, "second.txt")
	if res := mustCall(t, ai, "app.launch", launchFile(second)); res["status"] != "succeeded" {
		t.Fatalf("AI launch on a free PC: %v", res)
	}
	s := mustCall(t, ai, "control.get", pc)
	if s["owner"] != "ai" || s["owner_client_id"] != ai.ClientID || s["gen"] != float64(5) {
		t.Fatalf("state after the AI launch %v", s)
	}
	waitFor(t, "writer generation 5 at the agent", func() bool { return e.agent.srv.Input().Gen() == 5 })
}

func TestStopAppliesOfflineAndReplaysOnConnect(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	controller, err := daemon.ControllerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	e.agent.stop()
	e.waitState(t, "reconnecting")
	s := mustCall(t, e.c, "control.stop", pc)
	if s["owner"] != "human" || s["ai_paused"] != true || s["gen"] != float64(1) {
		t.Fatalf("stop while offline %v", s)
	}
	agent := startAgent(t, e.agent.port, e.agent.id, e.agent.dataDir, controller.Fingerprint)
	if agent.srv.Input().Gen() != 0 {
		t.Fatalf("a new agent starts at writer generation %d", agent.srv.Input().Gen())
	}
	e.waitState(t, "ready")
	waitFor(t, "the stop's writer generation at the reconnected agent", func() bool { return agent.srv.Input().Gen() == 1 })

	ai := dialAs(t, "mcp", "claude")
	if _, err := callErr(t, ai, "control.acquire", acquireAs("ai", false)); err == nil || err.Code != wire.CodeControlHeldByHuman ||
		err.NextAction != apierr.NextHumanResumeAI {
		t.Fatalf("AI acquire after the reconnect: %+v", err)
	}
	blocked := filepath.Join(agent.home, "blocked.txt")
	res := mustCall(t, ai, "app.launch", launchFile(blocked))
	if errv, _ := res["error"].(map[string]any); res["status"] != "failed" || errv["next_action"] != apierr.NextHumanResumeAI {
		t.Fatalf("AI launch after the reconnect: %v", res)
	}
	ran := filepath.Join(agent.home, "ran.txt")
	if res := mustCall(t, e.c, "app.launch", launchFile(ran)); res["status"] != "succeeded" {
		t.Fatalf("a person's launch after the reconnect: %v", res)
	}
	if _, err := os.Stat(blocked); err == nil {
		t.Fatal("the refused AI launch ran on the PC")
	}
}

func TestStopSurvivesDaemonRestart(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	app := dialAs(t, "app", "Deskhand")
	mustCall(t, app, "control.acquire", acquireAs("human", false))
	mustCall(t, app, "control.release", pc)
	before := mustCall(t, app, "control.stop", pc)
	if before["gen"] != float64(3) || before["ai_paused"] != true {
		t.Fatalf("stop %v", before)
	}
	app.Close()
	e.c.Close()
	e.stop()

	stop := startDaemon(t, "")
	t.Cleanup(stop)
	ai := dialAs(t, "mcp", "claude")
	after := mustCall(t, ai, "control.get", pc)
	if after["gen"] != float64(3) || after["ai_paused"] != true || after["owner"] != "human" {
		t.Fatalf("state after the restart %v", after)
	}
	if _, err := callErr(t, ai, "control.acquire", acquireAs("ai", false)); err == nil || err.Code != wire.CodeControlHeldByHuman ||
		err.NextAction != apierr.NextHumanResumeAI {
		t.Fatalf("AI acquire after the restart: %+v", err)
	}
	cli := dialAs(t, "cli", "itest")
	waitFor(t, "the host to connect", func() bool {
		h := mustCall(t, cli, "host.list", nil)["hosts"].([]any)[0].(map[string]any)
		return h["state"] == "ready"
	})
	res := mustCall(t, ai, "app.launch", launchFile(filepath.Join(e.agent.home, "x.txt")))
	if errv, _ := res["error"].(map[string]any); res["status"] != "failed" || errv["code"] != wire.CodeControlHeldByHuman ||
		errv["next_action"] != apierr.NextHumanResumeAI {
		t.Fatalf("AI launch after the restart: %v", res)
	}
	if s := mustCall(t, cli, "control.resume_ai", pc); s["gen"] != float64(4) || s["ai_paused"] != false {
		t.Fatalf("resume after the restart %v", s)
	}
	waitFor(t, "writer generation 4 at the agent", func() bool { return e.agent.srv.Input().Gen() == 4 })
}
