package input

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// fakeSystem records the inputs it is asked to send.
type fakeSystem struct {
	mu    sync.Mutex
	calls [][]raw
	// accept returns how many inputs of the call it injects; nil injects
	// all.
	accept func(call int, in []raw) int
	// hook runs at the start of every send, outside mu.
	hook  func(call int, in []raw)
	vs    rect
	above bool
	clip  *fakeClipboard
	// drains counts the drain calls and drainErr answers them.
	drains   int
	drainErr func(call int) error
}

var errFakeShort = errors.New("fake: input refused")

func (f *fakeSystem) send(in []raw) (int, error) {
	f.mu.Lock()
	call := len(f.calls)
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		hook(call, in)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]raw(nil), in...))
	n := len(in)
	if f.accept != nil {
		n = f.accept(call, in)
	}
	if n < len(in) {
		return n, errFakeShort
	}
	return n, nil
}

func (f *fakeSystem) virtualScreen() rect { return f.vs }

// keyScan maps a virtual key to 0x100 + its value, extended for the
// navigation keys.
func (f *fakeSystem) keyScan(vk uint16) (uint16, bool) {
	return 0x100 + vk, vk >= 0x21 && vk <= 0x28
}

func (f *fakeSystem) foregroundAbove() bool { return f.above }

func (f *fakeSystem) drain(ctx context.Context, _ time.Duration) error {
	f.mu.Lock()
	call := f.drains
	f.drains++
	answer := f.drainErr
	f.mu.Unlock()
	if ctx.Err() != nil {
		return nil
	}
	if answer != nil {
		return answer(call)
	}
	return nil
}

func (f *fakeSystem) drainCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.drains
}

func (f *fakeSystem) clipboard() clipboardAccess {
	if f.clip == nil {
		return nil
	}
	return f.clip
}

// sent returns the inputs of every call in order.
func (f *fakeSystem) sent() [][]raw {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]raw, len(f.calls))
	copy(out, f.calls)
	return out
}

// all returns the inputs of every call as one list.
func (f *fakeSystem) all() []raw {
	var out []raw
	for _, c := range f.sent() {
		out = append(out, c...)
	}
	return out
}

// fakeClipboard is a clipboard that records what is done to it in log,
// which it shares with the system's sends through a test's hook.
type fakeClipboard struct {
	mu   sync.Mutex
	text string
	log  []string
}

func (c *fakeClipboard) record(s string) {
	c.mu.Lock()
	c.log = append(c.log, s)
	c.mu.Unlock()
}

func (c *fakeClipboard) current() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text
}

func (c *fakeClipboard) save() (func() error, error) {
	c.mu.Lock()
	old := c.text
	c.log = append(c.log, "save "+old)
	c.mu.Unlock()
	return func() error {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.text = old
		c.log = append(c.log, "restore "+old)
		return nil
	}, nil
}

func (c *fakeClipboard) setText(s string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.text = s
	c.log = append(c.log, "set "+s)
	return nil
}

func (c *fakeClipboard) entries() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.log...)
}

var (
	userSession = session.Info{SessionID: 1, ConnectState: session.ConnectActive, Protocol: session.ProtocolConsole,
		InputDesktop: session.DesktopDefault, Integrity: "high"}
	oneDisplay = []display.Info{{Index: 0, Name: `\\.\DISPLAY1`, Width: 1920, Height: 1080, DPI: 96, Scale: 1, Primary: true}}
)

// newTestInjector returns an injector over sys in the session info with
// displays.
func newTestInjector(t *testing.T, sys *fakeSystem, info session.Info, displays []display.Info, hooks Options) *Injector {
	t.Helper()
	if sys.vs.w == 0 {
		sys.vs = rect{w: 1920, h: 1080}
	}
	hooks.SessionInfo = func() (session.Info, error) { return info, nil }
	hooks.InputDesktop = func() string { return info.InputDesktop }
	hooks.Displays = func() ([]display.Info, error) { return displays, nil }
	hooks.Logger = log.New(io.Discard, "", 0)
	j := newInjector(sys, hooks)
	j.pasteDelay = 10 * time.Millisecond
	return j
}

func keyDown(seq, gen uint64, sc uint16) wire.InputEvent {
	return wire.InputEvent{Seq: seq, Gen: gen, K: "key", SC: sc, Down: true}
}

func keyRaw(sc uint16, up bool) raw {
	r := raw{scan: sc, flags: keyeventfScancode}
	if up {
		r.flags |= keyeventfKeyUp
	}
	return r
}

func unicodeRaw(unit uint16, up bool) raw {
	r := raw{scan: unit, flags: keyeventfUnicode}
	if up {
		r.flags |= keyeventfKeyUp
	}
	return r
}

func mustOK(t *testing.T, ack wire.InputAck) {
	t.Helper()
	if !ack.OK || ack.Code != "" {
		t.Fatalf("ack %+v", ack)
	}
}

func mustFail(t *testing.T, ack wire.InputAck, code string) {
	t.Helper()
	if ack.OK || ack.Code != code || ack.Error == "" {
		t.Fatalf("ack %+v, want code %s with an error", ack, code)
	}
}

// streamClient is the daemon's end of an input stream served on a pipe.
type streamClient struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
	done chan struct{}
}

// serveTestStream serves an input stream with header gen on a pipe and
// returns the daemon's end with the stream's first line read into first.
func serveTestStream(t *testing.T, j *Injector, gen uint64) (*streamClient, map[string]any) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	c := &streamClient{t: t, conn: a, r: bufio.NewReader(a), done: make(chan struct{})}
	go func() {
		defer close(c.done)
		j.ServeStream(t.Context(), b, wire.StreamHeader{S: "input", Gen: gen})
	}()
	_ = a.SetDeadline(time.Now().Add(10 * time.Second))
	var first map[string]any
	if err := wire.ReadHeader(c.r, &first); err != nil {
		t.Fatalf("first line: %v", err)
	}
	return c, first
}

func (c *streamClient) send(ev wire.InputEvent) {
	c.t.Helper()
	b, err := json.Marshal(ev)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := wire.WriteFrame(c.conn, wire.KindClientInput, b); err != nil {
		c.t.Fatal(err)
	}
}

func (c *streamClient) ack() wire.InputAck {
	c.t.Helper()
	kind, payload, err := wire.ReadFrame(c.r, 1<<20)
	if err != nil {
		c.t.Fatalf("ack: %v", err)
	}
	if kind != wire.KindInputAck {
		c.t.Fatalf("frame kind %#x, want %#x", kind, wire.KindInputAck)
	}
	var a wire.InputAck
	if err := json.Unmarshal(payload, &a); err != nil {
		c.t.Fatal(err)
	}
	return a
}

// closeAndWait closes the daemon's end and waits for the stream to end.
func (c *streamClient) closeAndWait() {
	c.t.Helper()
	c.conn.Close()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		c.t.Fatal("the stream did not end after its peer closed")
	}
}
