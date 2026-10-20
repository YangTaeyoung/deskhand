package localapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/hosts"
	"github.com/YangTaeyoung/deskhand/internal/daemon/jobs"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/transport"
)

type rig struct {
	socket string
	bus    *events.Bus
	st     *store.Store
}

func newRig(t *testing.T) rig {
	t.Helper()
	dir, err := os.MkdirTemp("", "dh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("DESKHAND_HOME", dir)
	st, err := store.Open(filepath.Join(dir, "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bus := events.NewBus(st, nil)
	var reg *hosts.Registry
	mgr := conn.NewManager(conn.Options{
		Dial: func(context.Context, conn.Target, transport.Identity) (conn.Session, error) {
			return nil, errors.New("network is unreachable")
		},
		OnEvent:   func(ev conn.Event) { reg.HandleConnEvent(ev) },
		Backoff:   conn.BackoffConfig{Initial: time.Hour},
		ReadyWait: 50 * time.Millisecond, Requests: st,
	})
	t.Cleanup(mgr.Close)
	jreg := jobs.New(st, bus, mgr)
	reg = hosts.New(hosts.Options{Store: st, Bus: bus, Manager: mgr, Jobs: jreg, ConnectWait: 50 * time.Millisecond})
	socket := filepath.Join(dir, "run", "d.sock")
	ln, err := Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Deps{Store: st, Bus: bus, Hosts: reg, Jobs: jreg, Version: "test"})
	go srv.Serve(ln)
	t.Cleanup(srv.Close)
	return rig{socket: socket, bus: bus, st: st}
}

type wireConn struct {
	t  *testing.T
	c  net.Conn
	sc *bufio.Scanner
}

func (r rig) dial(t *testing.T) *wireConn {
	t.Helper()
	c, err := net.Dial("unix", r.socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	return &wireConn{t: t, c: c, sc: sc}
}

func (w *wireConn) send(line string) {
	w.t.Helper()
	if _, err := w.c.Write([]byte(line + "\n")); err != nil {
		w.t.Fatal(err)
	}
}

func (w *wireConn) next() map[string]any {
	w.t.Helper()
	_ = w.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if !w.sc.Scan() {
		w.t.Fatalf("no line: %v", w.sc.Err())
	}
	var m map[string]any
	if err := json.Unmarshal(w.sc.Bytes(), &m); err != nil {
		w.t.Fatalf("line %q: %v", w.sc.Bytes(), err)
	}
	return m
}

func errorOf(t *testing.T, m map[string]any) (float64, string) {
	t.Helper()
	e, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error in %v", m)
	}
	data := e["data"].(map[string]any)
	return e["code"].(float64), data["code"].(string)
}

func (w *wireConn) hello() {
	w.t.Helper()
	w.send(`{"jsonrpc":"2.0","id":0,"method":"hello","params":{"client":"cli","name":"t","version":"1"}}`)
	if res := w.next(); res["result"] == nil {
		w.t.Fatalf("hello %v", res)
	}
}

func TestHelloRequiredFirst(t *testing.T) {
	r := newRig(t)
	w := r.dial(t)
	w.send(`{"jsonrpc":"2.0","method":"host.list"}`)
	w.send(`{"jsonrpc":"2.0","id":1,"method":"host.list"}`)
	if code, data := errorOf(t, w.next()); code != codeServer || data != "INVALID_ARGUMENT" {
		t.Fatalf("before hello %v %v", code, data)
	}
	w.send(`{"jsonrpc":"2.0","id":2,"method":"hello","params":{"client":"robot"}}`)
	if _, data := errorOf(t, w.next()); data != "INVALID_ARGUMENT" {
		t.Fatalf("hello with an unknown client kind %v", data)
	}
	w.send(`{"jsonrpc":"2.0","id":3,"method":"hello","params":{"client":"app","name":"Deskhand","version":"1"}}`)
	res := w.next()["result"].(map[string]any)
	if id, _ := res["client_id"].(string); !strings.HasPrefix(id, "c-") || res["daemon_version"] != "test" || res["api_version"] != float64(1) {
		t.Fatalf("hello result %v", res)
	}
	w.send(`{"jsonrpc":"2.0","id":4,"method":"host.list"}`)
	m := w.next()
	if m["id"] != float64(4) {
		t.Fatalf("response %v", m)
	}
	if hostsList := m["result"].(map[string]any)["hosts"].([]any); len(hostsList) != 0 {
		t.Fatalf("hosts %v", hostsList)
	}
}

func TestProtocolErrors(t *testing.T) {
	r := newRig(t)
	w := r.dial(t)
	w.hello()
	w.send(`{not json`)
	m := w.next()
	if code, _ := errorOf(t, m); code != codeParse || m["id"] != nil {
		t.Fatalf("parse error %v", m)
	}
	w.send(`{"id":1,"method":"host.list"}`)
	if code, _ := errorOf(t, w.next()); code != codeInvalidRequest {
		t.Fatal("request without jsonrpc accepted")
	}
	w.send(`{"jsonrpc":"2.0","id":2,"method":"screen.teleport"}`)
	if code, data := errorOf(t, w.next()); code != codeMethodNotFound || data != "CAPABILITY_UNSUPPORTED" {
		t.Fatalf("unknown method %v %v", code, data)
	}
	w.send(`{"jsonrpc":"2.0","id":3,"method":"exec.run","params":{"host_id":7}}`)
	if code, data := errorOf(t, w.next()); code != codeInvalidParams || data != "INVALID_ARGUMENT" {
		t.Fatalf("bad params %v %v", code, data)
	}
	w.send(`{"jsonrpc":"2.0","method":"host.list"}`)
	w.send(`{"jsonrpc":"2.0","method":"screen.teleport"}`)
	w.send(`{"jsonrpc":"2.0","method":"exec.run","params":{"host_id":7}}`)
	w.send(`{"jsonrpc":"2.0","id":5,"method":"exec.run","params":{"host_id":"nope","argv":["x"]}}`)
	m = w.next()
	if m["id"] != float64(5) {
		t.Fatalf("a notification was answered: %v", m)
	}
	if _, data := errorOf(t, m); data != "NOT_FOUND" {
		t.Fatalf("unknown host %v", m)
	}
}

func TestSubscribeReplacesFilter(t *testing.T) {
	r := newRig(t)
	w := r.dial(t)
	w.hello()
	w.send(`{"jsonrpc":"2.0","id":1,"method":"events.subscribe","params":{"kinds":["a"]}}`)
	if res := w.next(); res["result"] == nil {
		t.Fatalf("subscribe %v", res)
	}
	r.bus.Publish("pc", "", "b", map[string]any{"n": 1})
	r.bus.Publish("pc", "", "a", map[string]any{"n": 2})
	ev := w.next()
	p := ev["params"].(map[string]any)
	if ev["method"] != "event" || p["kind"] != "a" || p["n"] != float64(2) || p["host_id"] != "pc" || p["seq"] == nil || p["ts"] == nil {
		t.Fatalf("event %v", ev)
	}
	w.send(`{"jsonrpc":"2.0","id":2,"method":"events.subscribe","params":{"kinds":["b"]}}`)
	if res := w.next(); res["id"] != float64(2) {
		t.Fatalf("second subscribe %v", res)
	}
	r.bus.Publish("pc", "", "a", map[string]any{"n": 3})
	r.bus.Publish("pc", "", "b", map[string]any{"n": 4})
	if p := w.next()["params"].(map[string]any); p["kind"] != "b" || p["n"] != float64(4) {
		t.Fatalf("event after the new filter %v", p)
	}
}

func TestEnvelopeOfUnreachableHost(t *testing.T) {
	r := newRig(t)
	if err := r.st.PutHost(store.Host{HostID: "pc", Name: "pc", Address: "127.0.0.1", Port: 1, AgentFP: "fp", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	w := r.dial(t)
	w.hello()
	w.send(`{"jsonrpc":"2.0","id":1,"method":"exec.run","params":{"host_id":"pc","argv":["echo","hi"]}}`)
	res := w.next()["result"].(map[string]any)
	action := res["action"].(map[string]any)
	errObj := res["error"].(map[string]any)
	rid, _ := res["request_id"].(string)
	if res["status"] != "failed" || action["executed"] != false || action["outcome"] != "not_started" || errObj["code"] != "OFFLINE" ||
		!strings.HasPrefix(rid, "r-") || res["job_id"] == nil || res["next_action"] == nil {
		t.Fatalf("envelope %v", res)
	}
	if _, err := r.st.GetRequest(rid); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a request no connection took was recorded: %v", err)
	}
	w.send(`{"jsonrpc":"2.0","id":2,"method":"jobs.get","params":{"job_id":"` + res["job_id"].(string) + `"}}`)
	if job := w.next()["result"].(map[string]any); job["status"] != "failed" {
		t.Fatalf("job %v", job)
	}
}

func TestListenRefusesRunningDaemon(t *testing.T) {
	r := newRig(t)
	if _, err := Listen(r.socket); !errors.Is(err, ErrInUse) {
		t.Fatalf("second listen: %v", err)
	}
	fi, err := os.Stat(r.socket)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v, %v", fi.Mode(), err)
	}
	if fi, err := os.Stat(filepath.Dir(r.socket)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("socket folder mode %v, %v", fi.Mode(), err)
	}
}

func TestOversizedRequestIsRPCError(t *testing.T) {
	r := newRig(t)
	if err := r.st.PutHost(store.Host{HostID: "pc", Name: "pc", Address: "127.0.0.1", Port: 1, AgentFP: "fp", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	w := r.dial(t)
	w.hello()
	big := strings.Repeat("A", 2<<20)
	w.send(`{"jsonrpc":"2.0","id":1,"method":"files.write","params":{"host_id":"pc","path":"C:\\a.bin","data":"` + big + `"}}`)
	m := w.next()
	if code, data := errorOf(t, m); code != codeInvalidParams || data != "INVALID_ARGUMENT" {
		t.Fatalf("oversized files.write %v", m)
	}
	if msg := m["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "1 MiB") {
		t.Fatalf("message %q", msg)
	}
	w.send(`{"jsonrpc":"2.0","id":2,"method":"exec.run","params":{"host_id":"pc","argv":["x"],"stdin":"` + big + `"}}`)
	if code, data := errorOf(t, w.next()); code != codeInvalidParams || data != "INVALID_ARGUMENT" {
		t.Fatal("oversized exec.run was not a params error")
	}
	if jobsList, _ := r.st.ListJobs(store.JobFilter{}); len(jobsList) != 0 {
		t.Fatalf("a refused request made jobs %+v", jobsList)
	}
	if entries, _ := r.st.ListTimeline(store.TimelineFilter{}); len(entries) != 0 {
		t.Fatalf("a refused request was recorded %+v", entries)
	}
}
