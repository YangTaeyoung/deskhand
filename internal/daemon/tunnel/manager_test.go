package tunnel

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	agent "github.com/YangTaeyoung/deskhand/internal/agent/tunnel"
	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// tcpStream is a TCP connection as the daemon's and the agent's end of a
// data stream.
type tcpStream struct{ *net.TCPConn }

func (s tcpStream) CloseWrite() error { return s.TCPConn.CloseWrite() }
func (s tcpStream) CancelWrite()      { s.TCPConn.Close() }
func (s tcpStream) CancelRead()       { s.TCPConn.CloseRead() }

// agentStream is the agent's end: Close ends its direction only.
type agentStream struct{ tcpStream }

func (s agentStream) Close() error { return s.TCPConn.CloseWrite() }

// fakeHost is a host whose tunnel streams end in the tunnel handler of the
// agent package, over loopback TCP.
type fakeHost struct {
	mu     sync.Mutex
	state  conn.ConnState
	caps   map[string]conn.Capability
	agentL net.Listener
}

func newFakeHost(t *testing.T) *fakeHost {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	h := &fakeHost{state: conn.StateReady, caps: map[string]conn.Capability{"tunnel": {State: "ready"}}, agentL: l}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				br := bufio.NewReader(c)
				var hdr wire.StreamHeader
				if wire.ReadHeader(br, &hdr) != nil {
					c.Close()
					return
				}
				agent.Serve(context.Background(), agentStream{tcpStream{c.(*net.TCPConn)}}, hdr)
				c.Close()
			}()
		}
	}()
	return h
}

func (h *fakeHost) ID() string          { return "h-fake" }
func (h *fakeHost) Target() conn.Target { return conn.Target{Name: "fake"} }
func (h *fakeHost) State() conn.ConnState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state
}
func (h *fakeHost) setState(s conn.ConnState) {
	h.mu.Lock()
	h.state = s
	h.mu.Unlock()
}
func (h *fakeHost) Hello() conn.Hello { return conn.Hello{Caps: h.caps} }
func (h *fakeHost) OpenStream(ctx context.Context, hdr wire.StreamHeader) (conn.DataStream, uint64, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", h.agentL.Addr().String())
	if err != nil {
		return nil, 0, err
	}
	if err := wire.WriteHeader(c, hdr); err != nil {
		c.Close()
		return nil, 0, err
	}
	return tcpStream{c.(*net.TCPConn)}, 1, nil
}

// echoServer is a TCP echo server on a loopback port.
func echoServer(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				io.Copy(c, c)
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func newManager(t *testing.T) *Manager {
	t.Helper()
	m := New(nil)
	t.Cleanup(m.Shutdown)
	return m
}

func localAddr(i Info) string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(i.LocalPort)) }

// roundTrip sends msg through the tunnel and reads its echo.
func roundTrip(t *testing.T, i Info, msg string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", localAddr(i), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(c, got); err != nil || string(got) != msg {
		t.Fatalf("echo %q: %v", got, err)
	}
}

// refused reports whether nothing listens on the local port of i.
func refused(i Info) bool {
	c, err := net.DialTimeout("tcp", localAddr(i), time.Second)
	if err == nil {
		c.Close()
		return false
	}
	return true
}

func TestTunnelBindsOnlyTheLoopbackAddress(t *testing.T) {
	m, h := newManager(t), newFakeHost(t)
	i, err := m.Open(h, OpenParams{RemotePort: echoServer(t), Owner: OwnerUser})
	if err != nil {
		t.Fatal(err)
	}
	if want := "127.0.0.1:" + strconv.Itoa(i.LocalPort); i.LocalAddress != want || i.LocalPort == 0 {
		t.Fatalf("local address %q, want %q", i.LocalAddress, want)
	}
	roundTrip(t, i, "ping")
}

func TestOpenAtAGivenLocalPortAndPortConflict(t *testing.T) {
	m, h := newManager(t), newFakeHost(t)
	remote := echoServer(t)
	ports, err := agent.FreePorts(1)
	if err != nil {
		t.Fatal(err)
	}
	i, err := m.Open(h, OpenParams{RemotePort: remote, LocalPort: ports[0], Owner: OwnerUser})
	if err != nil || i.LocalPort != ports[0] {
		t.Fatalf("open at %d: %+v, %v", ports[0], i, err)
	}
	if _, err := m.Open(h, OpenParams{RemotePort: remote, LocalPort: ports[0], Owner: OwnerUser}); apierr.CodeOf(err) != wire.CodePortConflict {
		t.Fatalf("a second open at the same local port: %v", err)
	}
}

func TestOpenRefusesBadPortsAndOwners(t *testing.T) {
	m, h := newManager(t), newFakeHost(t)
	for _, p := range []OpenParams{
		{RemotePort: 0, Owner: OwnerUser}, {RemotePort: 70000, Owner: OwnerUser}, {RemotePort: 80, LocalPort: -1, Owner: OwnerUser},
		{RemotePort: 80, LocalPort: 65536, Owner: OwnerUser}, {RemotePort: 80, Owner: ""}, {RemotePort: 80, Owner: "debug:"}, {RemotePort: 80, Owner: "other"},
	} {
		if _, err := m.Open(h, p); apierr.CodeOf(err) != wire.CodeInvalidArgument {
			t.Fatalf("open %+v: %v", p, err)
		}
	}
	if got := m.List(""); len(got) != 0 {
		t.Fatalf("tunnels after refused opens: %+v", got)
	}
}

func TestOpenNeedsAReadyHostWithTheTunnelCapability(t *testing.T) {
	m, h := newManager(t), newFakeHost(t)
	h.setState(conn.StateOffline)
	if _, err := m.Open(h, OpenParams{RemotePort: 80, Owner: OwnerUser}); apierr.CodeOf(err) != wire.CodeOffline {
		t.Fatalf("open on an offline host: %v", err)
	}
	h.setState(conn.StateReady)
	h.caps = map[string]conn.Capability{"tunnel": {State: "unsupported", Reason: "not implemented in this agent version"}}
	if _, err := m.Open(h, OpenParams{RemotePort: 80, Owner: OwnerUser}); apierr.CodeOf(err) != wire.CodeCapabilityUnsupported {
		t.Fatalf("open on an agent without tunnels: %v", err)
	}
}

func TestListCountsConnectionsAndBytes(t *testing.T) {
	m, h := newManager(t), newFakeHost(t)
	i, err := m.Open(h, OpenParams{RemotePort: echoServer(t), Owner: OwnerUser})
	if err != nil {
		t.Fatal(err)
	}
	hold, err := net.Dial("tcp", localAddr(i))
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	hold.Write(bytes.Repeat([]byte("z"), 1000))
	io.ReadFull(hold, make([]byte, 1000))
	roundTrip(t, i, "abc")
	var got Info
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		list := m.List(h.ID())
		if len(list) != 1 {
			t.Fatalf("list %+v", list)
		}
		got = list[0]
		if got.TotalConnections == 2 && got.Connections == 1 && got.BytesToRemote == 1003 && got.BytesFromRemote == 1003 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("counters %+v", got)
		}
	}
	if got.TunnelID != i.TunnelID || got.HostID != h.ID() || got.RemotePort != i.RemotePort || got.Owner != OwnerUser || got.CreatedAt.IsZero() {
		t.Fatalf("list entry %+v, opened %+v", got, i)
	}
	if other := m.List("h-other"); len(other) != 0 {
		t.Fatalf("list of another host %+v", other)
	}
}

func TestCloseEndsTheListenerAndItsConnections(t *testing.T) {
	m, h := newManager(t), newFakeHost(t)
	i, err := m.Open(h, OpenParams{RemotePort: echoServer(t), Owner: OwnerUser})
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", localAddr(i))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("hi"))
	io.ReadFull(c, make([]byte, 2))
	if _, err := m.Close(i.TunnelID); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil || isTimeout(err) {
		t.Fatalf("a connection of a closed tunnel read %v", err)
	}
	if !refused(i) {
		t.Fatal("the listener of a closed tunnel still accepts")
	}
	if _, err := m.Close(i.TunnelID); apierr.CodeOf(err) != wire.CodeNotFound {
		t.Fatalf("closing a closed tunnel: %v", err)
	}
	if len(m.List("")) != 0 {
		t.Fatal("a closed tunnel is still listed")
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

func TestCloseOwnerClosesOnlyThatOwnersTunnels(t *testing.T) {
	m, h := newManager(t), newFakeHost(t)
	remote := echoServer(t)
	open := func(owner string) Info {
		i, err := m.Open(h, OpenParams{RemotePort: remote, Owner: owner})
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	user, d1a, d1b, d2 := open(OwnerUser), open("debug:d-1"), open("debug:d-1"), open("debug:d-2")
	if n := m.CloseOwner("debug:d-1"); n != 2 {
		t.Fatalf("CloseOwner closed %d tunnels, want 2", n)
	}
	if !refused(d1a) || !refused(d1b) {
		t.Fatal("a tunnel of the closed owner still accepts")
	}
	roundTrip(t, user, "u")
	roundTrip(t, d2, "d")
	if n := m.CloseOwner("debug:d-1"); n != 0 {
		t.Fatalf("CloseOwner closed %d tunnels the second time", n)
	}
}

func TestCloseHostClosesItsTunnels(t *testing.T) {
	m, h := newManager(t), newFakeHost(t)
	i, err := m.Open(h, OpenParams{RemotePort: echoServer(t), Owner: OwnerUser})
	if err != nil {
		t.Fatal(err)
	}
	if n := m.CloseHost("h-other"); n != 0 {
		t.Fatalf("CloseHost of another host closed %d", n)
	}
	if n := m.CloseHost(h.ID()); n != 1 || !refused(i) {
		t.Fatalf("CloseHost closed %d, refused %v", n, refused(i))
	}
}

func TestShutdownClosesEveryTunnel(t *testing.T) {
	m, h := New(nil), newFakeHost(t)
	i, err := m.Open(h, OpenParams{RemotePort: echoServer(t), Owner: OwnerUser})
	if err != nil {
		t.Fatal(err)
	}
	m.Shutdown()
	if !refused(i) {
		t.Fatal("the listener survived Shutdown")
	}
	if _, err := m.Open(h, OpenParams{RemotePort: 80, Owner: OwnerUser}); err == nil {
		t.Fatal("Open after Shutdown succeeded")
	}
}

func TestConnectionsFailFastWhileTheHostIsOfflineAndWorkAfterwards(t *testing.T) {
	m, h := newManager(t), newFakeHost(t)
	i, err := m.Open(h, OpenParams{RemotePort: echoServer(t), Owner: OwnerUser})
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, i, "before")
	h.setState(conn.StateReconnecting)
	c, err := net.Dial("tcp", localAddr(i))
	if err != nil {
		t.Fatalf("the listener closed with the host: %v", err)
	}
	start := time.Now()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil || isTimeout(err) || time.Since(start) > 2*time.Second {
		t.Fatalf("a connection while the host is offline: read %v after %s", err, time.Since(start))
	}
	c.Close()
	var info Info
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if info = m.List("")[0]; info.FailedConnections == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed connections %+v", info)
		}
	}
	if info.LastError == "" {
		t.Fatal("a failed connection left no last_error")
	}
	h.setState(conn.StateReady)
	roundTrip(t, i, "after")
}

func TestARefusedConnectionIsCountedWithTheAgentsReason(t *testing.T) {
	m, h := newManager(t), newFakeHost(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	i, err := m.Open(h, OpenParams{RemotePort: port, Owner: OwnerUser})
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", localAddr(i))
	if err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil || isTimeout(err) {
		t.Fatalf("a connection to a closed remote port read %v", err)
	}
	c.Close()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		info := m.List("")[0]
		if info.FailedConnections == 1 && info.LastError != "" {
			if want := strconv.Itoa(port); !bytes.Contains([]byte(info.LastError), []byte(want)) {
				t.Fatalf("last_error %q does not name port %s", info.LastError, want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("list %+v", info)
		}
	}
}

func newLimited(t *testing.T, lim Limits) *Manager {
	t.Helper()
	m := NewWithLimits(nil, lim)
	t.Cleanup(m.Shutdown)
	return m
}

// holdServer accepts connections and reads them to the end without
// answering or closing; it closes them when the test ends.
func holdServer(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	t.Cleanup(func() {
		close(stop)
		l.Close()
	})
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				io.Copy(io.Discard, c)
				<-stop
				c.Close()
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func waitFailed(t *testing.T, m *Manager, id string, want int64) Info {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		info, _ := m.Get(id)
		if info.FailedConnections == want {
			return info
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed connections %+v, want %d", info, want)
		}
	}
}

// expectClosed reports whether c is closed by the peer within d.
func expectClosed(c net.Conn, d time.Duration) bool {
	c.SetReadDeadline(time.Now().Add(d))
	_, err := c.Read(make([]byte, 1))
	return err != nil && !isTimeout(err)
}

func TestOpenRefusesTunnelsBeyondTheHostLimit(t *testing.T) {
	lim := DefaultLimits
	lim.TunnelsPerHost = 3
	m, h := newLimited(t, lim), newFakeHost(t)
	remote := echoServer(t)
	var first Info
	for i := range 3 {
		info, err := m.Open(h, OpenParams{RemotePort: remote, Owner: OwnerUser})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = info
		}
	}
	_, err := m.Open(h, OpenParams{RemotePort: remote, Owner: OwnerUser})
	if apierr.CodeOf(err) != wire.CodeInvalidArgument || !strings.Contains(err.Error(), "3개") {
		t.Fatalf("the fourth tunnel: %v", err)
	}
	if _, err := m.Close(first.TunnelID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Open(h, OpenParams{RemotePort: remote, Owner: OwnerUser}); err != nil {
		t.Fatalf("open after a close: %v", err)
	}
}

func TestConnectionsBeyondTheTunnelLimitAreClosedAndCounted(t *testing.T) {
	lim := DefaultLimits
	lim.ConnsPerTunnel = 2
	m, h := newLimited(t, lim), newFakeHost(t)
	i, err := m.Open(h, OpenParams{RemotePort: echoServer(t), Owner: OwnerUser})
	if err != nil {
		t.Fatal(err)
	}
	var held []net.Conn
	for range 2 {
		c, err := net.Dial("tcp", localAddr(i))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.Write([]byte("x"))
		io.ReadFull(c, make([]byte, 1))
		held = append(held, c)
	}
	extra, err := net.Dial("tcp", localAddr(i))
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	if !expectClosed(extra, 5*time.Second) {
		t.Fatal("a connection beyond the tunnel limit stayed open")
	}
	info := waitFailed(t, m, i.TunnelID, 1)
	if !strings.Contains(info.LastError, "2개") || info.Connections != 2 {
		t.Fatalf("tunnel after the refused connection %+v", info)
	}
	held[0].Close()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if got, _ := m.Get(i.TunnelID); got.Connections == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the closed connection was not released")
		}
	}
	roundTrip(t, i, "again")
}

func TestConnectionsBeyondTheHostStreamLimitAreClosedAcrossTunnels(t *testing.T) {
	lim := DefaultLimits
	lim.StreamsPerHost = 2
	m, h := newLimited(t, lim), newFakeHost(t)
	remote := echoServer(t)
	a, err := m.Open(h, OpenParams{RemotePort: remote, Owner: OwnerUser})
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Open(h, OpenParams{RemotePort: remote, Owner: OwnerUser})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []Info{a, b} {
		c, err := net.Dial("tcp", localAddr(i))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.Write([]byte("x"))
		io.ReadFull(c, make([]byte, 1))
	}
	extra, err := net.Dial("tcp", localAddr(a))
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	if !expectClosed(extra, 5*time.Second) {
		t.Fatal("a connection beyond the host limit stayed open")
	}
	if info := waitFailed(t, m, a.TunnelID, 1); !strings.Contains(info.LastError, "2개") {
		t.Fatalf("last_error %q", info.LastError)
	}
}

func TestHalfClosedConnectionExpiresWhenTheOpenDirectionIsIdle(t *testing.T) {
	lim := DefaultLimits
	lim.HalfClosedIdle = 300 * time.Millisecond
	m, h := newLimited(t, lim), newFakeHost(t)
	i, err := m.Open(h, OpenParams{RemotePort: holdServer(t), Owner: OwnerUser})
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", localAddr(i))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("data"))
	c.(*net.TCPConn).CloseWrite()
	if !expectClosed(c, 5*time.Second) {
		t.Fatal("a half-closed idle connection was not closed")
	}
}

func TestFullyIdleConnectionIsNotExpired(t *testing.T) {
	lim := DefaultLimits
	lim.HalfClosedIdle = 100 * time.Millisecond
	m, h := newLimited(t, lim), newFakeHost(t)
	i, err := m.Open(h, OpenParams{RemotePort: echoServer(t), Owner: OwnerUser})
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", localAddr(i))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	time.Sleep(700 * time.Millisecond)
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte("late")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(c, got); err != nil || string(got) != "late" {
		t.Fatalf("echo after the idle time: %q, %v", got, err)
	}
}
