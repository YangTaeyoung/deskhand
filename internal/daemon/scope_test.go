package daemon_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func updateHost(t *testing.T, e *env, params map[string]any) map[string]any {
	t.Helper()
	params["host_id"] = "pc"
	return mustCall(t, e.c, "host.update", params)
}

func TestHostUpdateChangesNameAddressPortAndSSHAlias(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	h := updateHost(t, e, map[string]any{"name": "작업 PC", "ssh_alias": "work-pc"})
	if h["name"] != "작업 PC" || h["ssh_alias"] != "work-pc" || h["host_id"] != "pc" {
		t.Fatalf("host.update %v", h)
	}
	list := e.call(t, "host.list", nil)["hosts"].([]any)[0].(map[string]any)
	if list["name"] != "작업 PC" || list["ssh_alias"] != "work-pc" {
		t.Fatalf("host.list %v", list)
	}
	if _, err := callErr(t, e.c, "host.update", map[string]any{"host_id": "pc", "port": 0}); err == nil || err.Code != wire.CodeInvalidArgument {
		t.Fatalf("port 0: %+v", err)
	}
	if _, err := callErr(t, e.c, "host.update", map[string]any{"host_id": "pc", "name": " "}); err == nil || err.Code != wire.CodeInvalidArgument {
		t.Fatalf("empty name: %+v", err)
	}
	if _, err := callErr(t, e.c, "host.update", map[string]any{"host_id": "pc"}); err == nil || err.Code != wire.CodeInvalidArgument {
		t.Fatalf("nothing to change: %+v", err)
	}
	if _, err := callErr(t, e.c, "host.update", map[string]any{"host_id": "nope", "name": "x"}); err == nil || err.Code != wire.CodeNotFound {
		t.Fatalf("unknown host: %+v", err)
	}
}

func TestHostUpdateAddressChangeReconnectsToTheNewTarget(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	first := e.host(t)
	updateHost(t, e, map[string]any{"port": 9})
	waitFor(t, "the host to leave ready on a port nothing listens on", func() bool { return e.host(t).State != "ready" })
	h := e.call(t, "host.list", nil)["hosts"].([]any)[0].(map[string]any)
	if h["port"] != float64(9) {
		t.Fatalf("port %v", h["port"])
	}
	updateHost(t, e, map[string]any{"port": e.agent.port})
	back := e.waitState(t, "ready")
	if back.Gen <= first.Gen {
		t.Fatalf("connection generation %v after %v", back.Gen, first.Gen)
	}
}

func TestHostUpdateSurvivesDaemonRestart(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	updateHost(t, e, map[string]any{"name": "renamed", "ai_scope": map[string]any{"screen": false, "files": "none"}})
	e.stop()
	stop := startDaemon(t, "")
	t.Cleanup(stop)
	c := dialAs(t, "cli", "again")
	h := mustCall(t, c, "host.list", nil)["hosts"].([]any)[0].(map[string]any)
	scope := h["ai_scope"].(map[string]any)
	if h["name"] != "renamed" || scope["screen"] != false || scope["files"] != "none" || scope["clipboard"] != true {
		t.Fatalf("host after restart %v", h)
	}
}

func TestAIScopeDefaultsToUnrestricted(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	scope := e.call(t, "host.list", nil)["hosts"].([]any)[0].(map[string]any)["ai_scope"].(map[string]any)
	if scope["screen"] != true || scope["files"] != "all" || scope["clipboard"] != true {
		t.Fatalf("default scope %v", scope)
	}
	ai := dialAs(t, "mcp", "claude")
	dir := t.TempDir()
	if st := mustCall(t, ai, "files.stat", map[string]any{"host_id": "pc", "path": dir}); st["exists"] != true {
		t.Fatalf("AI stat %v", st)
	}
}

func TestAIClientCannotChangeItsOwnScope(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	updateHost(t, e, map[string]any{"ai_scope": map[string]any{"files": "none"}})
	ai := dialAs(t, "mcp", "claude")
	_, err := callErr(t, ai, "host.update", map[string]any{"host_id": "pc", "ai_scope": map[string]any{"files": "all"}})
	if err == nil || err.Code != wire.CodePermissionDenied {
		t.Fatalf("AI host.update: %+v", err)
	}
	if got := e.call(t, "host.list", nil)["hosts"].([]any)[0].(map[string]any)["ai_scope"].(map[string]any)["files"]; got != "none" {
		t.Fatalf("files scope %v", got)
	}
}

func TestAIFileScopeLimitsFilesAndTransfersOfAIClientsOnly(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	root := filepath.Join(e.agent.home, "work")
	other := filepath.Join(e.agent.home, "private")
	for _, d := range []string{root, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	updateHost(t, e, map[string]any{"ai_scope": map[string]any{"files": "work_roots", "work_roots": []string{root}}})
	ai := dialAs(t, "mcp", "claude")
	if st := mustCall(t, ai, "files.stat", map[string]any{"host_id": "pc", "path": filepath.Join(root, "x.txt")}); st["exists"] != false {
		t.Fatalf("stat in a work root %v", st)
	}
	for name, p := range map[string]map[string]any{
		"outside":        {"host_id": "pc", "path": other},
		"dotdot":         {"host_id": "pc", "path": filepath.Join(root, "..", "private")},
		"sibling prefix": {"host_id": "pc", "path": root + "-x"},
		"empty":          {"host_id": "pc", "path": ""},
	} {
		if _, err := callErr(t, ai, "files.list", p); err == nil || err.Code != wire.CodePermissionDenied {
			t.Fatalf("%s: %+v", name, err)
		}
	}
	if _, err := callErr(t, ai, "files.rename", map[string]any{"host_id": "pc", "from": filepath.Join(root, "a"), "to": filepath.Join(other, "a")}); err == nil || err.Code != wire.CodePermissionDenied {
		t.Fatalf("rename out of the root: %+v", err)
	}
	if _, err := callErr(t, ai, "transfer.start", map[string]any{"host_id": "pc", "direction": "download", "local": t.TempDir(), "remote": other}); err == nil || err.Code != wire.CodePermissionDenied {
		t.Fatalf("transfer from outside the root: %+v", err)
	}
	if got := mustCall(t, e.c, "files.list", map[string]any{"host_id": "pc", "path": other}); got["path"] != other {
		t.Fatalf("a person's list %v", got)
	}
	updateHost(t, e, map[string]any{"ai_scope": map[string]any{"files": "none"}})
	if _, err := callErr(t, ai, "files.stat", map[string]any{"host_id": "pc", "path": root}); err == nil || err.Code != wire.CodePermissionDenied {
		t.Fatalf("stat with files none: %+v", err)
	}
	tl := mustCall(t, e.c, "timeline.list", map[string]any{"host_id": "pc", "limit": 50})
	denied := false
	for _, it := range tl["entries"].([]any) {
		if it.(map[string]any)["outcome"] == "denied" && it.(map[string]any)["actor"] == "ai" {
			denied = true
		}
	}
	if !denied {
		t.Fatalf("no denied entry on the timeline: %v", tl)
	}
}

func TestAIScreenAndClipboardScopeLimitScreenInputAndPaste(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	updateHost(t, e, map[string]any{"ai_scope": map[string]any{"clipboard": false}})
	ai := dialAs(t, "mcp", "claude")
	if _, err := callErr(t, ai, "input.type", map[string]any{"host_id": "pc", "text": "hi 😀", "method": "paste"}); err == nil || err.Code != wire.CodePermissionDenied {
		t.Fatalf("paste without the clipboard: %+v", err)
	}
	if _, err := callErr(t, ai, "input.type", map[string]any{"host_id": "pc", "text": "hi 😀"}); err == nil || err.Code != wire.CodePermissionDenied {
		t.Fatalf("auto that needs paste without the clipboard: %+v", err)
	}
	if _, err := callErr(t, ai, "input.type", map[string]any{"host_id": "pc", "text": "hi", "method": "unicode"}); err != nil && err.Code == wire.CodePermissionDenied {
		t.Fatalf("unicode typing refused by the clipboard scope: %+v", err)
	}
	updateHost(t, e, map[string]any{"ai_scope": map[string]any{"screen": false}})
	for _, m := range []string{"screen.capture", "observe", "input.click", "input.key", "input.type", "ui.tree", "ui.act", "win.list", "win.action"} {
		if _, err := callErr(t, ai, m, map[string]any{"host_id": "pc", "keys": "a", "text": "a", "x": 1, "y": 1}); err == nil || err.Code != wire.CodePermissionDenied {
			t.Fatalf("%s with the screen blocked: %+v", m, err)
		}
	}
	if _, err := callErr(t, e.c, "screen.capture", pc); err != nil && err.Code == wire.CodePermissionDenied {
		t.Fatalf("a person's screen.capture refused: %+v", err)
	}
}

func TestAIScreenScopeRefusesAIScreenStreamsOnly(t *testing.T) {
	agent := newScreenAgent()
	e := setup(t, setupOpts{trustDaemon: true, agentStreams: agent.streams()})
	e.waitState(t, "ready")
	updateHost(t, e, map[string]any{"ai_scope": map[string]any{"screen": false}})
	_, err := attachScreenAs(t, "mcp", map[string]any{"host_id": "pc", "display": 0})
	if err == nil || apiCode(err) != wire.CodePermissionDenied {
		t.Fatalf("AI stream.attach with the screen blocked: %v", err)
	}
	s, err := attachScreenAs(t, "app", map[string]any{"host_id": "pc", "display": 0})
	if err != nil {
		t.Fatalf("a person's stream.attach: %v", err)
	}
	s.Close()
}

func denied(t *testing.T, what string, err *apierr.Error) {
	t.Helper()
	if err == nil || err.Code != wire.CodePermissionDenied {
		t.Fatalf("%s: %+v", what, err)
	}
}

func TestAIClientCannotAddRemoveRepairOrConnectHosts(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	updateHost(t, e, map[string]any{"ai_scope": map[string]any{"files": "none"}})
	ai := dialAs(t, "mcp", "claude")
	for _, m := range []string{"host.add", "host.remove", "host.repair", "host.connect", "host.disconnect", "host.update"} {
		_, err := callErr(t, ai, m, map[string]any{"host_id": "pc", "name": "again", "ssh": "pc", "uninstall": true})
		denied(t, m, err)
	}
	hosts := e.call(t, "host.list", nil)["hosts"].([]any)
	if len(hosts) != 1 || hosts[0].(map[string]any)["ai_scope"].(map[string]any)["files"] != "none" {
		t.Fatalf("hosts after the AI's attempts %v", hosts)
	}
}

func TestAIPrivilegedMethodsAreRefusedOnLimitedHostsOnly(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	ai := dialAs(t, "mcp", "claude")
	if _, err := callErr(t, ai, "exec.run", map[string]any{"host_id": "pc", "argv": []string{"echo", "hi"}}); err != nil && err.Code == wire.CodePermissionDenied {
		t.Fatalf("exec.run on an unlimited host: %+v", err)
	}
	for name, scope := range map[string]map[string]any{
		"screen":    {"screen": false},
		"files":     {"files": "work_roots", "work_roots": []string{e.agent.home}},
		"clipboard": {"clipboard": false},
	} {
		updateHost(t, e, map[string]any{"ai_scope": map[string]any{"screen": true, "files": "all", "clipboard": true}})
		updateHost(t, e, map[string]any{"ai_scope": scope})
		for _, m := range []string{"exec.run", "exec.start", "proc.list", "proc.kill", "app.launch", "pty.open", "tunnel.open", "tunnel.list",
			"debug.list", "debug.attach", "debug.electron.launch"} {
			_, err := callErr(t, ai, m, map[string]any{"host_id": "pc", "argv": []string{"echo"}, "cmd": "echo", "path": "cmd.exe", "port": 9229, "remote_port": 80, "pid": 1})
			denied(t, name+" "+m, err)
		}
		if _, err := callErr(t, e.c, "proc.list", pc); err != nil && err.Code == wire.CodePermissionDenied {
			t.Fatalf("a person's proc.list refused: %+v", err)
		}
	}
	updateHost(t, e, map[string]any{"ai_scope": map[string]any{"screen": true, "files": "all", "clipboard": true}})
	if _, err := callErr(t, ai, "proc.list", pc); err != nil && err.Code == wire.CodePermissionDenied {
		t.Fatalf("proc.list after the limits were lifted: %+v", err)
	}
}

func TestAIJobOutputOfLimitedHostIsRefused(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	start := e.call(t, "exec.start", map[string]any{"host_id": "pc", "argv": []string{"echo", "secret"}})
	jobID := start["job_id"].(string)
	ai := dialAs(t, "mcp", "claude")
	if _, err := callErr(t, ai, "jobs.output", map[string]any{"job_id": jobID}); err != nil && err.Code == wire.CodePermissionDenied {
		t.Fatalf("jobs.output on an unlimited host: %+v", err)
	}
	updateHost(t, e, map[string]any{"ai_scope": map[string]any{"clipboard": false}})
	for _, m := range []string{"jobs.output", "jobs.get"} {
		_, err := callErr(t, ai, m, map[string]any{"job_id": jobID})
		denied(t, m, err)
	}
	list := mustCall(t, ai, "jobs.list", map[string]any{"host_id": "pc"})["jobs"].([]any)
	if len(list) != 0 {
		t.Fatalf("AI jobs.list shows the limited host's jobs: %v", list)
	}
	if got := mustCall(t, e.c, "jobs.output", map[string]any{"job_id": jobID}); got == nil {
		t.Fatal("a person's jobs.output")
	}
}

func TestAITransferByIDFollowsTheFilesScope(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	mac := t.TempDir()
	src := filepath.Join(mac, "a.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := e.call(t, "transfer.start", map[string]any{"direction": "push", "host_id": "pc", "local": src, "remote": filepath.Join(e.agent.home, "a.txt")})
	id := res["transfer_id"].(string)
	waitFor(t, "the transfer to end", func() bool {
		return e.call(t, "transfer.get", map[string]any{"transfer_id": id})["state"] == "done"
	})
	ai := dialAs(t, "mcp", "claude")
	mustCall(t, ai, "transfer.get", map[string]any{"transfer_id": id})
	updateHost(t, e, map[string]any{"ai_scope": map[string]any{"files": "work_roots", "work_roots": []string{e.agent.home}}})
	for _, m := range []string{"transfer.get", "transfer.resume"} {
		_, err := callErr(t, ai, m, map[string]any{"transfer_id": id})
		denied(t, m, err)
	}
	_, err := callErr(t, ai, "transfer.list", map[string]any{"host_id": "pc"})
	denied(t, "transfer.list of the host", err)
	if list := mustCall(t, ai, "transfer.list", nil)["transfers"].([]any); len(list) != 0 {
		t.Fatalf("AI transfer.list shows the limited host's transfers: %v", list)
	}
	_, err = callErr(t, ai, "transfer.start", map[string]any{"host_id": "pc", "direction": "upload", "local": src, "remote": filepath.Join(e.agent.home, "work", "b.txt")})
	denied(t, "transfer.start inside a work root", err)
	if list := mustCall(t, e.c, "transfer.list", nil)["transfers"].([]any); len(list) != 1 {
		t.Fatalf("a person's transfer.list %v", list)
	}
}

func TestScreenScopeOffClosesLiveAIScreenStream(t *testing.T) {
	agent := newScreenAgent()
	e := setup(t, setupOpts{trustDaemon: true, agentStreams: agent.streams()})
	e.waitState(t, "ready")
	ai, err := attachScreenAs(t, "mcp", map[string]any{"host_id": "pc", "display": 0})
	if err != nil {
		t.Fatalf("AI stream.attach: %v", err)
	}
	human, err := attachScreenAs(t, "app", map[string]any{"host_id": "pc", "display": 0})
	if err != nil {
		t.Fatalf("a person's stream.attach: %v", err)
	}
	aiFrames, humanFrames := screenFrames(ai), screenFrames(human)
	nextFrame(t, aiFrames, wire.KindVideoInfo, nil)
	updateHost(t, e, map[string]any{"ai_scope": map[string]any{"screen": false}})
	closed := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && !closed; {
		select {
		case _, ok := <-aiFrames:
			closed = !ok
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !closed {
		t.Fatal("the AI screen stream stayed open after screen was turned off")
	}
	select {
	case _, ok := <-humanFrames:
		if !ok {
			t.Fatal("a person's stream was closed")
		}
	default:
	}
}
