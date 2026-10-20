package server

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
)

func startServerS(t *testing.T, opt Options) (*Server, *proto.Reader, *proto.Writer, net.Conn) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	s := New(opt)
	go s.Serve(l)
	c, r, w := dialAgent(t, l.Addr().String())
	return s, r, w, c
}

// send writes m and fails the test when the write fails.
func send(t *testing.T, w *proto.Writer, m proto.Message) {
	t.Helper()
	if err := w.Send(m); err != nil {
		t.Fatalf("send %v: %v", m, err)
	}
}

// capSet returns the capability keys of a hello or caps.changed whose state
// is ready.
func capSet(m proto.Message) map[string]bool {
	set := map[string]bool{}
	caps, _ := m["caps"].(map[string]any)
	for k, v := range caps {
		if st, _ := v.(map[string]any); st["state"] == "ready" {
			set[k] = true
		}
	}
	return set
}

// expectEvent returns the next event named name.
func expectEvent(t *testing.T, r *proto.Reader, name string) proto.Message {
	t.Helper()
	for {
		m := expect(t, r, "ev")
		if m.String("name") == name {
			return m
		}
	}
}

// expectCaps returns the next caps.changed event.
func expectCaps(t *testing.T, r *proto.Reader) proto.Message {
	t.Helper()
	return expectEvent(t, r, "caps.changed")
}

func TestHelloCarriesEveryCapabilityKey(t *testing.T) {
	_, r, _, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir(), Version: "1.2.3"})
	hello := expect(t, r, "hello")
	if hello.Int("v") != 1 || hello.String("agent") != "1.2.3" || hello.String("arch") != runtime.GOARCH || hello.String("os") != runtime.GOOS ||
		hello.String("boot_id") == "" {
		t.Fatalf("hello %v", hello)
	}
	caps, _ := hello["caps"].(map[string]any)
	for _, k := range []string{"screen", "input", "uia", "exec", "files", "processes", "pty", "tunnel", "diag", "clipboard"} {
		st, _ := caps[k].(map[string]any)
		if st["state"] == nil || (st["state"] != "ready" && st["reason"] == "") {
			t.Fatalf("caps.%s %v", k, caps[k])
		}
	}
	for _, k := range []string{"screen", "input", "tunnel"} {
		if st, _ := caps[k].(map[string]any); st["state"] != "unsupported" || st["reason"] != "not implemented in this agent version" {
			t.Fatalf("caps.%s %v", k, caps[k])
		}
	}
	set := capSet(hello)
	if set["exec"] || set["files"] || set["processes"] || set["pty"] || set["diag"] {
		t.Fatalf("caps without exec %v", hello["caps"])
	}

	_, r, _, _ = startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir(), AllowExec: true})
	hello = expect(t, r, "hello")
	set = capSet(hello)
	for _, c := range []string{"exec", "files", "processes", "pty", "clipboard"} {
		if !set[c] {
			t.Fatalf("caps with exec lack %s: %v", c, set)
		}
	}
	pty, _ := hello["caps"].(map[string]any)["pty"].(map[string]any)
	if shells, _ := pty["shells"].([]any); len(shells) == 0 || shells[0] != defaultTestShell {
		t.Fatalf("caps.pty %v", pty)
	}
}

func TestTheBootIDStaysForTheProcess(t *testing.T) {
	_, r1, _, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	_, r2, _, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	if a, b := expect(t, r1, "hello").String("boot_id"), expect(t, r2, "hello").String("boot_id"); a == "" || a != b {
		t.Fatalf("boot ids %q and %q", a, b)
	}
}

func TestARequestBeforeTheWelcomeClosesTheStream(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	s := New(Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	ran := make(chan struct{}, 1)
	s.register("t.run", false, func(context.Context, proto.Message) (proto.Message, error) {
		ran <- struct{}{}
		return proto.Message{}, nil
	})
	go s.Serve(l)
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(10 * time.Second))
	r := proto.NewReader(c)
	expect(t, r, "hello")
	send(t, proto.NewWriter(c), proto.Message{"t": "req", "id": 1, "op": "t.run"})
	if m, err := r.Next(); err != io.EOF {
		t.Fatalf("after a request before the welcome the agent sent %v, %v", m, err)
	}
	select {
	case <-ran:
		t.Fatal("the request before the welcome ran")
	default:
	}
}

func TestACodedErrorCarriesItsCode(t *testing.T) {
	s, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	s.register("t.coded", false, func(context.Context, proto.Message) (proto.Message, error) {
		return nil, coded("NOT_FOUND", "no such thing; check the name", false)
	})
	expect(t, r, "hello")
	send(t, w, proto.Message{"t": "req", "id": 5, "op": "t.coded"})
	res := expect(t, r, "res")
	if res["ok"] != false || res.String("code") != "NOT_FOUND" || res["retryable"] != false || res.String("error") != "no such thing; check the name" {
		t.Fatalf("res %v", res)
	}
}

func TestClipboardOps(t *testing.T) {
	cb := &fakeClipboard{text: "on the PC"}
	_, r, w, _ := startServerS(t, Options{Clipboard: cb, HomeDir: t.TempDir()})
	expect(t, r, "hello")
	send(t, w, proto.Message{"t": "req", "id": 1, "op": "clipboard.get"})
	if res := expect(t, r, "res"); res["ok"] != true || res.String("text") != "on the PC" {
		t.Fatalf("clipboard.get %v", res)
	}
	send(t, w, proto.Message{"t": "req", "id": 2, "op": "clipboard.set", "text": "from the host"})
	if res := expect(t, r, "res"); res["ok"] != true {
		t.Fatalf("clipboard.set %v", res)
	}
	if got, _ := cb.Text(); got != "from the host" {
		t.Fatalf("the clipboard holds %q", got)
	}
	send(t, w, proto.Message{"t": "req", "id": 3, "op": "clipboard.set"})
	if res := expect(t, r, "res"); res["ok"] != false || res.String("code") != "INVALID_ARGUMENT" {
		t.Fatalf("clipboard.set without text %v", res)
	}
}

func TestReqResRoundTrip(t *testing.T) {
	_, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	expect(t, r, "hello")
	send(t, w, proto.Message{"t": "req", "id": 7, "op": "ping"})
	res := expect(t, r, "res")
	if res.Int("id") != 7 || res["ok"] != true || res["pong"] != true {
		t.Fatalf("res %v", res)
	}
}

func TestUnknownOp(t *testing.T) {
	_, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	expect(t, r, "hello")
	send(t, w, proto.Message{"t": "req", "id": 1, "op": "nope"})
	res := expect(t, r, "res")
	if res["ok"] != false || res.String("error") != "unknown op 'nope'; update deskhand-agent" {
		t.Fatalf("res %v", res)
	}
}

func TestGatedOpWithoutAllowExec(t *testing.T) {
	s, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	s.register("t.gated", true, func(context.Context, proto.Message) (proto.Message, error) {
		return proto.Message{"ran": true}, nil
	})
	expect(t, r, "hello")
	send(t, w, proto.Message{"t": "req", "id": 2, "op": "t.gated"})
	res := expect(t, r, "res")
	if res["ok"] != false || res.String("error") != "exec, file and process operations are off in this agent; start it with deskhand-agent run, which serves them" || res.Has("ran") {
		t.Fatalf("res %v", res)
	}
}

func TestGatedOpWithAllowExec(t *testing.T) {
	s, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir(), AllowExec: true})
	s.register("t.gated", true, func(_ context.Context, req proto.Message) (proto.Message, error) {
		return proto.Message{"echo": req.String("v")}, nil
	})
	expect(t, r, "hello")
	send(t, w, proto.Message{"t": "req", "id": 3, "op": "t.gated", "v": "hi"})
	res := expect(t, r, "res")
	if res["ok"] != true || res.String("echo") != "hi" {
		t.Fatalf("res %v", res)
	}
}

func TestHandlerErrorBecomesFailure(t *testing.T) {
	s, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	s.register("t.fail", false, func(context.Context, proto.Message) (proto.Message, error) {
		return nil, errors.New("no such thing; check the name")
	})
	expect(t, r, "hello")
	send(t, w, proto.Message{"t": "req", "id": 4, "op": "t.fail"})
	res := expect(t, r, "res")
	if res.Int("id") != 4 || res["ok"] != false || res.String("error") != "no such thing; check the name" {
		t.Fatalf("res %v", res)
	}
}

func TestAReplyOverTheHostLineLimitBecomesAnError(t *testing.T) {
	s := New(Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	size := 0
	s.register("t.big", false, func(context.Context, proto.Message) (proto.Message, error) {
		return proto.Message{"text": strings.Repeat("<", size)}, nil
	})
	var got proto.Message
	capture := func(m proto.Message, _ []byte) { got = m }

	size = 4 << 20
	s.dispatch(context.Background(), proto.Message{"t": "req", "id": 1, "op": "t.big"}, capture)
	if got["ok"] != true || len(got.String("text")) != size {
		t.Fatalf("a reply of %d characters '<' answered ok %v", size, got["ok"])
	}

	size = maxReplyLine
	s.dispatch(context.Background(), proto.Message{"t": "req", "id": 2, "op": "t.big"}, capture)
	want := "the answer to 't.big' is larger than the 8 MiB line the host reads; ask for less (a lower max, depth or limit, a narrower path or a name filter)"
	if got["ok"] != false || got.Int("id") != 2 || got.String("error") != want || got.Has("text") {
		t.Fatalf("a reply of %d bytes answered %v %q", size, got["ok"], got.String("error"))
	}
	line, err := proto.Encode(got)
	if err != nil || len(line) >= maxReplyLine {
		t.Fatalf("the error line is %d bytes: %v", len(line), err)
	}
}

func TestARequestWithoutAnIdIsNotRun(t *testing.T) {
	s, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	ran := make(chan struct{}, 1)
	s.register("t.run", false, func(context.Context, proto.Message) (proto.Message, error) {
		ran <- struct{}{}
		return proto.Message{}, nil
	})
	expect(t, r, "hello")
	send(t, w, proto.Message{"t": "req", "op": "t.run"})
	res := expect(t, r, "res")
	if res["ok"] != false || res.String("error") != "the request has no id; give every req an id to match its answer" || res["id"] != nil {
		t.Fatalf("res %v", res)
	}
	select {
	case <-ran:
		t.Fatal("the op ran")
	default:
	}
}

func TestRequestsOverTheInFlightLimitAreRefused(t *testing.T) {
	s, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	release := make(chan struct{})
	s.register("t.wait", false, func(context.Context, proto.Message) (proto.Message, error) {
		<-release
		return proto.Message{}, nil
	})
	expect(t, r, "hello")
	for id := 1; id <= maxInFlight+1; id++ {
		send(t, w, proto.Message{"t": "req", "id": id, "op": "t.wait"})
	}
	res := expect(t, r, "res")
	want := "256 requests are running on this connection; wait for an answer before sending another"
	if res.Int("id") != maxInFlight+1 || res["ok"] != false || res.String("error") != want {
		t.Fatalf("res %v", res)
	}
	close(release)
	for i := 0; i < maxInFlight; i++ {
		if res := expect(t, r, "res"); res["ok"] != true {
			t.Fatalf("res %v", res)
		}
	}
	send(t, w, proto.Message{"t": "req", "id": 1000, "op": "ping"})
	if res := expect(t, r, "res"); res.Int("id") != 1000 || res["ok"] != true {
		t.Fatalf("res after the limit %v", res)
	}
}

func TestRequestsRunConcurrently(t *testing.T) {
	s, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	release := make(chan struct{})
	s.register("t.slow", false, func(context.Context, proto.Message) (proto.Message, error) {
		<-release
		return proto.Message{"which": "slow"}, nil
	})
	s.register("t.fast", false, func(context.Context, proto.Message) (proto.Message, error) {
		return proto.Message{"which": "fast"}, nil
	})
	expect(t, r, "hello")
	send(t, w, proto.Message{"t": "req", "id": 1, "op": "t.slow"})
	send(t, w, proto.Message{"t": "req", "id": 2, "op": "t.fast"})
	first := expect(t, r, "res")
	if first.Int("id") != 2 || first.String("which") != "fast" {
		t.Fatalf("first res %v", first)
	}
	close(release)
	second := expect(t, r, "res")
	if second.Int("id") != 1 || second.String("which") != "slow" {
		t.Fatalf("second res %v", second)
	}
}

func TestHandlerPanicBecomesError(t *testing.T) {
	s, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	s.register("t.panic", false, func(context.Context, proto.Message) (proto.Message, error) {
		panic("boom")
	})
	expect(t, r, "hello")
	send(t, w, proto.Message{"t": "req", "id": 1, "op": "t.panic"})
	res := expect(t, r, "res")
	if res.Int("id") != 1 || res["ok"] != false || res.String("error") == "" {
		t.Fatalf("res %v", res)
	}
	send(t, w, proto.Message{"t": "req", "id": 2, "op": "ping"})
	res = expect(t, r, "res")
	if res.Int("id") != 2 || res["ok"] != true {
		t.Fatalf("ping after panic %v", res)
	}
}

func TestEmitReachesAllConnections(t *testing.T) {
	s, r1, _, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	expect(t, r1, "hello")
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go s.Serve(l)
	_, r2, _ := dialAgent(t, l.Addr().String())
	expect(t, r2, "hello")

	deadline := time.Now().Add(5 * time.Second)
	for {
		s.connMu.Lock()
		n := len(s.conns)
		s.connMu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("connections registered: %d", n)
		}
		time.Sleep(10 * time.Millisecond)
	}

	s.emit("x", proto.Message{"n": 1})
	for i, r := range []*proto.Reader{r1, r2} {
		ev := expect(t, r, "ev")
		if ev.String("name") != "x" || ev.Int("n") != 1 {
			t.Fatalf("connection %d ev %v", i, ev)
		}
	}
}

func TestDispatchHandsOverTheLineItEncodedForTheSizeCheck(t *testing.T) {
	s := New(Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	var got proto.Message
	var line []byte
	s.dispatch(context.Background(), proto.Message{"t": "req", "id": 3, "op": "ping"}, func(m proto.Message, l []byte) { got, line = m, l })
	want, err := proto.Encode(got)
	if err != nil || line == nil || string(line) != string(want) {
		t.Fatalf("line %q for %v, expected %q", line, got, want)
	}
}

func TestTheConnWritesAHandedOverLineAsItIs(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close()
	c := newConn(srv, log.New(io.Discard, "", 0))
	go c.writeLoop()
	defer c.stopWriter()
	c.sendReply(proto.Message{"t": "res", "id": 1, "ok": true}, []byte("{\"t\":\"res\",\"id\":1,\"ok\":true,\"as\":\"encoded\"}\n"))
	cli.SetDeadline(time.Now().Add(10 * time.Second))
	m, err := proto.NewReader(cli).Next()
	if err != nil || m.String("as") != "encoded" {
		t.Fatalf("read %v, %v", m, err)
	}
}
