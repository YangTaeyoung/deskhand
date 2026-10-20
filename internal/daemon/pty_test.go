package daemon_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/cli"
	"github.com/YangTaeyoung/deskhand/internal/client"
	"github.com/YangTaeyoung/deskhand/internal/daemon"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// ptyFrame is one frame of a pty stream from the daemon.
type ptyFrame struct {
	kind    byte
	payload []byte
}

// ptyStream is a local API stream attached to a pty.
type ptyStream struct {
	t        *testing.T
	s        *client.Stream
	frames   chan ptyFrame
	ended    chan struct{}
	attached wire.PTYAttached

	mu   sync.Mutex
	out  bytes.Buffer
	exit *int
}

// attachPTY attaches a stream with params and reads its attached frame.
func attachPTY(t *testing.T, params map[string]any) (*ptyStream, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := client.Attach(ctx, client.Options{Start: func(context.Context) error { return errors.New("no daemon") }}, "app", "Deskhand", "pty", params)
	if err != nil {
		return nil, err
	}
	p := &ptyStream{t: t, s: s, frames: make(chan ptyFrame, 4096), ended: make(chan struct{})}
	t.Cleanup(func() { s.Close() })
	go func() {
		defer close(p.ended)
		for {
			kind, payload, err := s.ReadFrame(wire.MaxFrame)
			if err != nil {
				return
			}
			p.frames <- ptyFrame{kind, payload}
		}
	}()
	select {
	case f := <-p.frames:
		if f.kind != wire.KindPTYAttached || json.Unmarshal(f.payload, &p.attached) != nil {
			t.Fatalf("first frame %#x %q", f.kind, f.payload)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("no attached frame")
	}
	return p, nil
}

func (p *ptyStream) take(f ptyFrame) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch f.kind {
	case wire.KindPTYData:
		p.out.Write(f.payload)
	case wire.KindPTYExit:
		var e wire.PTYExit
		if err := json.Unmarshal(f.payload, &e); err != nil {
			p.t.Errorf("exit frame %q: %v", f.payload, err)
		}
		p.exit = &e.Code
	default:
		p.t.Errorf("unexpected frame %#x", f.kind)
	}
}

func (p *ptyStream) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.String()
}

// waitOutput reads frames until the output holds want.
func (p *ptyStream) waitOutput(want string) {
	p.t.Helper()
	deadline := time.After(30 * time.Second)
	for !strings.Contains(p.output(), want) {
		select {
		case f := <-p.frames:
			p.take(f)
		case <-p.ended:
			p.t.Fatalf("the stream ended before %q; got %q", want, p.output())
		case <-deadline:
			p.t.Fatalf("no %q; got %q", want, p.output())
		}
	}
}

// waitEnd reads frames until the stream ends and returns the exit code of
// its exit frame, or nil without one.
func (p *ptyStream) waitEnd() *int {
	p.t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case f := <-p.frames:
			p.take(f)
		case <-p.ended:
			for {
				select {
				case f := <-p.frames:
					p.take(f)
				default:
					p.mu.Lock()
					defer p.mu.Unlock()
					return p.exit
				}
			}
		case <-deadline:
			p.t.Fatalf("the stream did not end; got %q", p.output())
		}
	}
}

func (p *ptyStream) send(kind byte, payload []byte) {
	p.t.Helper()
	if err := p.s.WriteFrame(kind, payload); err != nil {
		p.t.Fatalf("write frame %#x: %v", kind, err)
	}
}

func (p *ptyStream) input(text string) { p.send(wire.KindPTYData, []byte(text)) }

func (p *ptyStream) resize(cols, rows int) {
	b, _ := json.Marshal(wire.PTYResize{Cols: cols, Rows: rows})
	p.send(wire.KindPTYResize, b)
}

// runningPTYs returns the job ids of jobs.list{kind:"pty", status:"running"}.
func (e *env) runningPTYs(t *testing.T) []string {
	t.Helper()
	var ids []string
	for _, item := range e.call(t, "jobs.list", map[string]any{"host_id": "pc", "kind": "pty", "status": "running"})["jobs"].([]any) {
		ids = append(ids, item.(map[string]any)["job_id"].(string))
	}
	return ids
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestPTYRelayAndReattach(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	host := e.call(t, "host.list", nil)["hosts"].([]any)[0].(map[string]any)
	if c := host["caps"].(map[string]any)["pty"].(map[string]any); c["state"] != "ready" || c["shells"].([]any)[0] != defaultShell {
		t.Fatalf("caps.pty %v", c)
	}

	res := e.call(t, "pty.open", map[string]any{"host_id": "pc", "shell": testShell, "cols": 80, "rows": 24})
	checkEnvelope(t, res)
	jobID, _ := res["job_id"].(string)
	ptyID, _ := res["pty_id"].(string)
	if res["status"] != "running" || res["error"] != nil || !strings.HasPrefix(jobID, "j-") || !strings.HasPrefix(ptyID, "p-") ||
		res["action"].(map[string]any)["executed"] != true {
		t.Fatalf("pty.open %v", res)
	}
	job := e.call(t, "jobs.get", map[string]any{"job_id": jobID})
	result := job["result"].(map[string]any)
	if job["kind"] != "pty" || job["status"] != "running" || result["shell"] != testShell || result["pid"].(float64) <= 0 || result["client_name"] != "itest" {
		t.Fatalf("pty job %v", job)
	}
	pid := int(result["pid"].(float64))

	first, err := attachPTY(t, map[string]any{"pty_id": ptyID})
	if err != nil {
		t.Fatal(err)
	}
	if first.attached.Job != jobID || first.attached.PID != pid {
		t.Fatalf("attached %+v, job %s pid %d", first.attached, jobID, pid)
	}
	first.resize(100, 30)
	first.input(markLine(40, 2))
	first.waitOutput(markText(40, 2))
	first.input(sizeLine)
	first.waitOutput(sizeText(100, 30))
	first.s.Close()
	if !contains(e.runningPTYs(t), jobID) {
		t.Fatalf("running ptys after the detach %v", e.runningPTYs(t))
	}

	again, err := attachPTY(t, map[string]any{"job_id": jobID})
	if err != nil {
		t.Fatal(err)
	}
	if again.attached != first.attached {
		t.Fatalf("reattached %+v, first %+v", again.attached, first.attached)
	}
	again.waitOutput(markText(40, 2))

	e.call(t, "host.disconnect", map[string]any{"host_id": "pc"})
	if code := again.waitEnd(); code != nil {
		t.Fatalf("the stream of a dropped connection carried exit %d", *code)
	}
	if _, err := attachPTY(t, map[string]any{"job_id": jobID}); apiCode(err) != "OFFLINE" {
		t.Fatalf("attach while disconnected: %v", err)
	}
	e.call(t, "host.connect", map[string]any{"host_id": "pc"})
	e.waitState(t, "ready")
	after, err := attachPTY(t, map[string]any{"job_id": jobID})
	if err != nil {
		t.Fatal(err)
	}
	if after.attached != first.attached {
		t.Fatalf("attached after the reconnect %+v, first %+v", after.attached, first.attached)
	}
	after.waitOutput(markText(40, 2))
	after.input(markLine(50, 8))
	after.waitOutput(markText(50, 8))
	after.input(exitLine(3))
	if code := after.waitEnd(); code == nil || *code != 3 {
		t.Fatalf("exit frame %v", code)
	}
	job = e.call(t, "jobs.get", map[string]any{"job_id": jobID})
	if job["status"] != "failed" || job["finished_at"] == nil || job["result"].(map[string]any)["code"] != float64(3) {
		t.Fatalf("job after the exit %v", job)
	}
	if contains(e.runningPTYs(t), jobID) {
		t.Fatal("the ended pty is still listed as running")
	}
	if _, err := attachPTY(t, map[string]any{"job_id": jobID}); apiCode(err) != "NOT_FOUND" {
		t.Fatalf("attach to the ended pty: %v", err)
	}
	if _, err := attachPTY(t, map[string]any{"pty_id": ptyID}); apiCode(err) != "NOT_FOUND" {
		t.Fatalf("attach to the ended pty by pty_id: %v", err)
	}
}

func TestPTYJobsCancelEndsTheShell(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	res := e.call(t, "pty.open", map[string]any{"host_id": "pc"})
	jobID := res["job_id"].(string)
	if res["status"] != "running" || e.call(t, "jobs.get", map[string]any{"job_id": jobID})["result"].(map[string]any)["shell"] != defaultShell {
		t.Fatalf("pty.open without a shell %v", res)
	}
	s, err := attachPTY(t, map[string]any{"pty_id": res["pty_id"]})
	if err != nil {
		t.Fatal(err)
	}
	s.input(markLine(1, 1))
	s.waitOutput(markText(1, 1))
	cancelled := e.call(t, "jobs.cancel", map[string]any{"job_id": jobID})
	checkEnvelope(t, cancelled)
	if cancelled["status"] != "cancelled" || cancelled["job"].(map[string]any)["status"] != "cancelled" {
		t.Fatalf("jobs.cancel %v", cancelled)
	}
	if code := s.waitEnd(); code == nil {
		t.Fatal("the stream of the cancelled shell carried no exit frame")
	}
	if again := e.call(t, "jobs.cancel", map[string]any{"job_id": jobID}); again["status"] != "succeeded" {
		t.Fatalf("jobs.cancel of the ended pty %v", again)
	}
	if _, err := attachPTY(t, map[string]any{"pty_id": "p-missing"}); apiCode(err) != "NOT_FOUND" {
		t.Fatalf("attach to an unknown pty: %v", err)
	}
	var cerr *client.Error
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.c.Call(ctx, "pty.open", map[string]any{"host_id": "pc", "shell": "fish"}, nil); !errors.As(err, &cerr) || cerr.Code != "INVALID_ARGUMENT" {
		t.Fatalf("pty.open with an unknown shell: %v", err)
	}
	refused := e.call(t, "pty.open", map[string]any{"host_id": "pc", "shell": missingShell})
	checkEnvelope(t, refused)
	if refused["status"] != "failed" || refused["error"].(map[string]any)["code"] != "CAPABILITY_UNSUPPORTED" || refused["pty_id"] != nil ||
		refused["action"].(map[string]any)["outcome"] != "not_started" {
		t.Fatalf("pty.open of a shell the PC lacks %v", refused)
	}
}

// waitJob polls jobs.get until ok accepts the job.
func (e *env) waitJob(t *testing.T, jobID, what string, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		job := e.call(t, "jobs.get", map[string]any{"job_id": jobID})
		if ok(job) {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: job %v", what, job)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPTYExitWhileDetachedFinishesTheJob(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	res := e.call(t, "pty.open", map[string]any{"host_id": "pc", "shell": testShell})
	jobID := res["job_id"].(string)
	s, err := attachPTY(t, map[string]any{"pty_id": res["pty_id"]})
	if err != nil {
		t.Fatal(err)
	}
	s.input(delayedExit(0))
	s.waitOutput("going")
	s.s.Close()
	job := e.waitJob(t, jobID, "the shell's exit after the detach", func(j map[string]any) bool { return j["status"] != "running" })
	if job["status"] != "succeeded" || job["result"].(map[string]any)["code"] != float64(0) {
		t.Fatalf("job after the exit %v", job)
	}
}

func TestPTYExitWhileDisconnectedFinishesTheJobOnReconnect(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	res := e.call(t, "pty.open", map[string]any{"host_id": "pc", "shell": testShell})
	jobID := res["job_id"].(string)
	s, err := attachPTY(t, map[string]any{"pty_id": res["pty_id"]})
	if err != nil {
		t.Fatal(err)
	}
	s.input(delayedExit(4))
	s.waitOutput("going")
	e.call(t, "host.disconnect", map[string]any{"host_id": "pc"})
	s.waitEnd()
	time.Sleep(time.Second)
	if job := e.call(t, "jobs.get", map[string]any{"job_id": jobID}); job["status"] != "running" {
		t.Fatalf("job while disconnected %v", job)
	}
	e.call(t, "host.connect", map[string]any{"host_id": "pc"})
	job := e.waitJob(t, jobID, "the exit found after the reconnect", func(j map[string]any) bool { return j["status"] != "running" })
	if job["status"] != "failed" || job["result"].(map[string]any)["code"] != float64(4) {
		t.Fatalf("job after the reconnect %v", job)
	}
}

func TestPTYJobsOfARestartedAgentEndUnknown(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	controller, err := daemon.ControllerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	jobID := e.call(t, "pty.open", map[string]any{"host_id": "pc", "shell": testShell})["job_id"].(string)
	e.agent.stop()
	time.Sleep(300 * time.Millisecond)
	startAgent(t, e.agent.port, e.agent.id, e.agent.dataDir, controller.Fingerprint)
	job := e.waitJob(t, jobID, "the pty of the restarted agent", func(j map[string]any) bool { return j["status"] != "running" })
	result := job["result"].(map[string]any)
	if job["status"] != "unknown" || result["state"] != "lost" || result["error"].(map[string]any)["code"] != "EXECUTION_OUTCOME_UNKNOWN" {
		t.Fatalf("job of the lost pty %v", job)
	}
	if _, err := attachPTY(t, map[string]any{"job_id": jobID}); apiCode(err) != "NOT_FOUND" {
		t.Fatalf("attach to the lost pty: %v", err)
	}
}

// apiCode returns the code of an *client.Error, or "".
func apiCode(err error) string {
	var e *client.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// fakeTerm is the local terminal of a deskhand shell under test.
type fakeTerm struct {
	mu         sync.Mutex
	cols, rows int
	terminal   bool
	raw        int
	resizes    chan struct{}
}

func (f *fakeTerm) Size() (int, int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cols, f.rows, f.terminal
}

func (f *fakeTerm) Raw() (func(), error) {
	f.mu.Lock()
	f.raw++
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		f.raw--
		f.mu.Unlock()
	}, nil
}

func (f *fakeTerm) Resizes() (<-chan struct{}, func()) { return f.resizes, func() {} }

func (f *fakeTerm) resize(cols, rows int) {
	f.mu.Lock()
	f.cols, f.rows = cols, rows
	f.mu.Unlock()
	f.resizes <- struct{}{}
}

// syncBuffer is a buffer written by one goroutine and read by another.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// shellRun is one deskhand shell invocation.
type shellRun struct {
	t      *testing.T
	in     *io.PipeWriter
	out    *syncBuffer
	errOut *syncBuffer
	code   chan int
}

func startShell(t *testing.T, term cli.Terminal, args ...string) *shellRun {
	t.Helper()
	inR, inW := io.Pipe()
	s := &shellRun{t: t, in: inW, out: &syncBuffer{}, errOut: &syncBuffer{}, code: make(chan int, 1)}
	app := &cli.App{Stdin: inR, Stdout: s.out, Stderr: s.errOut, Version: "test", Interactive: func() bool { return false },
		PollInterval: 20 * time.Millisecond, Progress: func() bool { return false }, Term: term}
	go func() { s.code <- app.Run(context.Background(), append([]string{"shell"}, args...)) }()
	t.Cleanup(func() { inW.Close() })
	return s
}

func (s *shellRun) input(text string) {
	s.t.Helper()
	if _, err := s.in.Write([]byte(text)); err != nil {
		s.t.Fatal(err)
	}
}

func (s *shellRun) waitOutput(want string) {
	s.t.Helper()
	for deadline := time.Now().Add(30 * time.Second); !strings.Contains(s.out.String(), want); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			s.t.Fatalf("no %q; stdout %q, stderr %q", want, s.out.String(), s.errOut.String())
		}
	}
}

func (s *shellRun) exit() int {
	s.t.Helper()
	select {
	case code := <-s.code:
		return code
	case <-time.After(30 * time.Second):
		s.t.Fatalf("deskhand shell did not end; stdout %q, stderr %q", s.out.String(), s.errOut.String())
	}
	return -1
}

func TestShellCommandRelaysTheTerminalAndDetaches(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	term := &fakeTerm{cols: 100, rows: 30, terminal: true, resizes: make(chan struct{}, 1)}
	sh := startShell(t, term, "pc", "--shell", testShell)
	sh.input(sizeLine)
	sh.waitOutput(sizeText(100, 30))
	term.resize(120, 40)
	sh.input(sizeLine)
	sh.waitOutput(sizeText(120, 40))
	sh.input(markLine(2, 3))
	sh.waitOutput(markText(2, 3))
	sh.input("\x1d")
	if code := sh.exit(); code != 0 {
		t.Fatalf("detach exit code %d; stderr %q", code, sh.errOut.String())
	}
	jobs := e.runningPTYs(t)
	if len(jobs) != 1 || !strings.Contains(sh.errOut.String(), "deskhand shell --attach "+jobs[0]) {
		t.Fatalf("running ptys %v after the detach; stderr %q", jobs, sh.errOut.String())
	}
	if term.raw != 0 {
		t.Fatalf("the terminal was left in raw mode (%d)", term.raw)
	}

	back := startShell(t, term, "--attach", jobs[0])
	back.waitOutput(markText(2, 3))
	back.input(exitLine(7))
	if code := back.exit(); code != 7 {
		t.Fatalf("exit code %d, want the shell's 7; stderr %q", code, back.errOut.String())
	}

	plain := startShell(t, &fakeTerm{resizes: make(chan struct{})}, "pc", "--shell", testShell)
	plain.input(markLine(1, 2) + exitLine(0))
	plain.waitOutput(markText(1, 2))
	if code := plain.exit(); code != 0 {
		t.Fatalf("exit code of the shell without a terminal %d; stderr %q", code, plain.errOut.String())
	}

	gone := startShell(t, term, "--attach", jobs[0])
	if code := gone.exit(); code != 1 || !strings.Contains(gone.errOut.String(), "새 터미널") {
		t.Fatalf("attach to the ended shell: code %d, stderr %q", code, gone.errOut.String())
	}
}
