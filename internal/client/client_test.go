package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
)

func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "dhhome")
	if err != nil {
		panic(err)
	}
	os.Setenv("DESKHAND_HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// shortDir returns a temporary folder whose socket path stays below the
// unix socket limit.
func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "dh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// fakeDaemon answers hello, echo, fail and events.subscribe on a socket.
type fakeDaemon struct {
	ln     net.Listener
	hellos atomic.Int32
	mu     sync.Mutex
	conns  []net.Conn
}

// closeAll closes every accepted connection.
func (f *fakeDaemon) closeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		c.Close()
	}
}

func startFake(t *testing.T, socket string) *fakeDaemon {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Error(err)
		return nil
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Error(err)
		return nil
	}
	f := &fakeDaemon{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeDaemon) serve(c net.Conn) {
	defer c.Close()
	f.mu.Lock()
	f.conns = append(f.conns, c)
	f.mu.Unlock()
	var wmu sync.Mutex
	write := func(v any) {
		b, _ := json.Marshal(v)
		wmu.Lock()
		c.Write(append(b, '\n'))
		wmu.Unlock()
	}
	sc := bufio.NewScanner(c)
	for sc.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			continue
		}
		switch req.Method {
		case "hello":
			n := f.hellos.Add(1)
			write(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"client_id": fmt.Sprintf("c-%d", n)}})
		case "echo":
			write(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": json.RawMessage(req.Params)})
		case "fail":
			write(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32000, "message": "사람이 조작 중입니다. 사람이 제어권을 놓을 때까지 기다리세요",
				"data": map[string]any{"code": "CONTROL_HELD_BY_HUMAN", "retryable": false, "next_action": "human_release"}}})
		case "events.subscribe":
			write(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"subscribed": true}})
			write(map[string]any{"jsonrpc": "2.0", "method": "event", "params": map[string]any{"seq": 7, "ts": "2026-10-20T01:02:03.004Z",
				"kind": "connection.state", "host_id": "pc", "state": "ready"}})
		}
	}
}

func TestSingleDaemonUnderRace(t *testing.T) {
	socket := filepath.Join(shortDir(t), "run", "d.sock")
	var starts atomic.Int32
	var fake atomic.Pointer[fakeDaemon]
	start := func(context.Context) error {
		starts.Add(1)
		go func() {
			time.Sleep(150 * time.Millisecond)
			fake.Store(startFake(t, socket))
		}()
		return nil
	}
	const n = 4
	var wg sync.WaitGroup
	clients := make([]*Client, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			clients[i], errs[i] = DialOptions(ctx, "cli", fmt.Sprintf("t%d", i), Options{Socket: socket, Start: start})
		}()
	}
	wg.Wait()
	ids := map[string]bool{}
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("client %d: %v", i, errs[i])
		}
		ids[clients[i].ClientID] = true
		clients[i].Close()
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("the daemon was started %d times", got)
	}
	if len(ids) != n {
		t.Fatalf("client ids %v", ids)
	}
}

func TestDialFailsWhenDaemonNeverAnswers(t *testing.T) {
	dir := shortDir(t)
	socket := filepath.Join(dir, "run", "d.sock")
	begin := time.Now()
	_, err := DialOptions(context.Background(), "cli", "t", Options{Socket: socket, StartTimeout: 300 * time.Millisecond,
		Start: func(context.Context) error { return nil }})
	if err == nil || !strings.Contains(err.Error(), "daemon.log") {
		t.Fatalf("error %v", err)
	}
	if d := time.Since(begin); d > 3*time.Second {
		t.Fatalf("Dial took %s", d)
	}
	_, err = DialOptions(context.Background(), "cli", "t", Options{Socket: socket, StartTimeout: 300 * time.Millisecond,
		Start: func(context.Context) error { return errors.New("exec: deskhand: not found") }})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error of a failed start %v", err)
	}
}

func TestCallAndErrors(t *testing.T) {
	socket := filepath.Join(shortDir(t), "run", "d.sock")
	startFake(t, socket)
	c, err := DialOptions(context.Background(), "mcp", "t", Options{Socket: socket, Start: func(context.Context) error {
		return errors.New("the fake daemon is already running")
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var out struct {
		Path string `json:"path"`
	}
	if err := c.Call(context.Background(), "echo", map[string]any{"path": `C:\한글 폴더`}, &out); err != nil || out.Path != `C:\한글 폴더` {
		t.Fatalf("echo %+v, %v", out, err)
	}
	err = c.Call(context.Background(), "fail", nil, nil)
	var e *apierr.Error
	if !errors.As(err, &e) || e.Code != "CONTROL_HELD_BY_HUMAN" || e.NextAction != "human_release" || e.Retryable ||
		!strings.Contains(e.Message, "사람이 조작 중") {
		t.Fatalf("error %#v", err)
	}
	evs, err := c.Subscribe(context.Background(), []string{"connection.state"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-evs:
		if ev.Seq != 7 || ev.Kind != "connection.state" || ev.HostID != "pc" || ev.Params["state"] != "ready" {
			t.Fatalf("event %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}
	c.Close()
	if err := c.Call(context.Background(), "echo", nil, nil); err == nil {
		t.Fatal("call after Close succeeded")
	}
	select {
	case _, ok := <-evs:
		if ok {
			t.Fatal("an event arrived after Close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the events channel stayed open after Close")
	}
}

func TestEventsCloseWhenDaemonGoes(t *testing.T) {
	socket := filepath.Join(shortDir(t), "run", "d.sock")
	f := startFake(t, socket)
	c, err := DialOptions(context.Background(), "cli", "t", Options{Socket: socket, Start: func(context.Context) error {
		return errors.New("the fake daemon is already running")
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	evs, err := c.Subscribe(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	<-evs
	f.closeAll()
	select {
	case _, ok := <-evs:
		if ok {
			t.Fatal("unexpected event")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the events channel stayed open after the daemon closed the connection")
	}
}
