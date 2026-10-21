package xfer_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/config"
	"github.com/YangTaeyoung/deskhand/internal/agent/quicserve"
	"github.com/YangTaeyoung/deskhand/internal/agent/server"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/jobs"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/daemon/xfer"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/transport"
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

// pcAgent is an agent served over QUIC in this process with the put and get
// streams; home holds the files of the PC.
type pcAgent struct {
	t       *testing.T
	port    int
	id      transport.Identity
	home    string
	dataDir string
	trusted string

	mu   sync.Mutex
	stop func()
}

// start serves the agent, retrying for up to 5 s while its port is still
// bound.
func (a *pcAgent) start() {
	a.t.Helper()
	cfg := config.Agent{Listen: fmt.Sprintf("127.0.0.1:%d", a.port), ControllerFingerprints: []string{a.trusted}, DataDir: a.dataDir, Name: "pc"}
	var (
		srv    *server.Server
		cancel context.CancelFunc
		done   chan error
	)
	for deadline := time.Now().Add(5 * time.Second); ; {
		srv = server.New(server.Options{Clipboard: fakeClipboard{}, HomeDir: a.home, AllowExec: true, DataDir: a.dataDir, Version: "test", Logger: quiet})
		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		done = make(chan error, 1)
		go func() { done <- quicserve.Run(ctx, cfg, a.id, srv, quicserve.TransferStreams(srv)) }()
		select {
		case err := <-done:
			cancel()
			srv.Close()
			if time.Now().After(deadline) {
				a.t.Fatalf("agent: %v", err)
			}
			time.Sleep(50 * time.Millisecond)
			continue
		case <-time.After(100 * time.Millisecond):
		}
		break
	}
	stopped := false
	a.mu.Lock()
	a.stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			a.t.Error("agent did not stop")
		}
		srv.Close()
	}
	a.mu.Unlock()
}

// halt stops the agent; its files, journal and receipts stay.
func (a *pcAgent) halt() {
	a.mu.Lock()
	stop := a.stop
	a.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// daemonSide is the part of a daemon a transfer manager needs.
type daemonSide struct {
	st    *store.Store
	bus   *events.Bus
	conns *conn.Manager
	jobs  *jobs.Registry
	m     *xfer.Manager
}

// harness is a transfer manager connected to an in-process agent; mac
// holds the files of this Mac.
type harness struct {
	t     *testing.T
	db    string
	ctl   transport.Identity
	agent *pcAgent
	mac   string
	hooks xfer.Hooks
	drop  string
	// rewrite, when set, gets each request on the rpc stream with the
	// agent's answer and returns the answer the daemon reads.
	rewrite func(req, res proto.Message) proto.Message
	// gate, when set, holds every reconnect until it is closed.
	gateMu sync.Mutex
	gate   chan struct{}

	mu     sync.Mutex
	d      *daemonSide
	events []events.Event
}

func newID(t *testing.T, cn string) transport.Identity {
	t.Helper()
	id, err := transport.NewIdentity(cn)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// newHarness starts an agent and a manager whose host pc is that agent.
func newHarness(t *testing.T, hooks xfer.Hooks) *harness {
	t.Helper()
	return newHarnessDropping(t, hooks, "")
}

// newHarnessDropping is newHarness whose connection drops the answer to the
// first request of the op drop.
func newHarnessDropping(t *testing.T, hooks xfer.Hooks, drop string) *harness {
	t.Helper()
	return newHarnessWith(t, hooks, drop, nil)
}

// newHarnessRewriting is newHarness whose daemon reads the agent's answers
// through rewrite.
func newHarnessRewriting(t *testing.T, hooks xfer.Hooks, rewrite func(req, res proto.Message) proto.Message) *harness {
	t.Helper()
	return newHarnessWith(t, hooks, "", rewrite)
}

func newHarnessWith(t *testing.T, hooks xfer.Hooks, drop string, rewrite func(req, res proto.Message) proto.Message) *harness {
	t.Helper()
	t.Setenv("DESKHAND_HOME", t.TempDir())
	h := &harness{t: t, db: filepath.Join(t.TempDir(), "deskhand.db"), ctl: newID(t, "controller"), mac: t.TempDir(), hooks: hooks, drop: drop,
		rewrite: rewrite}
	h.agent = &pcAgent{t: t, port: freeUDPPort(t), id: newID(t, "agent"), home: t.TempDir(), dataDir: t.TempDir(), trusted: h.ctl.Fingerprint}
	h.agent.start()
	t.Cleanup(h.agent.halt)
	st, err := store.Open(h.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutHost(store.Host{HostID: "pc", Name: "pc", Address: "127.0.0.1", Port: h.agent.port, AgentFP: h.agent.id.Fingerprint,
		OS: "darwin", Arch: "arm64", SSHAlias: "pc", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	h.open()
	t.Cleanup(h.closeDaemon)
	return h
}

// open starts the daemon side over the stored database, as a daemon start
// does: the manager loads the stored transfers and the host connects.
func (h *harness) open() {
	h.t.Helper()
	st, err := store.Open(h.db)
	if err != nil {
		h.t.Fatal(err)
	}
	bus := events.NewBus(st, quiet)
	sub := bus.Subscribe()
	d := &daemonSide{st: st, bus: bus}
	d.conns = conn.NewManager(conn.Options{Identity: h.ctl, Controller: "test", Requests: st, Logger: quiet, DropAfterSend: h.drop,
		ReadyWait: 3 * time.Second, Dial: h.dialer(),
		OnReady: func(hostID string, _ uint64) {
			if m := h.manager(); m != nil {
				go m.HostReady(hostID)
			}
		},
		Sleep: func(ctx context.Context, wait time.Duration) error {
			h.gateMu.Lock()
			gate := h.gate
			h.gateMu.Unlock()
			if gate != nil {
				select {
				case <-gate:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			select {
			case <-time.After(wait):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}})
	d.jobs = jobs.New(st, bus, d.conns)
	d.m = xfer.New(xfer.Options{Store: st, Bus: bus, Conns: d.conns, Jobs: d.jobs, Logger: quiet, Hooks: h.hooks})
	go func() {
		for ev := range sub.C {
			h.mu.Lock()
			h.events = append(h.events, ev)
			h.mu.Unlock()
		}
	}()
	h.mu.Lock()
	h.d = d
	h.mu.Unlock()
	d.m.Load()
	row, err := st.GetHost("pc")
	if err != nil {
		h.t.Fatal(err)
	}
	d.conns.Add(conn.Target{HostID: row.HostID, Name: row.Name, Address: row.Address, Port: row.Port, Fingerprint: row.AgentFP}).Connect()
}

// dialer returns the QUIC dialer whose rpc stream answers go through
// h.rewrite, or nil without one.
func (h *harness) dialer() conn.Dialer {
	if h.rewrite == nil {
		return nil
	}
	return func(ctx context.Context, t conn.Target, id transport.Identity) (conn.Session, error) {
		sess, err := conn.DialQUIC(ctx, t, id)
		if err != nil {
			return nil, err
		}
		return &rewriteSession{Session: sess, rewrite: h.rewrite}, nil
	}
}

// rewriteSession is a connection whose first stream, the rpc stream, is a
// rewriteStream; the data streams pass through.
type rewriteSession struct {
	conn.Session
	rewrite func(req, res proto.Message) proto.Message

	mu     sync.Mutex
	opened bool
}

func (s *rewriteSession) OpenStream(ctx context.Context) (conn.Stream, error) {
	st, err := s.Session.OpenStream(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	first := !s.opened
	s.opened = true
	s.mu.Unlock()
	if !first {
		return st, nil
	}
	return &rewriteStream{Stream: st, rewrite: s.rewrite, br: bufio.NewReader(st), reqs: map[string]proto.Message{}}, nil
}

// rewriteStream records the requests the daemon writes and hands each
// answer line with its request to rewrite before the daemon reads it.
type rewriteStream struct {
	conn.Stream
	rewrite func(req, res proto.Message) proto.Message
	br      *bufio.Reader
	rbuf    []byte

	mu   sync.Mutex
	wbuf []byte
	reqs map[string]proto.Message
}

func (s *rewriteStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.wbuf = append(s.wbuf, p...)
	for {
		i := bytes.IndexByte(s.wbuf, '\n')
		if i < 0 {
			break
		}
		var m proto.Message
		if json.Unmarshal(s.wbuf[:i], &m) == nil && m.Type() == "req" {
			s.reqs[fmt.Sprint(m["id"])] = m
		}
		s.wbuf = s.wbuf[i+1:]
	}
	s.mu.Unlock()
	return s.Stream.Write(p)
}

func (s *rewriteStream) Read(p []byte) (int, error) {
	if len(s.rbuf) == 0 {
		line, err := s.br.ReadBytes('\n')
		if err != nil {
			s.rbuf = line
			n := copy(p, s.rbuf)
			s.rbuf = s.rbuf[n:]
			return n, err
		}
		s.rbuf = s.rewriteLine(line)
	}
	n := copy(p, s.rbuf)
	s.rbuf = s.rbuf[n:]
	return n, nil
}

// rewriteLine returns the line the daemon reads for the agent's line.
func (s *rewriteStream) rewriteLine(line []byte) []byte {
	var m proto.Message
	if json.Unmarshal(line, &m) != nil || m.Type() != "res" {
		return line
	}
	key := fmt.Sprint(m["id"])
	s.mu.Lock()
	req := s.reqs[key]
	delete(s.reqs, key)
	s.mu.Unlock()
	if req == nil {
		return line
	}
	b, err := json.Marshal(s.rewrite(req, m))
	if err != nil {
		return line
	}
	return append(b, '\n')
}

func (h *harness) manager() *xfer.Manager {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.d == nil {
		return nil
	}
	return h.d.m
}

// closeDaemon stops the daemon side as a daemon stop does.
func (h *harness) closeDaemon() {
	h.mu.Lock()
	d := h.d
	h.d = nil
	h.mu.Unlock()
	if d == nil {
		return
	}
	d.m.Close()
	d.jobs.Close()
	d.conns.Close()
	d.st.Close()
}

// holdReconnects keeps the connection from coming back until the returned
// function runs.
func (h *harness) holdReconnects() func() {
	gate := make(chan struct{})
	h.gateMu.Lock()
	h.gate = gate
	h.gateMu.Unlock()
	return func() {
		h.gateMu.Lock()
		h.gate = nil
		h.gateMu.Unlock()
		close(gate)
	}
}

func (h *harness) hostState() conn.ConnState {
	h.mu.Lock()
	d := h.d
	h.mu.Unlock()
	if hc := d.conns.Get("pc"); hc != nil {
		return hc.State()
	}
	return conn.StateOffline
}

var human = model.Caller{Actor: model.ActorHuman, ClientID: "c-test", ClientName: "test"}

// start starts a transfer and fails the test unless it started.
func (h *harness) start(direction, local, remote, onConflict string) model.TransferDetail {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := h.manager().Start(ctx, xfer.StartParams{HostID: "pc", Direction: direction, Local: local, Remote: remote, OnConflict: onConflict}, human)
	if err != nil {
		h.t.Fatalf("start %s %s %s: %v", direction, local, remote, err)
	}
	if res.Err != nil {
		h.t.Fatalf("start %s %s %s did not start: %+v", direction, local, remote, res.Err)
	}
	if res.Transfer.TransferID == "" || res.Transfer.Rev < 1 || res.JobID == "" {
		h.t.Fatalf("started transfer %+v", res.Transfer)
	}
	return res.Transfer
}

func (h *harness) get(id string) model.TransferDetail {
	h.t.Helper()
	tr, err := h.manager().Get(id)
	if err != nil {
		h.t.Fatalf("get %s: %v", id, err)
	}
	return tr
}

// wait polls the transfer until ok accepts it, for at most limit.
func (h *harness) wait(id string, limit time.Duration, what string, ok func(model.TransferDetail) bool) model.TransferDetail {
	h.t.Helper()
	deadline := time.Now().Add(limit)
	for {
		tr := h.get(id)
		if ok(tr) {
			return tr
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("transfer %s is not %s after %s: %s", id, what, limit, describe(tr))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func finished(tr model.TransferDetail) bool {
	return tr.State == "done" || tr.State == "failed" || tr.State == "cancelled"
}

func (h *harness) waitFinished(id string) model.TransferDetail {
	h.t.Helper()
	return h.wait(id, 3*time.Minute, "finished", finished)
}

// describe returns the state of tr and of its files for a failure message.
func describe(tr model.TransferDetail) string {
	var b strings.Builder
	fmt.Fprintf(&b, "state %s outcome %q error %+v bytes %d/%d files %d/%d rev %d", tr.State, tr.Outcome, tr.Error, tr.BytesDone, tr.BytesTotal,
		tr.FilesDone, tr.FilesTotal, tr.Rev)
	for i, f := range tr.Files {
		if i == 8 {
			fmt.Fprintf(&b, "\n  … %d more", len(tr.Files)-i)
			break
		}
		fmt.Fprintf(&b, "\n  %s: %s %q %d/%d path %q error %+v", f.Rel, f.State, f.Outcome, f.BytesDone, f.Size, f.Path, f.Error)
	}
	return b.String()
}

// fileOf returns the file rel of tr.
func fileOf(t *testing.T, tr model.TransferDetail, rel string) model.TransferFile {
	t.Helper()
	for _, f := range tr.Files {
		if f.Rel == rel {
			return f
		}
	}
	t.Fatalf("transfer has no file %s: %s", rel, describe(tr))
	return model.TransferFile{}
}

// updates returns the transfer.updated events of the transfer id so far.
func (h *harness) updates(id string) []model.Transfer {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []model.Transfer
	for _, ev := range h.events {
		if ev.Kind != events.TransferUpdated {
			continue
		}
		tr, ok := ev.Fields["transfer"].(model.Transfer)
		if ok && tr.TransferID == id {
			out = append(out, tr)
		}
	}
	return out
}

// timeline returns the timeline entries recorded so far.
func (h *harness) timeline() []model.TimelineEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []model.TimelineEntry
	for _, ev := range h.events {
		if ev.Kind == events.TimelineAppended {
			out = append(out, ev.Fields["entry"].(model.TimelineEntry))
		}
	}
	return out
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// tree returns the files and folders below root: relative slash path to
// content, folders as "/".
func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			out[rel] = "/"
			return nil
		}
		out[rel] = string(readFile(t, p))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// parts returns the part files below root.
func parts(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.Contains(d.Name(), ".deskhand-part-") {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// gate blocks a hook until it is opened; reached is closed when the hook
// first waits on it.
type gate struct {
	once    sync.Once
	reached chan struct{}
	open    chan struct{}
}

func newGate() *gate { return &gate{reached: make(chan struct{}), open: make(chan struct{})} }

func (g *gate) wait() {
	g.once.Do(func() { close(g.reached) })
	<-g.open
}

func (g *gate) release() {
	select {
	case <-g.open:
	default:
		close(g.open)
	}
}

func sameBytes(t *testing.T, what string, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Fatalf("%s: %d bytes with sha256 %s, want %d bytes with %s", what, len(got), sum(got), len(want), sum(want))
	}
}
