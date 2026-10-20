package xfer_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/daemon/xfer"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/transport"
)

// pipeSession is a connection to an rpcAgent: its first stream is the rpc
// stream over net.Pipe, and it has no other streams.
type pipeSession struct {
	c      net.Conn
	mu     sync.Mutex
	opened bool
}

func (s *pipeSession) OpenStream(context.Context) (conn.Stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opened {
		return nil, errors.New("this fake connection has the rpc stream only")
	}
	s.opened = true
	return s.c, nil
}

func (s *pipeSession) Close(string) error { return s.c.Close() }
func (s *pipeSession) Err() error         { return nil }

// rpcAgent answers the rpc stream of docs/protocol.md §3 with answer.
type rpcAgent struct {
	answer func(req proto.Message) proto.Message
}

func (a *rpcAgent) dial(context.Context, conn.Target, transport.Identity) (conn.Session, error) {
	daemonEnd, agentEnd := net.Pipe()
	go a.serve(agentEnd)
	return &pipeSession{c: daemonEnd}, nil
}

func (a *rpcAgent) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	var wmu sync.Mutex
	write := func(m proto.Message) {
		b, _ := json.Marshal(m)
		wmu.Lock()
		c.Write(append(b, '\n'))
		wmu.Unlock()
	}
	if _, err := r.ReadBytes('\n'); err != nil {
		return
	}
	write(proto.Message{"t": "hello", "v": 1, "agent": "fake", "os": "darwin", "arch": "arm64", "host": "pc", "caps": map[string]any{},
		"session": map[string]any{}, "displays": []any{}, "boot_id": "b"})
	if _, err := r.ReadBytes('\n'); err != nil {
		return
	}
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var req proto.Message
		if json.Unmarshal(line, &req) != nil {
			continue
		}
		res := a.answer(req)
		res["t"], res["id"] = "res", req["id"]
		write(res)
	}
}

func TestASettlementKeepsACommitThatStillHoldsThePart(t *testing.T) {
	t.Setenv("DESKHAND_HOME", t.TempDir())
	mac := t.TempDir()
	data := randomBytes(t, 1<<20)
	src := filepath.Join(mac, "k.bin")
	writeFile(t, src, data)
	fi, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := "/pc/k.bin"
	var mu sync.Mutex
	released := false
	busy, prepares := 0, 0
	stored := map[string]proto.Message{}
	agent := &rpcAgent{answer: func(req proto.Message) proto.Message {
		mu.Lock()
		defer mu.Unlock()
		done := proto.Message{"ok": true, "path": dst, "outcome": "written"}
		switch req.String("op") {
		case "req.status":
			rid := req.String("rid")
			switch {
			case rid == "r-first" && !released:
				return proto.Message{"ok": true, "state": "running"}
			case rid == "r-first":
				return proto.Message{"ok": true, "state": "done", "path": dst, "outcome": "written"}
			case stored[rid] != nil:
				out := proto.Message{"state": "done"}
				for k, v := range stored[rid] {
					out[k] = v
				}
				return out
			}
			return proto.Message{"ok": true, "state": "unknown"}
		case "xfer.commit":
			if !released {
				busy++
				res := proto.Message{"ok": false, "code": "IN_PROGRESS", "error": "another xfer.commit works on the part now; retry", "retryable": true}
				stored[req.String("rid")] = res
				return res
			}
			return done
		case "xfer.prepare":
			prepares++
			return proto.Message{"ok": true, "part_size": 0, "part_sha256": sum(nil), "free_bytes": 1 << 40, "dest_exists": false}
		case "ping":
			return proto.Message{"ok": true}
		}
		return proto.Message{"ok": false, "code": "CAPABILITY_UNSUPPORTED", "error": "unknown op"}
	}}
	db := filepath.Join(t.TempDir(), "deskhand.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	if err := st.PutHost(store.Host{HostID: "pc", Name: "pc", Address: "127.0.0.1", Port: 1, AgentFP: "fp", OS: "darwin", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	row := store.Transfer{TransferID: "t-settle", HostID: "pc", Direction: "push", Local: src, Remote: dst, OnConflict: "fail", Sep: "/",
		State: "running", Planned: true, Rev: 3, Actor: "human", CreatedAt: now, UpdatedAt: now}
	if err := st.CreateTransfer(row); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTransfer(row, []store.TransferFile{{TransferID: "t-settle", Rel: "k.bin", Size: int64(len(data)),
		MTime: fi.ModTime().UTC().Format(time.RFC3339Nano), SHA256: sum(data), State: "verifying", BytesDone: int64(len(data)),
		CommitRID: "r-first", Part: true}}, true); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus(st, quiet)
	conns := conn.NewManager(conn.Options{Identity: newID(t, "controller"), Controller: "test", Requests: st, Logger: quiet, Dial: agent.dial,
		ResolveWait: time.Second, ReadyWait: time.Second})
	defer conns.Close()
	m := xfer.New(xfer.Options{Store: st, Bus: bus, Conns: conns, Logger: quiet})
	defer m.Close()
	m.Load()
	conns.Add(conn.Target{HostID: "pc", Name: "pc", Address: "127.0.0.1", Port: 1, Fingerprint: "fp"}).Connect()
	deadline := time.Now().Add(30 * time.Second)
	for {
		mu.Lock()
		n, p := busy, prepares
		mu.Unlock()
		if p > 0 {
			t.Fatalf("the settlement gave up the commit that held the part and prepared the part again after %d answers IN_PROGRESS", n)
		}
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the settlement asked %d times while the first commit held the part", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if tr, err := m.Get("t-settle"); err != nil || tr.Files[0].State == "failed" {
		t.Fatalf("while the first commit runs: %+v, %v", tr, err)
	}
	mu.Lock()
	released = true
	mu.Unlock()
	for {
		tr, err := m.Get("t-settle")
		if err != nil {
			t.Fatal(err)
		}
		if tr.State == "done" || tr.State == "failed" {
			f := tr.Files[0]
			if tr.State != "done" || f.Outcome != "written" || f.Path != dst {
				t.Fatalf("settled transfer %+v", tr)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("transfer %+v", tr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if prepares != 0 {
		t.Fatalf("the settlement prepared the part again %d times", prepares)
	}
}
