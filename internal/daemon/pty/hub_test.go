package pty_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"math/rand/v2"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/config"
	"github.com/YangTaeyoung/deskhand/internal/agent/quicserve"
	"github.com/YangTaeyoung/deskhand/internal/agent/server"
	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/jobs"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/pty"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/transport"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

var quiet = log.New(io.Discard, "", 0)

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

func newID(t *testing.T, cn string) transport.Identity {
	t.Helper()
	id, err := transport.NewIdentity(cn)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// startAgent serves an agent with the put, get and pty streams in this
// process until the test ends.
func startAgent(t *testing.T, port int, id transport.Identity, controller string) {
	t.Helper()
	cfg := config.Agent{Listen: fmt.Sprintf("127.0.0.1:%d", port), ControllerFingerprints: []string{controller}, DataDir: t.TempDir(), Name: "pc"}
	srv := server.New(server.Options{Clipboard: fakeClipboard{}, HomeDir: t.TempDir(), AllowExec: true, DataDir: cfg.DataDir, Version: "test", Logger: quiet})
	streams := quicserve.TransferStreams(srv)
	maps.Copy(streams, quicserve.PTYStreams(srv))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- quicserve.Run(ctx, cfg, id, srv, streams) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("agent did not stop")
		}
		srv.Close()
	})
}

// rig is a hub connected to an in-process agent as the host pc.
type rig struct {
	hub  *pty.Hub
	jobs *jobs.Registry
	host *conn.Host
}

func newRig(t *testing.T) rig {
	t.Helper()
	ctl, agentID := newID(t, "controller"), newID(t, "agent")
	port := freeUDPPort(t)
	startAgent(t, port, agentID, ctl.Fingerprint)
	st, err := store.Open(filepath.Join(t.TempDir(), "deskhand.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bus := events.NewBus(st, quiet)
	var hub *pty.Hub
	var jreg *jobs.Registry
	mgr := conn.NewManager(conn.Options{Identity: ctl, Controller: "test", Requests: st, Logger: quiet, ReadyWait: 5 * time.Second,
		OnReady: func(hostID string, _ uint64) { go hub.Refresh(context.Background(), hostID) },
		OnEvent: func(ev conn.Event) {
			if ev.Kind == conn.KindAgentEvent {
				jreg.HandleAgentEvent(ev.HostID, ev.Payload)
			}
		}})
	t.Cleanup(mgr.Close)
	jreg = jobs.New(st, bus, mgr)
	t.Cleanup(jreg.Close)
	hub = pty.New(pty.Options{Store: st, Conns: mgr, Jobs: jreg, Logger: quiet})
	h := mgr.Add(conn.Target{HostID: "pc", Name: "pc", Address: "127.0.0.1", Port: port, Fingerprint: agentID.Fingerprint})
	h.Connect()
	waitReady(t, h)
	return rig{hub: hub, jobs: jreg, host: h}
}

func waitReady(t *testing.T, h *conn.Host) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := h.WaitState(ctx, func(s conn.ConnState) bool { return s == conn.StateReady }); err != nil {
		t.Fatalf("host state %s: %v", h.State(), err)
	}
}

// client is the local API end of a relay.
type client struct {
	t        *testing.T
	c        net.Conn
	frames   chan [2][]byte
	ran      chan struct{}
	attached wire.PTYAttached

	mu   sync.Mutex
	out  bytes.Buffer
	exit *int
}

// run relays r to a new client and reads its attached frame.
func run(t *testing.T, r *pty.Relay) *client {
	t.Helper()
	local, remote := net.Pipe()
	c := &client{t: t, c: local, frames: make(chan [2][]byte, 4096), ran: make(chan struct{})}
	go func() {
		defer close(c.ran)
		r.Run(remote)
	}()
	t.Cleanup(func() {
		local.Close()
		<-c.ran
	})
	go func() {
		defer close(c.frames)
		br := bufio.NewReader(local)
		for {
			kind, payload, err := wire.ReadFrame(br, wire.MaxFrame)
			if err != nil {
				return
			}
			c.frames <- [2][]byte{{kind}, payload}
		}
	}()
	f, ok := <-c.frames
	if !ok || f[0][0] != wire.KindPTYAttached || json.Unmarshal(f[1], &c.attached) != nil {
		t.Fatalf("first frame %v", f)
	}
	return c
}

func (c *client) take(f [2][]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch f[0][0] {
	case wire.KindPTYData:
		c.out.Write(f[1])
	case wire.KindPTYExit:
		var e wire.PTYExit
		_ = json.Unmarshal(f[1], &e)
		c.exit = &e.Code
	}
}

func (c *client) output() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.out.String()
}

func (c *client) waitOutput(want string) {
	c.t.Helper()
	deadline := time.After(30 * time.Second)
	for !strings.Contains(c.output(), want) {
		select {
		case f, ok := <-c.frames:
			if !ok {
				c.t.Fatalf("the relay ended before %q; got %q", want, c.output())
			}
			c.take(f)
		case <-deadline:
			c.t.Fatalf("no %q; got %q", want, c.output())
		}
	}
}

// waitEnd reads frames until the relay ends and returns its exit code, or
// nil without an exit frame.
func (c *client) waitEnd() *int {
	c.t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case f, ok := <-c.frames:
			if !ok {
				<-c.ran
				return c.exit
			}
			c.take(f)
		case <-deadline:
			c.t.Fatalf("the relay did not end; got %q", c.output())
		}
	}
}

func (c *client) send(kind byte, payload []byte) {
	c.t.Helper()
	_ = c.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := wire.WriteFrame(c.c, kind, payload); err != nil {
		c.t.Fatalf("write frame %#x: %v", kind, err)
	}
}

func (c *client) input(text string) { c.send(wire.KindPTYData, []byte(text)) }

func (c *client) resize(cols, rows int) {
	b, _ := json.Marshal(wire.PTYResize{Cols: cols, Rows: rows})
	c.send(wire.KindPTYResize, b)
}

var person = model.Caller{Actor: model.ActorHuman, ClientID: "c-test", ClientName: "test"}

func TestHubRelaysAShellAndReattachesAfterAConnectionDrop(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	opened, err := r.hub.Open(ctx, r.host, pty.OpenParams{HostID: "pc", Shell: testShell, Cols: 80, Rows: 24}, person)
	if err != nil || opened.Status != model.StatusRunning || opened.Err != nil || opened.PTYID == "" || opened.Job.Kind != jobs.KindPTY {
		t.Fatalf("open %+v %v", opened, err)
	}
	var result struct {
		Shell      string `json:"shell"`
		PID        int    `json:"pid"`
		ClientName string `json:"client_name"`
	}
	if json.Unmarshal(opened.Job.Result, &result) != nil || result.Shell != testShell || result.PID <= 0 || result.ClientName != "test" {
		t.Fatalf("job result %s", opened.Job.Result)
	}
	relay, err := r.hub.Attach(ctx, pty.Ref{PTYID: opened.PTYID})
	if err != nil {
		t.Fatal(err)
	}
	first := run(t, relay)
	if first.attached.Job != opened.Job.JobID || first.attached.PID != result.PID {
		t.Fatalf("attached %+v, job %s pid %d", first.attached, opened.Job.JobID, result.PID)
	}
	first.resize(100, 30)
	first.input(markLine(40, 2))
	first.waitOutput(markText(40, 2))
	first.input(sizeLine)
	first.waitOutput(sizeText(100, 30))
	first.input(koreanLine)
	first.waitOutput("한글테스트")
	first.c.Close()
	if code := first.waitEnd(); code != nil {
		t.Fatalf("the detached relay carried exit %d", *code)
	}

	r.host.Disconnect()
	if _, err := r.hub.Attach(ctx, pty.Ref{JobID: opened.Job.JobID}); apierr.CodeOf(err) != wire.CodeOffline {
		t.Fatalf("attach while disconnected: %v", err)
	}
	r.host.Connect()
	waitReady(t, r.host)
	relay, err = r.hub.Attach(ctx, pty.Ref{JobID: opened.Job.JobID})
	if err != nil {
		t.Fatal(err)
	}
	again := run(t, relay)
	if again.attached != first.attached {
		t.Fatalf("attached after the reconnect %+v, first %+v", again.attached, first.attached)
	}
	again.waitOutput("한글테스트")
	again.input(markLine(50, 8))
	again.waitOutput(markText(50, 8))
	again.input(exitLine(3))
	if code := again.waitEnd(); code == nil || *code != 3 {
		t.Fatalf("exit %v", code)
	}
	job, err := r.jobs.Get(ctx, opened.Job.JobID)
	if err != nil || job.Status != model.StatusFailed || job.FinishedAt == "" {
		t.Fatalf("job after the exit %+v %v", job, err)
	}
	if _, err := r.hub.Attach(ctx, pty.Ref{JobID: opened.Job.JobID}); apierr.CodeOf(err) != wire.CodeNotFound {
		t.Fatalf("attach to the ended shell: %v", err)
	}
}

func TestHubCancelEndsTheShellTree(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	opened, err := r.hub.Open(ctx, r.host, pty.OpenParams{HostID: "pc", Shell: testShell}, person)
	if err != nil || opened.Status != model.StatusRunning {
		t.Fatalf("open %+v %v", opened, err)
	}
	relay, err := r.hub.Attach(ctx, pty.Ref{PTYID: opened.PTYID})
	if err != nil {
		t.Fatal(err)
	}
	c := run(t, relay)
	secs := 300 + rand.IntN(600)
	c.input(longChild(secs))
	c.waitOutput(longChildText)
	if n := r.children(ctx, t, secs); n != 1 {
		t.Fatalf("%d processes run %q before the cancel", n, longChildCmd(secs))
	}
	job, err := r.jobs.Cancel(ctx, opened.Job.JobID, nil)
	if err != nil || job.Status != model.StatusCancelled {
		t.Fatalf("cancel %+v %v", job, err)
	}
	if code := c.waitEnd(); code == nil {
		t.Fatal("the cancelled shell's relay carried no exit frame")
	}
	for deadline := time.Now().Add(10 * time.Second); r.children(ctx, t, secs) != 0; time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the shell's child %q still runs after the cancel", longChildCmd(secs))
		}
	}
}

// children counts the processes of the PC whose command line holds
// longChildCmd(secs).
func (r rig) children(ctx context.Context, t *testing.T, secs int) int {
	t.Helper()
	res, err := r.host.Call(ctx, "proc.list", nil, conn.CallOpts{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	procs, _ := res["procs"].([]any)
	for _, p := range procs {
		if m, ok := p.(map[string]any); ok && strings.Contains(fmt.Sprint(m["cmd"]), longChildCmd(secs)) {
			n++
		}
	}
	return n
}
