package daemon_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/config"
	"github.com/YangTaeyoung/deskhand/internal/agent/quicserve"
	"github.com/YangTaeyoung/deskhand/internal/agent/server"
	"github.com/YangTaeyoung/deskhand/internal/cli"
	"github.com/YangTaeyoung/deskhand/internal/client"
	"github.com/YangTaeyoung/deskhand/internal/daemon"
	"github.com/YangTaeyoung/deskhand/internal/daemon/flock"
	"github.com/YangTaeyoung/deskhand/internal/daemon/paths"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/transport"
)

var quiet = log.New(io.Discard, "", 0)

// TestMain runs the daemon when the test binary is started as
// `<binary> daemon run`, the command client.Spawn runs.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "daemon" && os.Args[2] == "run" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
		defer stop()
		if err := daemon.Run(ctx, daemon.Options{Version: "spawned"}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fakeClipboard struct{}

func (fakeClipboard) Available() bool       { return true }
func (fakeClipboard) Text() (string, error) { return "", nil }
func (fakeClipboard) SetText(string) error  { return nil }
func (fakeClipboard) Sequence() uint64      { return 0 }

func freeUDPPort(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

// testAgent is an agent served over QUIC in this process.
type testAgent struct {
	port    int
	id      transport.Identity
	home    string
	dataDir string
	stop    func()
}

// startAgent serves an agent that trusts the controller fingerprints,
// retrying for up to 5 s while the port is still bound.
func startAgent(t *testing.T, port int, id transport.Identity, dataDir string, controllers ...string) *testAgent {
	t.Helper()
	home := t.TempDir()
	cfg := config.Agent{Listen: fmt.Sprintf("127.0.0.1:%d", port), ControllerFingerprints: controllers, DataDir: dataDir, Name: "pc"}
	var (
		srv    *server.Server
		cancel context.CancelFunc
		done   chan error
	)
	for deadline := time.Now().Add(5 * time.Second); ; {
		srv = server.New(server.Options{Clipboard: fakeClipboard{}, HomeDir: home, AllowExec: true, DataDir: dataDir, Version: "test", Logger: quiet})
		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		done = make(chan error, 1)
		go func() { done <- quicserve.Run(ctx, cfg, id, srv, nil) }()
		select {
		case err := <-done:
			cancel()
			srv.Close()
			if time.Now().After(deadline) {
				t.Fatalf("agent: %v", err)
			}
			time.Sleep(50 * time.Millisecond)
			continue
		case <-time.After(100 * time.Millisecond):
		}
		break
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("agent Run returned %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("agent did not stop")
		}
		srv.Close()
	}
	t.Cleanup(stop)
	return &testAgent{port: port, id: id, home: home, dataDir: dataDir, stop: stop}
}

// env is a daemon with one stored host and a client connected to it.
type env struct {
	home  string
	agent *testAgent
	c     *client.Client
}

type setupOpts struct {
	storedFP      func(agent transport.Identity) string
	trustDaemon   bool
	dropAfterSend string
	// jobs are stored before the daemon starts.
	jobs []store.Job
}

func newID(t *testing.T, cn string) transport.Identity {
	t.Helper()
	id, err := transport.NewIdentity(cn)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func setup(t *testing.T, o setupOpts) *env {
	t.Helper()
	home, err := os.MkdirTemp("", "dh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("DESKHAND_HOME", home)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	controller, err := daemon.ControllerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	agentID := newID(t, "agent")
	trusted := []string{controller.Fingerprint}
	if !o.trustDaemon {
		trusted = []string{newID(t, "other").Fingerprint}
	}
	agent := startAgent(t, freeUDPPort(t), agentID, t.TempDir(), trusted...)
	fp := agentID.Fingerprint
	if o.storedFP != nil {
		fp = o.storedFP(agentID)
	}
	st, err := store.Open(paths.DB())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutHost(store.Host{HostID: "pc", Name: "pc", Address: "127.0.0.1", Port: agent.port, AgentFP: fp, OS: "darwin",
		Arch: "arm64", SSHAlias: "pc", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, j := range o.jobs {
		if err := st.CreateJob(j); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()
	d, err := daemon.New(daemon.Options{Version: "test", Logger: quiet, DropAfterSend: o.dropAfterSend})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		d.Serve(ctx)
		close(served)
	}()
	t.Cleanup(func() {
		cancel()
		<-served
	})
	c, err := client.DialOptions(context.Background(), "cli", "itest", client.Options{Start: func(context.Context) error {
		return errors.New("the daemon of the test is not running")
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &env{home: home, agent: agent, c: c}
}

func (e *env) call(t *testing.T, method string, params any) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out map[string]any
	if err := e.c.Call(ctx, method, params, &out); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return out
}

type hostView struct {
	State  string
	Gen    float64
	Reason string
}

func (e *env) host(t *testing.T) hostView {
	t.Helper()
	list := e.call(t, "host.list", nil)["hosts"].([]any)
	if len(list) != 1 {
		t.Fatalf("hosts %v", list)
	}
	h := list[0].(map[string]any)
	reason, _ := h["reason"].(string)
	return hostView{State: h["state"].(string), Gen: h["connection_generation"].(float64), Reason: reason}
}

func (e *env) waitState(t *testing.T, want string) hostView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		h := e.host(t)
		if h.State == want {
			return h
		}
		if time.Now().After(deadline) {
			t.Fatalf("host state %s (%s), want %s", h.State, h.Reason, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

var envelopeKeys = []string{"schema_version", "request_id", "job_id", "host_id", "session_id", "connection_generation", "status",
	"action", "error", "next_action", "artifacts"}

func checkEnvelope(t *testing.T, res map[string]any) {
	t.Helper()
	for _, k := range envelopeKeys {
		if _, ok := res[k]; !ok {
			t.Fatalf("envelope has no %s: %v", k, res)
		}
	}
	if res["schema_version"] != float64(1) {
		t.Fatalf("schema_version %v", res["schema_version"])
	}
}

func TestLocalAPIExecRunEnvelope(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	res := e.call(t, "exec.run", map[string]any{"host_id": "pc", "argv": []string{"echo", "hi"}})
	checkEnvelope(t, res)
	if res["code"] != float64(0) || res["stdout"] != "hi\n" || res["status"] != "succeeded" || res["host_id"] != "pc" ||
		res["connection_generation"] != float64(1) || res["error"] != nil || res["next_action"] != nil {
		t.Fatalf("envelope %v", res)
	}
	action := res["action"].(map[string]any)
	if action["executed"] != true || action["outcome"] != "done" {
		t.Fatalf("action %v", action)
	}
	rid, _ := res["request_id"].(string)
	jobID, _ := res["job_id"].(string)
	if !strings.HasPrefix(rid, "r-") || !strings.HasPrefix(jobID, "j-") {
		t.Fatalf("request_id %v, job_id %v", res["request_id"], res["job_id"])
	}
	if arts, ok := res["artifacts"].([]any); !ok || len(arts) != 0 {
		t.Fatalf("artifacts %v", res["artifacts"])
	}
	job := e.call(t, "jobs.get", map[string]any{"job_id": jobID})
	if job["status"] != "succeeded" || job["request_id"] != rid || job["kind"] != "exec" || job["finished_at"] == nil {
		t.Fatalf("job %v", job)
	}
	failed := e.call(t, "exec.run", map[string]any{"host_id": "pc", "cmd": "echo oops >&2; exit 7", "shell": "sh"})
	if failed["code"] != float64(7) || failed["status"] != "failed" || failed["stderr"] != "oops\n" {
		t.Fatalf("exit 7 envelope %v", failed)
	}
	tl := e.call(t, "timeline.list", map[string]any{"host_id": "pc", "limit": 2})
	entries := tl["entries"].([]any)
	first := entries[0].(map[string]any)
	if len(entries) != 2 || first["kind"] != "exec" || first["outcome"] != "failed" || first["actor"] != "human" ||
		first["client_id"] != e.c.ClientID || first["client_name"] != "itest" || tl["next_cursor"] == nil {
		t.Fatalf("timeline %v", tl)
	}
}

func TestOutcomeSettledThroughRealAgent(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true, dropAfterSend: "exec.run"})
	e.waitState(t, "ready")
	file := filepath.Join(e.agent.home, "lines.txt")
	res := e.call(t, "exec.run", map[string]any{"host_id": "pc", "cmd": "echo line >> lines.txt", "shell": "sh"})
	checkEnvelope(t, res)
	if res["status"] != "succeeded" || res["connection_generation"] != float64(1) {
		t.Fatalf("envelope %v", res)
	}
	b, err := os.ReadFile(file)
	if err != nil || strings.Count(string(b), "\n") != 1 {
		t.Fatalf("lines %q, %v", b, err)
	}
	if h := e.waitState(t, "ready"); h.Gen != 2 {
		t.Fatalf("generation after the drop %v", h.Gen)
	}
	again := e.call(t, "exec.run", map[string]any{"host_id": "pc", "cmd": "echo line >> lines.txt", "shell": "sh"})
	if again["status"] != "succeeded" || again["connection_generation"] != float64(2) {
		t.Fatalf("second envelope %v", again)
	}
}

func TestFingerprintMismatchBlocksRealHost(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true, storedFP: func(transport.Identity) string { return strings.Repeat("0", 64) }})
	h := e.waitState(t, "blocked")
	if h.Reason != "pc의 인증서가 페어링 때와 다릅니다. 재설치했다면 deskhand host repair pc을 실행하세요" {
		t.Fatalf("reason %q", h.Reason)
	}
	res := e.call(t, "exec.run", map[string]any{"host_id": "pc", "argv": []string{"echo", "hi"}})
	errObj, _ := res["error"].(map[string]any)
	if res["status"] != "failed" || errObj["code"] != "AUTH_REQUIRED" || res["next_action"] != "host_repair" {
		t.Fatalf("exec on a blocked host %v", res)
	}
	if res["action"].(map[string]any)["executed"] != false {
		t.Fatalf("action %v", res["action"])
	}
	again := e.call(t, "host.connect", map[string]any{"host_id": "pc"})
	if again["state"] != "blocked" {
		t.Fatalf("host.connect %v", again)
	}
}

func TestAgentDistrustBlocksRealHost(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: false})
	h := e.waitState(t, "blocked")
	if h.Reason != "pc PC가 이 Mac을 더 이상 신뢰하지 않습니다. deskhand host repair pc을 실행하세요" {
		t.Fatalf("reason %q", h.Reason)
	}
}

func TestFilesRoundTrip(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	dir := filepath.Join(e.agent.home, "한글 폴더 😀")
	mk := e.call(t, "files.mkdir", map[string]any{"host_id": "pc", "path": dir})
	checkEnvelope(t, mk)
	if mk["status"] != "succeeded" {
		t.Fatalf("mkdir %v", mk)
	}
	file := filepath.Join(dir, "파일.txt")
	data := base64.StdEncoding.EncodeToString([]byte("안녕\n"))
	if w := e.call(t, "files.write", map[string]any{"host_id": "pc", "path": file, "data": data}); w["status"] != "succeeded" || w["size"] != float64(7) {
		t.Fatalf("write %v", w)
	}
	list := e.call(t, "files.list", map[string]any{"host_id": "pc", "path": dir})
	entries := list["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries %v", entries)
	}
	ent := entries[0].(map[string]any)
	if ent["name"] != "파일.txt" || ent["path"] != file || ent["dir"] != false || ent["size"] != float64(7) || ent["link"] != false {
		t.Fatalf("entry %v", ent)
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000Z", ent["mtime"].(string)); err != nil {
		t.Fatalf("mtime %v", ent["mtime"])
	}
	rd := e.call(t, "files.read", map[string]any{"host_id": "pc", "path": file})
	if got, _ := base64.StdEncoding.DecodeString(rd["data"].(string)); string(got) != "안녕\n" || rd["eof"] != true {
		t.Fatalf("read %v", rd)
	}
	st := e.call(t, "files.stat", map[string]any{"host_id": "pc", "path": file})
	if st["exists"] != true || st["name"] != "파일.txt" || st["size"] != float64(7) {
		t.Fatalf("stat %v", st)
	}
	moved := filepath.Join(dir, "새 이름.txt")
	if r := e.call(t, "files.rename", map[string]any{"host_id": "pc", "from": file, "to": moved}); r["status"] != "succeeded" {
		t.Fatalf("rename %v", r)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var e1 *client.Error
	if err := e.c.Call(ctx, "files.delete", map[string]any{"host_id": "pc", "path": moved}, nil); !errors.As(err, &e1) || e1.Code != "INVALID_ARGUMENT" {
		t.Fatalf("delete without permanent: %v", err)
	}
	if d := e.call(t, "files.delete", map[string]any{"host_id": "pc", "path": moved, "permanent": true}); d["deleted"] != true {
		t.Fatalf("delete %v", d)
	}
	refused := e.call(t, "files.rename", map[string]any{"host_id": "pc", "from": moved, "to": file})
	if refused["status"] != "failed" || refused["error"].(map[string]any)["code"] != "NOT_FOUND" {
		t.Fatalf("rename of a deleted file %v", refused)
	}
}

func TestFilesCopyTrashAndHash(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	src := filepath.Join(e.agent.home, "원본.txt")
	if err := os.WriteFile(src, []byte("copy me"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(e.agent.home, "사본.txt")
	cp := e.call(t, "files.copy", map[string]any{"host_id": "pc", "from": src, "to": dst})
	checkEnvelope(t, cp)
	if cp["status"] != "succeeded" || cp["path"] != dst || cp["copied"] != float64(1) || cp["outcome"] != "written" || cp["request_id"] == nil {
		t.Fatalf("copy %v", cp)
	}
	if b, _ := os.ReadFile(dst); string(b) != "copy me" {
		t.Fatalf("copy content %q", b)
	}
	st, err := store.Open(paths.DB())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if req, err := st.GetRequest(cp["request_id"].(string)); err != nil || req.State != store.RequestDone {
		t.Fatalf("the copy request %+v, %v was not journaled", req, err)
	}
	conflict := e.call(t, "files.copy", map[string]any{"host_id": "pc", "from": src, "to": dst})
	if errObj, _ := conflict["error"].(map[string]any); conflict["status"] != "failed" || errObj["code"] != "TRANSFER_CONFLICT" {
		t.Fatalf("copy onto an existing file %v", conflict)
	}
	renamed := e.call(t, "files.copy", map[string]any{"host_id": "pc", "from": src, "to": dst, "on_conflict": "rename"})
	if renamed["status"] != "succeeded" || renamed["path"] != filepath.Join(e.agent.home, "사본 (1).txt") || renamed["outcome"] != "renamed" {
		t.Fatalf("copy with rename %v", renamed)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ce *client.Error
	for _, params := range []map[string]any{
		{"host_id": "pc", "from": src, "to": dst, "on_conflict": "merge"},
		{"host_id": "pc", "from": src},
		{"host_id": "pc"},
	} {
		if err := e.c.Call(ctx, "files.copy", params, nil); !errors.As(err, &ce) || ce.Code != "INVALID_ARGUMENT" {
			t.Fatalf("files.copy %v: %v", params, err)
		}
	}

	hash := e.call(t, "files.hash", map[string]any{"host_id": "pc", "path": dst})
	checkEnvelope(t, hash)
	sum := sha256.Sum256([]byte("copy me"))
	if hash["status"] != "succeeded" || hash["host_id"] != "pc" || hash["sha256"] != hex.EncodeToString(sum[:]) || hash["size"] != float64(7) ||
		hash["offset"] != float64(0) || hash["length"] != float64(7) {
		t.Fatalf("hash %v", hash)
	}
	part := e.call(t, "files.hash", map[string]any{"host_id": "pc", "path": dst, "offset": 5, "length": 2})
	if sum := sha256.Sum256([]byte("me")); part["sha256"] != hex.EncodeToString(sum[:]) || part["length"] != float64(2) {
		t.Fatalf("hash of a range %v", part)
	}
	gone := e.call(t, "files.hash", map[string]any{"host_id": "pc", "path": filepath.Join(e.agent.home, "gone")})
	if errObj, _ := gone["error"].(map[string]any); gone["status"] != "failed" || errObj["code"] != "NOT_FOUND" || gone["sha256"] != nil {
		t.Fatalf("hash of a missing file %v", gone)
	}
	if err := e.c.Call(ctx, "files.hash", map[string]any{"host_id": "pc", "path": dst, "offset": -1}, nil); !errors.As(err, &ce) || ce.Code != "INVALID_ARGUMENT" {
		t.Fatalf("hash with a negative offset: %v", err)
	}

	trash := e.call(t, "files.trash", map[string]any{"host_id": "pc", "path": dst})
	checkEnvelope(t, trash)
	if errObj, _ := trash["error"].(map[string]any); trash["status"] != "failed" || errObj["code"] != "CAPABILITY_UNSUPPORTED" || trash["next_action"] != nil {
		t.Fatalf("trash outside Windows %v", trash)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("the file the agent could not trash is gone: %v", err)
	}
	if missing := e.call(t, "files.trash", map[string]any{"host_id": "pc", "path": filepath.Join(e.agent.home, "gone")}); missing["status"] != "succeeded" || missing["trashed"] != false {
		t.Fatalf("trash of a missing path %v", missing)
	}

	entries := e.call(t, "timeline.list", map[string]any{"host_id": "pc"})["entries"].([]any)
	seen := map[string]string{}
	for _, en := range entries {
		m := en.(map[string]any)
		if m["kind"] == "file" {
			seen[m["summary"].(string)] = m["outcome"].(string)
		}
	}
	if seen["복사: "+src+" → "+dst] == "" || seen["휴지통으로 옮기기: "+dst] != "failed" {
		t.Fatalf("timeline %v", seen)
	}
}

func TestExecStartJobOutput(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	evs, err := e.c.Subscribe(ctx, []string{"job.updated"})
	if err != nil {
		t.Fatal(err)
	}
	res := e.call(t, "exec.start", map[string]any{"host_id": "pc", "cmd": "echo a; sleep 0.3; echo b; exit 3", "shell": "sh"})
	checkEnvelope(t, res)
	jobID := res["job_id"].(string)
	if res["status"] != "running" || res["pid"] == float64(0) {
		t.Fatalf("start %v", res)
	}
	var out strings.Builder
	cursor := ""
	var last map[string]any
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		last = e.call(t, "jobs.output", map[string]any{"job_id": jobID, "cursor": cursor})
		for _, c := range last["chunks"].([]any) {
			out.WriteString(c.(map[string]any)["data"].(string))
		}
		cursor = last["next_cursor"].(string)
		if last["state"] == "exited" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if out.String() != "a\nb\n" || last["code"] != float64(3) || last["status"] != "failed" {
		t.Fatalf("output %q, last %v", out.String(), last)
	}
	job := e.call(t, "jobs.get", map[string]any{"job_id": jobID})
	if job["status"] != "failed" || job["finished_at"] == nil {
		t.Fatalf("job %v", job)
	}
	for {
		select {
		case ev := <-evs:
			j, _ := ev.Params["job"].(map[string]any)
			if ev.Kind == "job.updated" && j["job_id"] == jobID && j["status"] == "failed" {
				return
			}
		case <-ctx.Done():
			t.Fatal("no job.updated event with the final status")
		}
	}
}

func TestSessionInspectAndEvents(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	in := e.call(t, "session.inspect", map[string]any{"host_id": "pc"})
	for _, k := range []string{"session_id", "user", "connect_state", "protocol", "input_desktop", "owner", "gen", "ai_paused",
		"displays", "caps", "connection_generation"} {
		if _, ok := in[k]; !ok {
			t.Fatalf("session.inspect has no %s: %v", k, in)
		}
	}
	if _, ok := in["locked"]; ok {
		t.Fatalf("session.inspect reports locked though the agent did not: %v", in)
	}
	if in["owner"] != "none" || in["connection_generation"] != float64(1) {
		t.Fatalf("session.inspect %v", in)
	}
	caps := in["caps"].(map[string]any)
	if caps["exec"].(map[string]any)["state"] != "ready" {
		t.Fatalf("caps %v", caps)
	}
	list := e.call(t, "host.list", nil)["hosts"].([]any)[0].(map[string]any)
	for _, k := range []string{"host_id", "name", "os", "arch", "address", "port", "ssh_alias", "state", "caps", "displays", "owner",
		"connection_generation"} {
		if _, ok := list[k]; !ok {
			t.Fatalf("host has no %s: %v", k, list)
		}
	}
	if list["agent_version"] != "test" {
		t.Fatalf("host %v", list)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	evs, err := e.c.SubscribeHost(ctx, "pc", []string{"connection.state"})
	if err != nil {
		t.Fatal(err)
	}
	e.call(t, "host.disconnect", map[string]any{"host_id": "pc"})
	if got := e.call(t, "host.connect", map[string]any{"host_id": "pc"}); got["state"] != "ready" || got["connection_generation"] != float64(2) {
		t.Fatalf("host.connect %v", got)
	}
	var states []string
	for len(states) < 3 {
		select {
		case ev := <-evs:
			if ev.Kind != "connection.state" || ev.HostID != "pc" || ev.Seq == 0 || ev.TS == "" {
				t.Fatalf("event %+v", ev)
			}
			states = append(states, ev.Params["state"].(string))
		case <-ctx.Done():
			t.Fatalf("states %v", states)
		}
	}
	if strings.Join(states, ",") != "offline,connecting,ready" {
		t.Fatalf("states %v", states)
	}
}

func TestAgentRestartReconnects(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	controller, err := daemon.ControllerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	e.agent.stop()
	e.waitState(t, "reconnecting")
	startAgent(t, e.agent.port, e.agent.id, t.TempDir(), controller.Fingerprint)
	if h := e.waitState(t, "ready"); h.Gen != 2 {
		t.Fatalf("generation after the agent restarted %v", h.Gen)
	}
}

func TestSecondDaemonRefused(t *testing.T) {
	setup(t, setupOpts{trustDaemon: true})
	if _, err := daemon.New(daemon.Options{Logger: quiet}); !errors.Is(err, daemon.ErrAlreadyRunning) {
		t.Fatalf("second daemon: %v", err)
	}
	if !client.Running(context.Background(), "") {
		t.Fatal("the first daemon stopped answering")
	}
}

func TestDialSpawnsDaemonAndStopEndsIt(t *testing.T) {
	home, err := os.MkdirTemp("", "dh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("DESKHAND_HOME", home)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t.Cleanup(func() { client.Stop(context.Background()) })
	c, err := client.Dial(ctx, "cli", "spawn-test")
	if err != nil {
		b, _ := os.ReadFile(paths.DaemonLog())
		t.Fatalf("dial: %v; log: %s", err, b)
	}
	if c.DaemonVersion != "spawned" || c.ClientID == "" {
		t.Fatalf("hello from the spawned daemon: %q %q", c.DaemonVersion, c.ClientID)
	}
	c.Close()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start with a running daemon: %v", err)
	}
	if err := client.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if client.Running(ctx, "") {
		t.Fatal("the daemon still answers after Stop")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		l, err := flock.TryLock(paths.Lock())
		if err == nil {
			l.Release()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon lock is still held: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if b, err := os.ReadFile(paths.DaemonLog()); err != nil || !strings.Contains(string(b), "listening on") {
		t.Fatalf("daemon log %q, %v", b, err)
	}
}

func TestSettlementAcrossAgentRestartWithSameDataDir(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	controller, err := daemon.ControllerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(e.agent.home, "run.txt")
	type result struct {
		res map[string]any
		err error
	}
	done := make(chan result, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var out map[string]any
		err := e.c.Call(ctx, "exec.run", map[string]any{"host_id": "pc", "shell": "sh",
			"cmd": fmt.Sprintf("echo start >> '%s'; sleep 5; echo end >> '%s'", file, file)}, &out)
		done <- result{out, err}
	}()
	for deadline := time.Now().Add(10 * time.Second); ; {
		if b, _ := os.ReadFile(file); strings.Contains(string(b), "start") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the command did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.agent.stop()
	time.Sleep(300 * time.Millisecond)
	startAgent(t, e.agent.port, e.agent.id, e.agent.dataDir, controller.Fingerprint)
	var r result
	select {
	case r = <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("exec.run did not return")
	}
	if r.err != nil {
		t.Fatal(r.err)
	}
	checkEnvelope(t, r.res)
	if st := r.res["status"]; st != "failed" && st != "unknown" || r.res["connection_generation"] != float64(1) {
		t.Fatalf("envelope %v", r.res)
	}
	st, err := store.Open(paths.DB())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	req, err := st.GetRequest(r.res["request_id"].(string))
	if err != nil || req.State != store.RequestDone {
		t.Fatalf("request %+v, %v: the outcome was not settled through req.status", req, err)
	}
	if b, _ := os.ReadFile(file); string(b) != "start\n" {
		t.Fatalf("file %q: the command ran again or finished", b)
	}
	if h := e.waitState(t, "ready"); h.Gen != 2 {
		t.Fatalf("generation %v", h.Gen)
	}
}

// bigOutputCmd writes bigOutputBytes bytes of 'a' to stdout.
const (
	bigOutputBytes = 700000
	bigOutputCmd   = "head -c 700000 /dev/zero | tr '\\000' a"
)

func TestBigExecStartOutputIsReadToTheEnd(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	res := e.call(t, "exec.start", map[string]any{"host_id": "pc", "cmd": bigOutputCmd, "shell": "sh"})
	jobID := res["job_id"].(string)
	got, cursor := 0, ""
	for deadline := time.Now().Add(20 * time.Second); cursor != "end"; {
		out := e.call(t, "jobs.output", map[string]any{"job_id": jobID, "cursor": cursor})
		for _, c := range out["chunks"].([]any) {
			got += len(c.(map[string]any)["data"].(string))
		}
		cursor = out["next_cursor"].(string)
		if time.Now().After(deadline) {
			t.Fatalf("no end cursor; read %d bytes, cursor %s", got, cursor)
		}
		if cursor != "end" && len(out["chunks"].([]any)) == 0 {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if got != bigOutputBytes {
		t.Fatalf("read %d bytes, want %d", got, bigOutputBytes)
	}
	if job := e.call(t, "jobs.get", map[string]any{"job_id": jobID}); job["status"] != "succeeded" {
		t.Fatalf("job %v", job)
	}
}

// TestCLIExecFollowsBigOutputToTheEnd polls slower than the command runs, so
// that the job has exited before its output is read.
func TestCLIExecFollowsBigOutputToTheEnd(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	var stdout, stderr strings.Builder
	app := cli.New("test")
	app.Stdin, app.Stdout, app.Stderr = strings.NewReader(""), &stdout, &stderr
	app.Interactive = func() bool { return false }
	app.PollInterval = 300 * time.Millisecond
	code := app.Run(context.Background(), []string{"exec", "pc", "--shell", "none", "--", "sh", "-c", "sleep 0.1; " + bigOutputCmd})
	if code != 0 || stdout.Len() != bigOutputBytes || strings.Trim(stdout.String(), "a") != "" {
		t.Fatalf("exit %d, %d bytes on stdout, stderr %q", code, stdout.Len(), stderr.String())
	}
}

func TestJobPayloadsCarryNoOutput(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	evs, err := e.c.Subscribe(ctx, []string{"job.updated"})
	if err != nil {
		t.Fatal(err)
	}
	res := e.call(t, "exec.run", map[string]any{"host_id": "pc", "cmd": "echo out; echo err >&2", "shell": "sh"})
	if res["stdout"] != "out\n" || res["stderr"] != "err\n" {
		t.Fatalf("exec.run %v", res)
	}
	jobID := res["job_id"].(string)
	noOutput := func(where string, job map[string]any) {
		t.Helper()
		result, _ := job["result"].(map[string]any)
		if _, ok := result["stdout"]; ok {
			t.Fatalf("%s carries stdout: %v", where, job)
		}
		if _, ok := result["stderr"]; ok {
			t.Fatalf("%s carries stderr: %v", where, job)
		}
	}
	got := e.call(t, "jobs.get", map[string]any{"job_id": jobID})
	noOutput("jobs.get", got)
	if got["output_bytes"] != float64(8) || got["result"].(map[string]any)["code"] != float64(0) {
		t.Fatalf("jobs.get %v", got)
	}
	for _, j := range e.call(t, "jobs.list", map[string]any{"host_id": "pc"})["jobs"].([]any) {
		noOutput("jobs.list", j.(map[string]any))
	}
	for done := false; !done; {
		select {
		case ev := <-evs:
			j := ev.Params["job"].(map[string]any)
			noOutput("job.updated", j)
			done = j["job_id"] == jobID && j["status"] == "succeeded"
		case <-ctx.Done():
			t.Fatal("no job.updated with the final status")
		}
	}
	out := e.call(t, "jobs.output", map[string]any{"job_id": jobID})
	var text strings.Builder
	for _, c := range out["chunks"].([]any) {
		text.WriteString(c.(map[string]any)["data"].(string))
	}
	if text.String() != "out\nerr\n" || out["next_cursor"] != "end" {
		t.Fatalf("jobs.output %v", out)
	}
}

func TestJobsLeftRunningAreSettledAtStartAndOnReady(t *testing.T) {
	now := time.Now()
	running := func(id, kind, remote string) store.Job {
		return store.Job{JobID: id, HostID: "pc", Kind: kind, Status: "running", Summary: id, RemoteJob: remote, CreatedAt: now, UpdatedAt: now}
	}
	e := setup(t, setupOpts{trustDaemon: true, jobs: []store.Job{running("j-run", "exec", ""), running("j-install", "install", ""),
		running("j-remote", "exec", "j77")}})
	for _, id := range []string{"j-run", "j-install"} {
		if j := e.call(t, "jobs.get", map[string]any{"job_id": id}); j["status"] != "unknown" || j["finished_at"] == nil {
			t.Fatalf("%s after the start %v", id, j)
		}
	}
	e.waitState(t, "ready")
	for deadline := time.Now().Add(10 * time.Second); ; {
		j := e.call(t, "jobs.get", map[string]any{"job_id": "j-remote"})
		if j["status"] == "unknown" && j["result"].(map[string]any)["state"] == "lost" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the job the agent does not know %v", j)
		}
		time.Sleep(20 * time.Millisecond)
	}
	entries := e.call(t, "timeline.list", map[string]any{"host_id": "pc"})["entries"].([]any)
	seen := map[string]bool{}
	for _, en := range entries {
		m := en.(map[string]any)
		if m["actor"] == "system" && m["outcome"] == "unknown" {
			seen[m["job_id"].(string)] = true
		}
	}
	if !seen["j-run"] || !seen["j-install"] {
		t.Fatalf("timeline %v", entries)
	}
}

func TestExecStartTimeoutEndsTheJobWithTimeoutAndKeepsItsOutput(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	res := e.call(t, "exec.start", map[string]any{"host_id": "pc", "cmd": "echo started; sleep 30", "shell": "sh", "timeout_ms": 400})
	jobID := res["job_id"].(string)
	var job map[string]any
	for deadline := time.Now().Add(15 * time.Second); ; {
		job = e.call(t, "jobs.get", map[string]any{"job_id": jobID})
		if job["status"] != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job still running %v", job)
		}
		time.Sleep(50 * time.Millisecond)
	}
	result := job["result"].(map[string]any)
	if job["status"] != "failed" || result["timed_out"] != true || result["error"].(map[string]any)["code"] != "TIMEOUT" {
		t.Fatalf("job %v", job)
	}
	var text strings.Builder
	var last map[string]any
	for cursor := ""; cursor != "end"; {
		last = e.call(t, "jobs.output", map[string]any{"job_id": jobID, "cursor": cursor})
		for _, c := range last["chunks"].([]any) {
			text.WriteString(c.(map[string]any)["data"].(string))
		}
		cursor = last["next_cursor"].(string)
	}
	if text.String() != "started\n" || last["timed_out"] != true || last["status"] != "failed" {
		t.Fatalf("output %q, last %v", text.String(), last)
	}

	var stdout, stderr strings.Builder
	app := cli.New("test")
	app.Stdin, app.Stdout, app.Stderr = strings.NewReader(""), &stdout, &stderr
	app.Interactive = func() bool { return false }
	app.PollInterval = 20 * time.Millisecond
	start := time.Now()
	code := app.Run(context.Background(), []string{"exec", "pc", "--timeout", "500ms", "--shell", "none", "--", "sh", "-c", "echo hi; sleep 30"})
	if code != 124 || stdout.String() != "hi\n" || !strings.Contains(stderr.String(), "500ms 안에 끝나지 않아 종료했습니다") || time.Since(start) > 10*time.Second {
		t.Fatalf("exit %d after %s, stdout %q, stderr %q", code, time.Since(start), stdout.String(), stderr.String())
	}
}

func TestExecRunCutByTheConnectionSaysItWasInterrupted(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	marker := filepath.Join(e.agent.home, "started")
	type result struct {
		res map[string]any
		err error
	}
	done := make(chan result, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var out map[string]any
		err := e.c.Call(ctx, "exec.run", map[string]any{"host_id": "pc", "shell": "sh", "cmd": "touch '" + marker + "'; sleep 20"}, &out)
		done <- result{out, err}
	}()
	for deadline := time.Now().Add(10 * time.Second); ; {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the command did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.call(t, "host.disconnect", map[string]any{"host_id": "pc"})
	e.call(t, "host.connect", map[string]any{"host_id": "pc"})
	var r result
	select {
	case r = <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("exec.run did not return")
	}
	if r.err != nil {
		t.Fatal(r.err)
	}
	errObj, _ := r.res["error"].(map[string]any)
	if r.res["status"] != "failed" || r.res["killed"] != "connection_closed" || errObj["code"] != "OFFLINE" || r.res["next_action"] != "check_state" {
		t.Fatalf("envelope %v", r.res)
	}
	job := e.call(t, "jobs.get", map[string]any{"job_id": r.res["job_id"]})
	if job["result"].(map[string]any)["killed"] != "connection_closed" {
		t.Fatalf("job %v", job)
	}
}
