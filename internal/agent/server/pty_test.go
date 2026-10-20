package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// ptyWait bounds the waits of the pty tests for output of a shell.
const ptyWait = 30 * time.Second

// pipeStream is the agent's end of a pty stream: it reads what the daemon
// writes and writes to the daemon.
type pipeStream struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (p pipeStream) Read(b []byte) (int, error)       { return p.r.Read(b) }
func (p pipeStream) Write(b []byte) (int, error)      { return p.w.Write(b) }
func (p pipeStream) SetReadDeadline(time.Time) error  { return nil }
func (p pipeStream) SetWriteDeadline(time.Time) error { return nil }
func (p pipeStream) Close() error                     { return p.w.Close() }
func (p pipeStream) CancelRead()                      { p.r.CloseWithError(errors.New("stream read cancelled")) }
func (p pipeStream) CancelWrite()                     { p.w.CloseWithError(errors.New("stream reset")) }

// peerFrame is one frame the agent wrote.
type peerFrame struct {
	kind    byte
	payload []byte
}

// ptyPeer is the daemon's end of one pty stream that ServePTY serves.
type ptyPeer struct {
	t       *testing.T
	toAgent *io.PipeWriter
	served  chan struct{}
	accept  proto.Message
	frames  chan peerFrame
	// ended is closed when the agent's direction ended.
	ended chan struct{}

	mu       sync.Mutex
	out      bytes.Buffer
	attached *wire.PTYAttached
	exit     *wire.PTYExit
}

// servePTY serves a pty stream with header h on s and reads the agent's
// first line; after an acceptance it waits for the attached frame.
func servePTY(t *testing.T, s *Server, h wire.StreamHeader) *ptyPeer {
	t.Helper()
	h.S = "pty"
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	p := &ptyPeer{t: t, toAgent: inW, served: make(chan struct{}), frames: make(chan peerFrame, 4096), ended: make(chan struct{})}
	go func() {
		defer close(p.served)
		s.ServePTY(context.Background(), pipeStream{r: inR, w: outW}, h)
	}()
	t.Cleanup(func() {
		inW.Close()
		outR.Close()
		select {
		case <-p.served:
		case <-time.After(10 * time.Second):
			t.Error("ServePTY did not return after the stream closed")
		}
	})
	br := bufio.NewReader(outR)
	line, err := br.ReadBytes('\n')
	if err != nil {
		t.Fatalf("the pty stream carried no first line: %v", err)
	}
	if err := json.Unmarshal(line, &p.accept); err != nil {
		t.Fatalf("first line %q: %v", line, err)
	}
	if p.accept["ok"] != true {
		return p
	}
	go func() {
		defer close(p.ended)
		for {
			kind, payload, err := wire.ReadFrame(br, wire.MaxFrame)
			if err != nil {
				return
			}
			p.frames <- peerFrame{kind, payload}
		}
	}()
	f := p.next(ptyWait)
	if f.kind != wire.KindPTYAttached {
		t.Fatalf("the first frame is %#x, not attached", f.kind)
	}
	var a wire.PTYAttached
	if err := json.Unmarshal(f.payload, &a); err != nil {
		t.Fatalf("attached frame %q: %v", f.payload, err)
	}
	p.attached = &a
	return p
}

// next returns the next frame, failing the test when none comes within d.
func (p *ptyPeer) next(d time.Duration) peerFrame {
	p.t.Helper()
	select {
	case f := <-p.frames:
		return f
	case <-time.After(d):
		p.t.Fatalf("no frame within %s", d)
	}
	return peerFrame{}
}

// take applies a data or exit frame.
func (p *ptyPeer) take(f peerFrame) {
	p.t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	switch f.kind {
	case wire.KindPTYData:
		p.out.Write(f.payload)
	case wire.KindPTYExit:
		var e wire.PTYExit
		if err := json.Unmarshal(f.payload, &e); err != nil {
			p.t.Fatalf("exit frame %q: %v", f.payload, err)
		}
		p.exit = &e
	default:
		p.t.Fatalf("unexpected frame %#x after the attached frame", f.kind)
	}
}

// output returns the data received so far.
func (p *ptyPeer) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.String()
}

// contains reports whether the data received from byte from on holds want.
func (p *ptyPeer) contains(want string, from int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return bytes.Contains(p.out.Bytes()[min(from, p.out.Len()):], []byte(want))
}

// waitOutput reads frames until the data received contains want.
func (p *ptyPeer) waitOutput(want string) string {
	p.t.Helper()
	deadline := time.After(ptyWait)
	from := 0
	for !p.contains(want, from) {
		p.mu.Lock()
		from = max(0, p.out.Len()-len(want))
		p.mu.Unlock()
		select {
		case f := <-p.frames:
			p.take(f)
		case <-p.ended:
			p.t.Fatalf("the stream ended before %q arrived; got %q", want, p.output())
		case <-deadline:
			p.t.Fatalf("%q did not arrive within %s; got %q", want, ptyWait, p.output())
		}
	}
	return p.output()
}

// waitExit reads frames until the exit frame and the end of the stream.
func (p *ptyPeer) waitExit() int {
	p.t.Helper()
	deadline := time.After(ptyWait)
	for {
		select {
		case f := <-p.frames:
			p.take(f)
			continue
		case <-p.ended:
		case <-deadline:
			p.t.Fatalf("no exit within %s; got %q", ptyWait, p.output())
		}
		break
	}
	for {
		select {
		case f := <-p.frames:
			p.take(f)
			continue
		default:
		}
		break
	}
	if p.exit == nil {
		p.t.Fatalf("the stream ended without an exit frame; got %q", p.output())
	}
	return p.exit.Code
}

// send writes a frame to the agent.
func (p *ptyPeer) send(kind byte, payload []byte) {
	p.t.Helper()
	if err := wire.WriteFrame(p.toAgent, kind, payload); err != nil {
		p.t.Fatalf("write frame %#x: %v", kind, err)
	}
}

// input types text into the shell.
func (p *ptyPeer) input(text string) { p.send(wire.KindPTYData, []byte(text)) }

// resize sends a resize frame.
func (p *ptyPeer) resize(cols, rows int) {
	b, _ := json.Marshal(wire.PTYResize{Cols: cols, Rows: rows})
	p.send(wire.KindPTYResize, b)
}

// detach ends the daemon's direction and waits for ServePTY to return.
func (p *ptyPeer) detach() {
	p.t.Helper()
	p.toAgent.Close()
	select {
	case <-p.served:
	case <-time.After(10 * time.Second):
		p.t.Fatal("ServePTY did not return after the daemon ended its direction")
	}
}

// listedPTY returns the pty.list entry of job.
func listedPTY(t *testing.T, c *rpcClient, job string) proto.Message {
	t.Helper()
	list, _ := c.ok("pty.list", nil)["ptys"].([]any)
	for _, item := range list {
		if m, ok := item.(map[string]any); ok && m["job"] == job {
			return proto.Message(m)
		}
	}
	t.Fatalf("pty.list has no %s: %v", job, list)
	return nil
}

// newPTY starts a shell on s, attached to the returned peer; the shell is
// ended when the test ends.
func newPTY(t *testing.T, s *Server, n wire.PTYNew) *ptyPeer {
	t.Helper()
	p := servePTY(t, s, wire.StreamHeader{New: &n})
	if p.accept["ok"] != true {
		t.Fatalf("the agent refused the new pty: %v", p.accept)
	}
	if sess := s.ptys.get(p.attached.Job); sess != nil {
		t.Cleanup(func() {
			if _, err := s.killPTY(sess); err != nil {
				t.Errorf("end pty %s: %v", sess.id, err)
			}
		})
	}
	if p.accept["job"] != p.attached.Job || p.accept["pid"] != float64(p.attached.PID) || p.attached.PID <= 0 {
		t.Fatalf("acceptance %v and attached frame %+v", p.accept, *p.attached)
	}
	return p
}

func TestPTYReattachKeepsProcess(t *testing.T) {
	c, s := newRPCClient(t, Options{})
	first := newPTY(t, s, wire.PTYNew{Shell: testShell, Cols: 80, Rows: 24, RID: "r-pty-test"})
	first.input(sumLine(40, 2))
	first.waitOutput(sumText(40, 2))
	first.detach()

	listed := listedPTY(t, c, first.attached.Job)
	if listed.String("state") != "running" || listed.Int("pid") != first.attached.PID || listed.String("shell") != testShell ||
		listed.String("rid") != "r-pty-test" {
		t.Fatalf("pty.list after the detach %v", listed)
	}

	again := servePTY(t, s, wire.StreamHeader{Job: first.attached.Job})
	if again.accept["ok"] != true || *again.attached != *first.attached {
		t.Fatalf("reattach accepted %v with %+v, first %+v", again.accept, again.attached, *first.attached)
	}
	again.waitOutput(sumText(40, 2))
	again.input(sumLine(50, 8))
	again.waitOutput(sumText(50, 8))

	c.ok("exec.kill", proto.Message{"job": first.attached.Job})
	again.waitExit()
	if listed := listedPTY(t, c, first.attached.Job); listed.String("state") != "exited" {
		t.Fatalf("pty.list after exec.kill %v", listed)
	}
	gone := servePTY(t, s, wire.StreamHeader{Job: first.attached.Job})
	if gone.accept["ok"] != false || gone.accept["code"] != wire.CodeNotFound {
		t.Fatalf("attach to the ended pty %v", gone.accept)
	}
}

func TestResizeApplies(t *testing.T) {
	c, s := newRPCClient(t, Options{})
	p := newPTY(t, s, wire.PTYNew{Shell: sizeShell, Cols: 80, Rows: 24})
	p.waitOutput(sizePrompt)
	p.resize(100, 30)
	p.input(sizeLine)
	p.waitOutput(sizeText(100, 30))
	if listed := listedPTY(t, c, p.attached.Job); listed.Int("cols") != 100 || listed.Int("rows") != 30 {
		t.Fatalf("pty.list after the resize %v", listed)
	}
}

func TestPTYExitSendsTheCodeAndAnEvent(t *testing.T) {
	c, s := newRPCClient(t, Options{})
	p := newPTY(t, s, wire.PTYNew{Shell: testShell})
	p.input(exitLine(3))
	if code := p.waitExit(); code != 3 {
		t.Fatalf("exit frame code %d, want 3", code)
	}
	ev := c.event("pty.exited")
	if ev.String("job") != p.attached.Job || ev.Int("code") != 3 {
		t.Fatalf("pty.exited %v", ev)
	}
	if listed := listedPTY(t, c, p.attached.Job); listed.String("state") != "exited" || listed.Int("code") != 3 {
		t.Fatalf("pty.list after the exit %v", listed)
	}
}

func TestTwoAttachmentsOfAPTYShareItsOutput(t *testing.T) {
	_, s := newRPCClient(t, Options{})
	a := newPTY(t, s, wire.PTYNew{Shell: testShell})
	b := servePTY(t, s, wire.StreamHeader{Job: a.attached.Job})
	if b.accept["ok"] != true {
		t.Fatalf("second attach %v", b.accept)
	}
	b.input(sumLine(30, 3))
	a.waitOutput(sumText(30, 3))
	b.waitOutput(sumText(30, 3))
	b.detach()
	a.input(sumLine(60, 6))
	a.waitOutput(sumText(60, 6))
}

func TestPTYStreamsRefuseBadHeaders(t *testing.T) {
	_, s := newRPCClient(t, Options{})
	for _, c := range []struct {
		name string
		h    wire.StreamHeader
		code string
	}{
		{"neither", wire.StreamHeader{}, wire.CodeInvalidArgument},
		{"both", wire.StreamHeader{Job: "p-1", New: &wire.PTYNew{}}, wire.CodeInvalidArgument},
		{"unknown job", wire.StreamHeader{Job: "p-missing"}, wire.CodeNotFound},
		{"unknown shell", wire.StreamHeader{New: &wire.PTYNew{Shell: "fish-like"}}, wire.CodeInvalidArgument},
		{"too wide", wire.StreamHeader{New: &wire.PTYNew{Cols: 5000, Rows: 24}}, wire.CodeInvalidArgument},
		{"negative rows", wire.StreamHeader{New: &wire.PTYNew{Cols: 80, Rows: -1}}, wire.CodeInvalidArgument},
		{"bad rid", wire.StreamHeader{New: &wire.PTYNew{RID: "a b"}}, wire.CodeInvalidArgument},
	} {
		p := servePTY(t, s, c.h)
		if p.accept["ok"] != false || p.accept["code"] != c.code || p.accept["error"] == "" {
			t.Fatalf("%s: %v, want %s", c.name, p.accept, c.code)
		}
	}
	off := New(Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir(), Logger: log.New(io.Discard, "", 0)})
	if p := servePTY(t, off, wire.StreamHeader{New: &wire.PTYNew{}}); p.accept["ok"] != false || p.accept["code"] != wire.CodeCapabilityUnsupported {
		t.Fatalf("pty of an agent without exec %v", p.accept)
	}
}

func TestPTYCapabilityListsTheShells(t *testing.T) {
	s := New(Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir(), AllowExec: true, Logger: log.New(io.Discard, "", 0)})
	caps := s.checkCaps()
	if caps["pty"].State != capReady || len(caps["pty"].Shells) == 0 || caps["pty"].Shells[0] != defaultTestShell {
		t.Fatalf("pty capability %+v", caps["pty"])
	}
	for _, sh := range caps["pty"].Shells {
		if !knownShell(sh) {
			t.Fatalf("pty capability lists %q", sh)
		}
	}
	off := New(Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir(), Logger: log.New(io.Discard, "", 0)})
	if c := off.checkCaps()["pty"]; c.State != capUnavailable || len(c.Shells) != 0 {
		t.Fatalf("pty capability without exec %+v", c)
	}
}

func TestReplayStartsAtTheFirstWholeLineAfterTheScrollbackLostItsStart(t *testing.T) {
	for _, c := range []struct {
		data    string
		dropped bool
		want    string
	}{
		{"abc\r\ndef", false, "abc\r\ndef"},
		{"bc\r\ndef", true, "def"},
		{"\x95\x9c글\r\n끝", true, "끝"},
		{"\x95\x9cno line end", true, "no line end"},
		{strings.Repeat("x", ptyReplayAlign) + "\nlate", true, strings.Repeat("x", ptyReplayAlign) + "\nlate"},
	} {
		if got := string(replayable([]byte(c.data), c.dropped)); got != c.want {
			t.Fatalf("replayable(%q, %v) = %q, want %q", c.data, c.dropped, got, c.want)
		}
	}
}
