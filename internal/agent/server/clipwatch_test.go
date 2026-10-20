package server

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
)

// stuckClipboard is a clipboard whose Text waits until release is closed,
// after a send on entered when entered is set, and that counts its Sequence
// calls.
type stuckClipboard struct {
	fakeClipboard
	release   chan struct{}
	entered   chan struct{}
	stuck     atomic.Bool
	sequences atomic.Int64
}

func (c *stuckClipboard) Text() (string, error) {
	if c.stuck.Load() {
		if c.entered != nil {
			c.entered <- struct{}{}
		}
		<-c.release
	}
	return c.fakeClipboard.Text()
}

func (c *stuckClipboard) Sequence() uint64 {
	c.sequences.Add(1)
	return c.fakeClipboard.Sequence()
}

func TestAHungClipboardDoesNotStopOtherRequests(t *testing.T) {
	cb := &stuckClipboard{release: make(chan struct{})}
	cb.stuck.Store(true)
	t.Cleanup(func() { close(cb.release) })
	_, r, w, _ := startServerS(t, Options{Clipboard: cb, HomeDir: t.TempDir(), AllowExec: true})
	expect(t, r, "hello")
	send(t, w, proto.Message{"t": "req", "id": 1, "op": "clipboard.get"})
	send(t, w, proto.Message{"t": "req", "id": 2, "op": "ping"})
	if res := expect(t, r, "res"); res.Int("id") != 2 || res["ok"] != true {
		t.Fatalf("res %v", res)
	}
}

func TestClipboardSetThenGetReadsTheSetText(t *testing.T) {
	c, _ := newRPCClient(t, Options{Clipboard: &fakeClipboard{}})
	for _, text := range []string{"one", "two", "three"} {
		c.ok("clipboard.set", proto.Message{"text": text})
		if got := c.ok("clipboard.get", nil).String("text"); got != text {
			t.Fatalf("clipboard.get after set %q answered %q", text, got)
		}
	}
}

func TestOneClipboardWatcherPollsLessWhileNothingChanges(t *testing.T) {
	oldMin, oldMax := clipboardPollMin, clipboardPollMax
	clipboardPollMin, clipboardPollMax = 10*time.Millisecond, 80*time.Millisecond
	t.Cleanup(func() { clipboardPollMin, clipboardPollMax = oldMin, oldMax })
	cb := &stuckClipboard{release: make(chan struct{})}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go New(Options{Clipboard: cb, HomeDir: t.TempDir()}).Serve(l)
	var wg sync.WaitGroup
	readers := make([]*proto.Reader, 3)
	for i := range readers {
		_, readers[i], _ = dialAgent(t, l.Addr().String())
		wg.Add(1)
		go func(r *proto.Reader) {
			defer wg.Done()
			expect(t, r, "hello")
		}(readers[i])
	}
	wg.Wait()
	time.Sleep(time.Second)
	if n := cb.sequences.Load(); n > 30 {
		t.Fatalf("the clipboard was polled %d times in 1 s by three connections", n)
	}
	cb.external("changed on the PC")
	for i, r := range readers {
		if m := expectEvent(t, r, "clipboard.changed"); m.String("text") != "changed on the PC" {
			t.Fatalf("connection %d got %v", i, m)
		}
	}
}
