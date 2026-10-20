package server

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// captureStart hooks startProgram to record the argv of the next launch and
// still start it.
func captureStart(t *testing.T) *[]string {
	t.Helper()
	orig := startProgram
	captured := &[]string{}
	startProgram = func(c *exec.Cmd) error {
		*captured = append([]string{}, c.Args...)
		return orig(c)
	}
	t.Cleanup(func() { startProgram = orig })
	return captured
}

func TestDebugElectronLaunchStartsWithInspectorArgs(t *testing.T) {
	captured := captureStart(t)
	c, _ := newRPCClient(t, Options{})
	path, args := exitProgram(0)
	res := c.ok("debug.electron.launch", proto.Message{"path": path, "args": args})
	pid := res.Int("pid")
	if pid <= 0 {
		t.Fatalf("pid %v", res)
	}
	ports, _ := res["ports"].(map[string]any)
	p1, p2 := int(ports["inspect"].(float64)), int(ports["remote_debugging"].(float64))
	if p1 <= 0 || p2 <= 0 || p1 == p2 {
		t.Fatalf("ports %v", res)
	}
	joined := strings.Join(*captured, " ")
	if !strings.Contains(joined, "--inspect=127.0.0.1:") || !strings.Contains(joined, "--remote-debugging-port=") {
		t.Fatalf("inspector args missing: %q", joined)
	}
	c.event("process.exited")
}

func TestDebugElectronLaunchPauseOnStartUsesInspectBrk(t *testing.T) {
	captured := captureStart(t)
	c, _ := newRPCClient(t, Options{})
	path, args := exitProgram(0)
	c.ok("debug.electron.launch", proto.Message{"path": path, "args": args, "pause_on_start": true})
	c.event("process.exited")
	joined := strings.Join(*captured, " ")
	if !strings.Contains(joined, "--inspect-brk=127.0.0.1:") {
		t.Fatalf("pause_on_start did not use --inspect-brk: %q", joined)
	}
}

func TestDebugTreeReportsExitCodeAfterExit(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	path, args := exitProgram(7)
	res := c.ok("debug.electron.launch", proto.Message{"path": path, "args": args})
	pid := res.Int("pid")
	c.event("process.exited")
	gone := c.ok("debug.tree", proto.Message{"pid": pid})
	if gone["alive"] != false {
		t.Fatalf("tree still alive after exit: %v", gone)
	}
	if gone["exit_code"] != float64(7) {
		t.Fatalf("exit_code = %v, want 7", gone["exit_code"])
	}
}

func TestDebugElectronLaunchNeedsExec(t *testing.T) {
	_, r, w, _ := startServerS(t, Options{AllowExec: false})
	expect(t, r, "hello")
	send(t, w, proto.Message{"t": "req", "id": 1, "op": "debug.electron.launch", "path": "x"})
	if res := expect(t, r, "res"); res["ok"] != false || res.String("error") != errExecDisabled {
		t.Fatalf("debug.electron.launch without exec answered %v", res)
	}
}

func TestDebugAttachPIDMissingProcess(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	res := c.call("debug.attach.pid", proto.Message{"pid": 2000000000})
	if res["ok"] != false {
		t.Fatalf("attach to a missing pid succeeded: %v", res)
	}
	code := res.String("code")
	if code != wire.CodeNotFound && code != wire.CodeCapabilityUnsupported {
		t.Fatalf("attach pid code = %q", code)
	}
}
