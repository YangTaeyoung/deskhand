package server

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
)

type fakeClipboard struct {
	mu   sync.Mutex
	text string
	seq  uint64
}

func (f *fakeClipboard) Available() bool { return true }
func (f *fakeClipboard) Text() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.text, nil
}
func (f *fakeClipboard) SetText(s string) error { f.external(s); return nil }
func (f *fakeClipboard) Sequence() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seq
}
func (f *fakeClipboard) external(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.text = s
	f.seq++
}

// dialAgent connects to the server at addr, sends the host's welcome and
// bounds the connection to 10 s.
func dialAgent(t *testing.T, addr string) (net.Conn, *proto.Reader, *proto.Writer) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(10 * time.Second))
	w := proto.NewWriter(c)
	send(t, w, proto.Message{"t": "welcome", "v": 1, "controller": "test"})
	return c, proto.NewReader(c), w
}

func startServer(t *testing.T, opt Options) (*proto.Reader, *proto.Writer, net.Conn) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go New(opt).Serve(l)
	c, r, w := dialAgent(t, l.Addr().String())
	return r, w, c
}

func expect(t *testing.T, r *proto.Reader, typ string) proto.Message {
	t.Helper()
	for {
		m, err := r.Next()
		if err != nil {
			t.Fatalf("waiting for %s: %v", typ, err)
		}
		if m.Type() == typ {
			return m
		}
	}
}

func TestTheClipboardWatcherReportsOnlyThePCsChanges(t *testing.T) {
	cb := &fakeClipboard{}
	r, w, _ := startServer(t, Options{Clipboard: cb, HomeDir: t.TempDir()})
	expect(t, r, "hello")
	send(t, w, proto.Message{"t": "req", "id": 1, "op": "clipboard.set", "text": "from host"})
	if res := expect(t, r, "res"); res["ok"] != true {
		t.Fatalf("clipboard.set %v", res)
	}
	time.Sleep(600 * time.Millisecond)
	cb.external("typed on the PC")
	if m := expectEvent(t, r, "clipboard.changed"); m.String("text") != "typed on the PC" {
		t.Fatalf("the first clipboard.changed is %v", m)
	}
}

// slowFirstSequence is a fake clipboard whose first Sequence call takes
// 300 ms before it reads the sequence number.
type slowFirstSequence struct {
	*fakeClipboard
	once sync.Once
}

func (f *slowFirstSequence) Sequence() uint64 {
	f.once.Do(func() { time.Sleep(300 * time.Millisecond) })
	return f.fakeClipboard.Sequence()
}

func TestAClipboardChangeRightAfterHelloIsReported(t *testing.T) {
	cb := &slowFirstSequence{fakeClipboard: &fakeClipboard{}}
	r, _, _ := startServer(t, Options{Clipboard: cb, HomeDir: t.TempDir()})
	expect(t, r, "hello")
	cb.external("copied as the agent connected")
	if m := expectEvent(t, r, "clipboard.changed"); m.String("text") != "copied as the agent connected" {
		t.Fatalf("the first clipboard.changed is %v", m)
	}
}

func TestExecDisabledByDefault(t *testing.T) {
	r, w, _ := startServer(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	expect(t, r, "hello")
	send(t, w, proto.Message{"t": "req", "id": 1, "op": "exec.run", "cmd": "echo hi"})
	if res := expect(t, r, "res"); res["ok"] != false || res.String("error") != errExecDisabled {
		t.Fatalf("exec.run should be disabled: %v", res)
	}
}

func TestAMessageThatIsNotAReqGetsOneErrorLineAndIsNotRun(t *testing.T) {
	home := t.TempDir()
	cb := &fakeClipboard{text: "kept"}
	r, w, _ := startServer(t, Options{Clipboard: cb, HomeDir: home, AllowExec: true})
	expect(t, r, "hello")
	marker := filepath.Join(home, "ran.txt")
	for i, m := range []proto.Message{
		{"t": "exec", "cmd": appendLine(marker)},
		{"t": "clipboard.set", "text": "replaced"},
		{"t": "shutdown"},
		{"t": "file.begin", "id": "f1", "name": "x.txt", "size": 1},
		{"t": "ping", "id": 9},
	} {
		send(t, w, m)
		res := expect(t, r, "res")
		if res["ok"] != false || res.String("code") != "INVALID_ARGUMENT" || !strings.Contains(res.String("error"), m.Type()) {
			t.Fatalf("message %d (%s) answered %v", i, m.Type(), res)
		}
	}
	send(t, w, proto.Message{"t": "req", "id": 2, "op": "ping"})
	if res := expect(t, r, "res"); res.Int("id") != 2 || res["ok"] != true {
		t.Fatalf("ping after the refused messages %v", res)
	}
	if got, _ := cb.Text(); got != "kept" {
		t.Fatalf("the clipboard holds %q", got)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the exec message ran: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "Desktop")); !os.IsNotExist(err) {
		t.Fatalf("the file message created the Desktop folder: %v", err)
	}
}
