package quicserve

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/config"
	"github.com/YangTaeyoung/deskhand/internal/agent/server"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/transport"
	"github.com/YangTaeyoung/deskhand/internal/wire"
	"github.com/quic-go/quic-go"
)

// quiet is the logger of the servers under test.
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

type agentUnderTest struct {
	addr   string
	agent  transport.Identity
	daemon transport.Identity
	// home is the home folder of the agent's server.
	home string
}

func startAgent(t *testing.T) agentUnderTest {
	t.Helper()
	return startAgentWith(t, nil)
}

// startAgentWith serves an agent whose later streams go to the handlers
// streams returns for its server; nil streams serves none.
func startAgentWith(t *testing.T, streams func(*server.Server) StreamHandlers) agentUnderTest {
	t.Helper()
	t.Setenv("DESKHAND_HOME", t.TempDir())
	agentID, err := transport.NewIdentity("agent")
	if err != nil {
		t.Fatal(err)
	}
	daemonID, err := transport.NewIdentity("daemon")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Agent{Listen: fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t)), ControllerFingerprints: []string{daemonID.Fingerprint},
		DataDir: t.TempDir(), Name: "pc"}
	home := t.TempDir()
	srv := server.New(server.Options{Clipboard: fakeClipboard{}, HomeDir: home, AllowExec: true, DataDir: cfg.DataDir, Version: "test",
		Logger: quiet})
	var handlers StreamHandlers
	if streams != nil {
		handlers = streams(srv)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, agentID, srv, handlers) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Run did not return 5 s after its context ended")
		}
		srv.Close()
	})
	return agentUnderTest{addr: cfg.Listen, agent: agentID, daemon: daemonID, home: home}
}

func (a agentUnderTest) dial(t *testing.T) *quic.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := transport.Dial(ctx, a.addr, a.daemon, a.agent.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseWithError(0, "") })
	return conn
}

func openStream(t *testing.T, conn *quic.Conn, h wire.StreamHeader) *quic.Stream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st.SetDeadline(time.Now().Add(10 * time.Second))
	if err := wire.WriteHeader(st, h); err != nil {
		t.Fatal(err)
	}
	return st
}

func next(t *testing.T, r *proto.Reader, typ string) proto.Message {
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

// openRPC opens the rpc stream, reads the hello and answers it with a welcome.
func openRPC(t *testing.T, conn *quic.Conn) (proto.Message, *proto.Reader, *proto.Writer) {
	t.Helper()
	st := openStream(t, conn, wire.StreamHeader{S: "rpc"})
	r, w := proto.NewReader(st), proto.NewWriter(st)
	hello := next(t, r, "hello")
	if err := w.Send(proto.Message{"t": "welcome", "v": 1, "controller": "test-mac"}); err != nil {
		t.Fatal(err)
	}
	return hello, r, w
}

func readRefusal(t *testing.T, st *quic.Stream) refusal {
	t.Helper()
	br := bufio.NewReader(st)
	line, err := br.ReadBytes('\n')
	if err != nil {
		t.Fatalf("reading the answer to the header: %v", err)
	}
	var got refusal
	if err := json.Unmarshal(line, &got); err != nil {
		t.Fatalf("answer %q: %v", line, err)
	}
	if _, err := br.ReadByte(); err != io.EOF {
		var se *quic.StreamError
		var ae *quic.ApplicationError
		if !errors.As(err, &se) && !errors.As(err, &ae) {
			t.Fatalf("the stream went on after the refusal: %v", err)
		}
	}
	return got
}

func TestServeOverQUIC(t *testing.T) {
	a := startAgent(t)
	conn := a.dial(t)
	hello, r, w := openRPC(t, conn)
	if hello.Int("v") != 1 || hello.String("agent") != "test" || hello.String("boot_id") == "" || hello.String("host") == "" ||
		hello.String("os") == "" || hello.String("arch") == "" {
		t.Fatalf("hello %v", hello)
	}
	caps, _ := hello["caps"].(map[string]any)
	exec, _ := caps["exec"].(map[string]any)
	screen, _ := caps["screen"].(map[string]any)
	if exec["state"] != "ready" || screen["state"] != "unsupported" || screen["reason"] == "" {
		t.Fatalf("caps %v", hello["caps"])
	}
	if _, ok := hello["displays"].([]any); !ok {
		t.Fatalf("displays %v", hello["displays"])
	}
	if _, ok := hello["session"].(map[string]any); !ok {
		t.Fatalf("session %v", hello["session"])
	}
	if err := w.Send(proto.Message{"t": "req", "id": 1, "op": "ping"}); err != nil {
		t.Fatal(err)
	}
	res := next(t, r, "res")
	if res.Int("id") != 1 || res["ok"] != true || res["pong"] != true {
		t.Fatalf("res %v", res)
	}
}

func TestUnknownStreamKindRefused(t *testing.T) {
	a := startAgent(t)
	conn := a.dial(t)
	_, r, w := openRPC(t, conn)
	st := openStream(t, conn, wire.StreamHeader{S: "bogus"})
	got := readRefusal(t, st)
	if got.OK || got.Code != wire.CodeCapabilityUnsupported || got.Error == "" {
		t.Fatalf("answer %+v", got)
	}
	if err := w.Send(proto.Message{"t": "req", "id": 2, "op": "ping"}); err != nil {
		t.Fatal(err)
	}
	if res := next(t, r, "res"); res["ok"] != true {
		t.Fatalf("the rpc stream after a refused stream answered %v", res)
	}
}

func TestFirstStreamMustBeRPC(t *testing.T) {
	a := startAgent(t)
	conn := a.dial(t)
	st := openStream(t, conn, wire.StreamHeader{S: "video"})
	got := readRefusal(t, st)
	if got.OK || got.Code != wire.CodeInvalidArgument || got.Error == "" {
		t.Fatalf("answer %+v", got)
	}
	select {
	case <-conn.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the connection stayed open after a first stream that is not rpc")
	}
}

func TestASecondRPCStreamIsRefused(t *testing.T) {
	a := startAgent(t)
	conn := a.dial(t)
	openRPC(t, conn)
	st := openStream(t, conn, wire.StreamHeader{S: "rpc"})
	if got := readRefusal(t, st); got.OK || got.Code != wire.CodeInvalidArgument {
		t.Fatalf("answer %+v", got)
	}
}

func TestARegisteredStreamKindReachesItsHandler(t *testing.T) {
	t.Setenv("DESKHAND_HOME", t.TempDir())
	agentID, _ := transport.NewIdentity("agent")
	daemonID, _ := transport.NewIdentity("daemon")
	cfg := config.Agent{Listen: fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t)), ControllerFingerprints: []string{daemonID.Fingerprint},
		DataDir: t.TempDir(), Name: "pc"}
	srv := server.New(server.Options{Clipboard: fakeClipboard{}, HomeDir: t.TempDir(), DataDir: cfg.DataDir, Logger: quiet})
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	handlers := StreamHandlers{"echo": func(_ context.Context, st *quic.Stream, h wire.StreamHeader) {
		defer st.Close()
		wire.WriteHeader(st, map[string]any{"ok": true, "path": h.Path})
	}}
	go Run(ctx, cfg, agentID, srv, handlers)
	conn := agentUnderTest{addr: cfg.Listen, agent: agentID, daemon: daemonID}.dial(t)
	openRPC(t, conn)
	st := openStream(t, conn, wire.StreamHeader{S: "echo", Path: "x"})
	var got struct {
		OK   bool   `json:"ok"`
		Path string `json:"path"`
	}
	if err := wire.ReadHeader(bufio.NewReader(st), &got); err != nil || !got.OK || got.Path != "x" {
		t.Fatalf("answer %+v, %v", got, err)
	}
}

// ping sends ping on the rpc stream of w and r and reports whether it was
// answered.
func ping(t *testing.T, r *proto.Reader, w *proto.Writer, id int) bool {
	t.Helper()
	if err := w.Send(proto.Message{"t": "req", "id": id, "op": "ping"}); err != nil {
		return false
	}
	for {
		m, err := r.Next()
		if err != nil {
			return false
		}
		if m.Type() == "res" && m.Int("id") == id {
			return m["ok"] == true
		}
	}
}

func TestANewRPCStreamOfTheSameControllerClosesItsOlderConnection(t *testing.T) {
	a := startAgent(t)
	old := a.dial(t)
	openRPC(t, old)
	newer := a.dial(t)
	_, r, w := openRPC(t, newer)
	select {
	case <-old.Context().Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the older connection of the controller stayed open after its newer rpc stream began")
	}
	var ae *quic.ApplicationError
	if err := context.Cause(old.Context()); !errors.As(err, &ae) || !ae.Remote || ae.ErrorMessage == "" {
		t.Fatalf("the older connection ended with %v", err)
	}
	if !ping(t, r, w, 1) {
		t.Fatal("the newer connection does not answer")
	}
	if newer.Context().Err() != nil {
		t.Fatalf("the newer connection closed: %v", context.Cause(newer.Context()))
	}
}

func TestAnotherControllersConnectionStaysOpen(t *testing.T) {
	t.Setenv("DESKHAND_HOME", t.TempDir())
	agentID, _ := transport.NewIdentity("agent")
	first, _ := transport.NewIdentity("mac-1")
	second, _ := transport.NewIdentity("mac-2")
	cfg := config.Agent{Listen: fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t)), ControllerFingerprints: []string{first.Fingerprint, second.Fingerprint},
		DataDir: t.TempDir(), Name: "pc"}
	srv := server.New(server.Options{Clipboard: fakeClipboard{}, HomeDir: t.TempDir(), DataDir: cfg.DataDir, Logger: quiet})
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go Run(ctx, cfg, agentID, srv, nil)
	one := agentUnderTest{addr: cfg.Listen, agent: agentID, daemon: first}.dial(t)
	_, r, w := openRPC(t, one)
	two := agentUnderTest{addr: cfg.Listen, agent: agentID, daemon: second}.dial(t)
	openRPC(t, two)
	if !ping(t, r, w, 1) || one.Context().Err() != nil {
		t.Fatalf("the first controller's connection closed when another controller connected: %v", context.Cause(one.Context()))
	}
}
