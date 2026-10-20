// Package tunnel keeps the loopback TCP tunnels of the daemon: each tunnel
// listens on 127.0.0.1 of this Mac and carries every accepted connection
// over a tunnel stream to a loopback port of a PC (docs/protocol.md §2,
// docs/local-api.md tunnel.*).
package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/ids"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// OwnerUser is the owner of the tunnels a user or an AI client opens.
	OwnerUser = "user"
	// OwnerDebugPrefix starts the owner of the tunnels of a debug session:
	// "debug:<debug_id>".
	OwnerDebugPrefix = "debug:"

	// openTimeout bounds the open of a tunnel stream and the wait for the
	// agent's acceptance line.
	openTimeout = 15 * time.Second
	// copyBuffer is the size of the buffer of one copy direction.
	copyBuffer = 64 << 10
)

// Host is the PC connection a tunnel uses; *conn.Host is one.
type Host interface {
	ID() string
	Target() conn.Target
	State() conn.ConnState
	Hello() conn.Hello
	OpenStream(ctx context.Context, hdr wire.StreamHeader) (conn.DataStream, uint64, error)
}

// OpenParams are the parameters of Open. LocalPort 0 picks a free port.
type OpenParams struct {
	RemotePort int
	LocalPort  int
	Owner      string
}

// Info describes a tunnel.
type Info struct {
	TunnelID          string    `json:"tunnel_id"`
	HostID            string    `json:"host_id"`
	RemotePort        int       `json:"remote_port"`
	LocalPort         int       `json:"local_port"`
	LocalAddress      string    `json:"local_address"`
	Owner             string    `json:"owner"`
	CreatedAt         time.Time `json:"created_at"`
	Connections       int       `json:"connections"`
	TotalConnections  int64     `json:"total_connections"`
	FailedConnections int64     `json:"failed_connections"`
	BytesToRemote     int64     `json:"bytes_to_remote"`
	BytesFromRemote   int64     `json:"bytes_from_remote"`
	LastError         string    `json:"last_error,omitempty"`
}

// Manager owns the tunnels.
type Manager struct {
	lg *log.Logger

	mu      sync.Mutex
	tunnels map[string]*tunnel
	closed  bool
}

// New returns a Manager that logs to lg; nil discards the log.
func New(lg *log.Logger) *Manager {
	if lg == nil {
		lg = log.New(io.Discard, "", 0)
	}
	return &Manager{lg: lg, tunnels: map[string]*tunnel{}}
}

// tunnel is one listener with its connections.
type tunnel struct {
	m       *Manager
	id      string
	host    Host
	hostID  string
	remote  int
	local   int
	owner   string
	created time.Time
	ln      net.Listener
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	active   atomic.Int64
	total    atomic.Int64
	failed   atomic.Int64
	toRemote atomic.Int64
	fromRem  atomic.Int64

	mu      sync.Mutex
	conns   map[*relay]struct{}
	lastErr string
}

func validOwner(owner string) bool {
	return owner == OwnerUser || strings.HasPrefix(owner, OwnerDebugPrefix) && len(owner) > len(OwnerDebugPrefix)
}

func validPort(p int) bool { return p >= 1 && p <= 65535 }

// Open listens on 127.0.0.1:p.LocalPort and starts carrying the accepted
// connections to port p.RemotePort of h's loopback. The host has to be
// connected and its agent has to serve tunnels.
func (m *Manager) Open(h Host, p OpenParams) (Info, error) {
	switch {
	case !validPort(p.RemotePort):
		return Info{}, apierr.Invalid(fmt.Sprintf("remote_port %d는 1~65535 사이여야 합니다. PC에서 열려 있는 포트 번호를 주세요", p.RemotePort))
	case p.LocalPort != 0 && !validPort(p.LocalPort):
		return Info{}, apierr.Invalid(fmt.Sprintf("local_port %d는 0(자동)이거나 1~65535 사이여야 합니다. 비워 두면 빈 포트를 고릅니다", p.LocalPort))
	case !validOwner(p.Owner):
		return Info{}, apierr.Invalid(fmt.Sprintf("터널 소유자 %q가 올바르지 않습니다. user나 debug:<debug_id>를 주세요", p.Owner))
	}
	if st := h.State(); st != conn.StateReady && st != conn.StateDegraded {
		return Info{}, apierr.New(wire.CodeOffline, fmt.Sprintf("%s PC와 연결돼 있지 않아 터널을 열 수 없습니다. deskhand host connect %s로 연결한 뒤 다시 시도하세요", h.Target().Name, h.Target().Name),
			true, apierr.NextHostConnect)
	}
	if c := h.Hello().Caps["tunnel"]; c.State != "ready" {
		reason := c.Reason
		if reason == "" {
			reason = "에이전트가 터널을 지원하지 않습니다"
		}
		return Info{}, apierr.New(wire.CodeCapabilityUnsupported, fmt.Sprintf("%s PC의 터널을 쓸 수 없습니다(%s). deskhand host repair로 에이전트를 확인하세요", h.Target().Name, reason),
			false, apierr.NextHostRepair)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p.LocalPort)))
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return Info{}, apierr.New(wire.CodePortConflict, fmt.Sprintf("이 Mac의 포트 %d를 이미 다른 프로그램이 쓰고 있습니다. --local을 비우거나 다른 포트를 주세요", p.LocalPort), false, apierr.NextFixParams)
		}
		return Info{}, apierr.Invalid(fmt.Sprintf("이 Mac의 127.0.0.1:%d를 열지 못했습니다(%v). 다른 포트를 주세요", p.LocalPort, err))
	}
	t := &tunnel{m: m, id: ids.New(ids.Tunnel), host: h, hostID: h.ID(), remote: p.RemotePort, local: ln.Addr().(*net.TCPAddr).Port,
		owner: p.Owner, created: time.Now(), ln: ln, conns: map[*relay]struct{}{}}
	t.ctx, t.cancel = context.WithCancel(context.Background())
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		ln.Close()
		return Info{}, apierr.Internal("데몬이 종료 중이라 터널을 열 수 없습니다. 데몬을 다시 시작한 뒤 시도하세요")
	}
	m.tunnels[t.id] = t
	m.mu.Unlock()
	t.wg.Add(1)
	go t.accept()
	return t.info(), nil
}

// Close closes the tunnel id: its listener and every connection.
func (m *Manager) Close(id string) (Info, error) {
	m.mu.Lock()
	t := m.tunnels[id]
	delete(m.tunnels, id)
	m.mu.Unlock()
	if t == nil {
		return Info{}, apierr.NotFound(fmt.Sprintf("터널 %s가 없습니다. tunnel.list로 열린 터널을 확인하세요", id))
	}
	info := t.info()
	t.shut()
	return info, nil
}

// CloseOwner closes the tunnels of owner and returns how many it closed.
func (m *Manager) CloseOwner(owner string) int {
	return m.closeWhere(func(t *tunnel) bool { return t.owner == owner })
}

// CloseHost closes the tunnels of the host hostID and returns how many it
// closed.
func (m *Manager) CloseHost(hostID string) int {
	return m.closeWhere(func(t *tunnel) bool { return t.hostID == hostID })
}

func (m *Manager) closeWhere(match func(*tunnel) bool) int {
	m.mu.Lock()
	var doomed []*tunnel
	for id, t := range m.tunnels {
		if match(t) {
			doomed = append(doomed, t)
			delete(m.tunnels, id)
		}
	}
	m.mu.Unlock()
	for _, t := range doomed {
		t.shut()
	}
	return len(doomed)
}

// Shutdown closes every tunnel; Open fails afterwards.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.closeWhere(func(*tunnel) bool { return true })
}

// Get returns the tunnel id.
func (m *Manager) Get(id string) (Info, bool) {
	m.mu.Lock()
	t := m.tunnels[id]
	m.mu.Unlock()
	if t == nil {
		return Info{}, false
	}
	return t.info(), true
}

// FindLocal returns the tunnel that listens on the local port.
func (m *Manager) FindLocal(port int) (Info, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tunnels {
		if t.local == port {
			return t.info(), true
		}
	}
	return Info{}, false
}

// List returns the open tunnels of the host hostID, or of every host when
// hostID is empty, oldest first.
func (m *Manager) List(hostID string) []Info {
	m.mu.Lock()
	var list []*tunnel
	for _, t := range m.tunnels {
		if hostID == "" || t.hostID == hostID {
			list = append(list, t)
		}
	}
	m.mu.Unlock()
	out := make([]Info, 0, len(list))
	for _, t := range list {
		out = append(out, t.info())
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].TunnelID < out[j-1].TunnelID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (t *tunnel) info() Info {
	t.mu.Lock()
	last := t.lastErr
	t.mu.Unlock()
	return Info{TunnelID: t.id, HostID: t.hostID, RemotePort: t.remote, LocalPort: t.local,
		LocalAddress: net.JoinHostPort("127.0.0.1", strconv.Itoa(t.local)), Owner: t.owner, CreatedAt: t.created,
		Connections: int(t.active.Load()), TotalConnections: t.total.Load(), FailedConnections: t.failed.Load(),
		BytesToRemote: t.toRemote.Load(), BytesFromRemote: t.fromRem.Load(), LastError: last}
}

// shut closes the listener and every connection and waits for them.
func (t *tunnel) shut() {
	t.cancel()
	t.ln.Close()
	t.mu.Lock()
	rs := make([]*relay, 0, len(t.conns))
	for r := range t.conns {
		rs = append(rs, r)
	}
	t.mu.Unlock()
	for _, r := range rs {
		r.abort()
	}
	t.wg.Wait()
}

func (t *tunnel) accept() {
	defer t.wg.Done()
	for {
		c, err := t.ln.Accept()
		if err != nil {
			return
		}
		t.total.Add(1)
		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			t.serve(c.(*net.TCPConn))
		}()
	}
}

// fail records why a connection could not be carried.
func (t *tunnel) fail(text string) {
	t.failed.Add(1)
	t.mu.Lock()
	t.lastErr = text
	t.mu.Unlock()
	t.m.lg.Printf("tunnel %s (127.0.0.1:%d -> %s port %d): %s", t.id, t.local, t.host.Target().Name, t.remote, text)
}

// serve carries one accepted connection over a new tunnel stream.
func (t *tunnel) serve(c *net.TCPConn) {
	name := t.host.Target().Name
	if st := t.host.State(); st != conn.StateReady && st != conn.StateDegraded {
		c.Close()
		t.fail(fmt.Sprintf("%s PC와 연결돼 있지 않아 연결을 닫았습니다. 연결이 돌아오면 같은 포트로 다시 접속하세요", name))
		return
	}
	ctx, cancel := context.WithTimeout(t.ctx, openTimeout)
	ds, _, err := t.host.OpenStream(ctx, wire.StreamHeader{S: "tunnel", Port: t.remote})
	cancel()
	if err != nil {
		c.Close()
		t.fail(fmt.Sprintf("%s PC에 터널 스트림을 열지 못했습니다(%s)", name, apierr.As(err).Message))
		return
	}
	r := &relay{tc: c, ds: ds}
	t.mu.Lock()
	t.conns[r] = struct{}{}
	t.mu.Unlock()
	t.active.Add(1)
	defer func() {
		t.active.Add(-1)
		t.mu.Lock()
		delete(t.conns, r)
		t.mu.Unlock()
	}()
	if t.ctx.Err() != nil {
		r.abort()
		return
	}
	br, refusal := readAcceptance(ds)
	if refusal != "" {
		r.abort()
		t.fail(refusal)
		return
	}
	r.run(br, &t.toRemote, &t.fromRem)
}

// readAcceptance reads the agent's first line of a tunnel stream and
// returns a reader of what follows it, or the text of a refusal.
func readAcceptance(ds conn.DataStream) (*bufio.Reader, string) {
	_ = ds.SetReadDeadline(time.Now().Add(openTimeout))
	br := bufio.NewReader(ds)
	var m proto.Message
	err := wire.ReadHeader(br, &m)
	_ = ds.SetReadDeadline(time.Time{})
	switch {
	case err != nil:
		return nil, fmt.Sprintf("PC가 터널 수락 줄을 보내지 않았습니다(%v)", err)
	case m["ok"] != true:
		text := m.String("error")
		if text == "" {
			text = "PC가 터널을 거절했습니다"
		}
		return nil, text
	}
	return br, ""
}

// relay joins one local TCP connection and one tunnel stream.
type relay struct {
	tc *net.TCPConn
	ds conn.DataStream
}

// abort ends both sides at once.
func (r *relay) abort() {
	r.tc.Close()
	r.ds.CancelRead()
	r.ds.CancelWrite()
}

// run copies both directions, counting bytes, until both end; a failed
// direction aborts the connection. Streams that ended cleanly are not reset.
func (r *relay) run(from *bufio.Reader, toRemote, fromRemote *atomic.Int64) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := io.CopyBuffer(counter{w: streamWriter{r.ds}, n: toRemote}, readerOnly{r.tc}, make([]byte, copyBuffer))
		if err != nil {
			r.abort()
			return
		}
		_ = r.ds.CloseWrite()
	}()
	go func() {
		defer wg.Done()
		_, err := io.CopyBuffer(counter{w: connWriter{r.tc}, n: fromRemote}, readerOnly{from}, make([]byte, copyBuffer))
		if err != nil {
			r.abort()
			return
		}
		_ = r.tc.CloseWrite()
	}()
	wg.Wait()
	r.tc.Close()
}

type readerOnly struct{ r io.Reader }

func (r readerOnly) Read(p []byte) (int, error) { return r.r.Read(p) }

type streamWriter struct{ ds conn.DataStream }

func (w streamWriter) Write(p []byte) (int, error) { return w.ds.Write(p) }

type connWriter struct{ c *net.TCPConn }

func (w connWriter) Write(p []byte) (int, error) { return w.c.Write(p) }

// counter adds the bytes written through it to n.
type counter struct {
	w io.Writer
	n *atomic.Int64
}

func (c counter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(int64(n))
	return n, err
}
