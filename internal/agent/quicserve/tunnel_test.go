package quicserve

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
	"github.com/quic-go/quic-go"
)

// tcpServer is a loopback TCP listener whose connections handle serves.
type tcpServer struct {
	ln       net.Listener
	accepted atomic.Int64
}

func listenTCP(t *testing.T, handle func(net.Conn)) *tcpServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &tcpServer{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.accepted.Add(1)
			go func() {
				defer c.Close()
				handle(c)
			}()
		}
	}()
	return s
}

func (s *tcpServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// echo writes back what it reads until the peer ends its direction, then
// ends its own.
func echo(c net.Conn) {
	io.Copy(c, c)
	c.(*net.TCPConn).CloseWrite()
}

// openTunnel opens a tunnel stream to port on conn and reads its
// acceptance line.
func openTunnel(t *testing.T, conn *quic.Conn, port int) (*quic.Stream, *bufio.Reader) {
	t.Helper()
	st := openStream(t, conn, wire.StreamHeader{S: "tunnel", Port: port})
	br := bufio.NewReader(st)
	if accept := readJSONLine(t, br); accept["ok"] != true {
		t.Fatalf("the tunnel to port %d answered %v", port, accept)
	}
	return st, br
}

func TestTunnelEchoRoundTrip(t *testing.T) {
	srv := listenTCP(t, echo)
	a := startAgentWith(t, TunnelStreams)
	conn := a.dial(t)
	openRPC(t, conn)
	st, br := openTunnel(t, conn, srv.port())
	for _, msg := range []string{"hello", "second line\n", string(bytes.Repeat([]byte("x"), 100000))} {
		if _, err := st.Write([]byte(msg)); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(msg))
		if _, err := io.ReadFull(br, got); err != nil || string(got) != msg {
			t.Fatalf("echo of %d bytes: %v", len(msg), err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if rest, err := io.ReadAll(br); err != nil || len(rest) != 0 {
		t.Fatalf("after the end of the tunnel: %d bytes, %v", len(rest), err)
	}
}

func TestNonLoopbackRefused(t *testing.T) {
	srv := listenTCP(t, echo)
	a := startAgentWith(t, TunnelStreams)
	conn := a.dial(t)
	openRPC(t, conn)
	for _, host := range []string{"10.0.0.5", "192.168.0.10", "example.com", "localhost", "0.0.0.0", "::", "127.0.0.2", "::ffff:10.0.0.5", " 127.0.0.1"} {
		got := readRefusal(t, openStream(t, conn, wire.StreamHeader{S: "tunnel", Host: host, Port: srv.port()}))
		if got.OK || got.Code != wire.CodePermissionDenied || got.Error == "" {
			t.Fatalf("host %q answered %+v", host, got)
		}
	}
	for _, port := range []int{0, -1, 65536, 1 << 20} {
		got := readRefusal(t, openStream(t, conn, wire.StreamHeader{S: "tunnel", Port: port}))
		if got.OK || got.Code != wire.CodeInvalidArgument || got.Error == "" {
			t.Fatalf("port %d answered %+v", port, got)
		}
	}
	if n := srv.accepted.Load(); n != 0 {
		t.Fatalf("the refused streams reached the loopback server %d times", n)
	}
	for _, host := range []string{"", "127.0.0.1", "::1"} {
		st := openStream(t, conn, wire.StreamHeader{S: "tunnel", Host: host, Port: srv.port()})
		if accept := readJSONLine(t, bufio.NewReader(st)); host != "::1" && accept["ok"] != true {
			t.Fatalf("host %q answered %v", host, accept)
		}
	}
}

func TestTunnelToAClosedPortIsRefusedNotFound(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	a := startAgentWith(t, TunnelStreams)
	conn := a.dial(t)
	openRPC(t, conn)
	got := readRefusal(t, openStream(t, conn, wire.StreamHeader{S: "tunnel", Port: port}))
	if got.OK || got.Code != wire.CodeNotFound || got.Error == "" {
		t.Fatalf("answer %+v", got)
	}
}

func TestTunnelHalfCloseBothWays(t *testing.T) {
	a := startAgentWith(t, TunnelStreams)
	conn := a.dial(t)
	openRPC(t, conn)

	counter := listenTCP(t, func(c net.Conn) {
		n, _ := io.Copy(io.Discard, c)
		fmt.Fprintf(c, "got %d", n)
	})
	st, br := openTunnel(t, conn, counter.port())
	if _, err := st.Write(bytes.Repeat([]byte("a"), 12345)); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if reply, err := io.ReadAll(br); err != nil || string(reply) != "got 12345" {
		t.Fatalf("reply after the client ended its direction: %q, %v", reply, err)
	}

	received := make(chan string, 1)
	speaker := listenTCP(t, func(c net.Conn) {
		c.Write([]byte("bye"))
		c.(*net.TCPConn).CloseWrite()
		b, _ := io.ReadAll(c)
		received <- string(b)
	})
	st, br = openTunnel(t, conn, speaker.port())
	first := make([]byte, 3)
	if _, err := io.ReadFull(br, first); err != nil || string(first) != "bye" {
		t.Fatalf("first words %q, %v", first, err)
	}
	if _, err := st.Write([]byte("after")); err != nil {
		t.Fatalf("write after the server ended its direction: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if rest, err := io.ReadAll(br); err != nil || len(rest) != 0 {
		t.Fatalf("after the server ended its direction: %q, %v", rest, err)
	}
	select {
	case b := <-received:
		if b != "after" {
			t.Fatalf("the server received %q after it ended its direction, want %q", b, "after")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not see the end of the client's direction")
	}
}

func TestTunnelManyConcurrentConnections(t *testing.T) {
	srv := listenTCP(t, echo)
	a := startAgentWith(t, TunnelStreams)
	conn := a.dial(t)
	openRPC(t, conn)
	const n = 64
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := openStream(t, conn, wire.StreamHeader{S: "tunnel", Port: srv.port()})
			br := bufio.NewReader(ctx)
			var accept map[string]any
			if err := wire.ReadHeader(br, &accept); err != nil || accept["ok"] != true {
				errs <- fmt.Errorf("connection %d: acceptance %v, %v", i, accept, err)
				return
			}
			data := make([]byte, 200000+i)
			rand.New(rand.NewSource(int64(i))).Read(data)
			go func() {
				ctx.Write(data)
				ctx.Close()
			}()
			got, err := io.ReadAll(br)
			if err != nil || !bytes.Equal(got, data) {
				errs <- fmt.Errorf("connection %d: %d of %d bytes, %v", i, len(got), len(data), err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := srv.accepted.Load(); got != n {
		t.Fatalf("the server accepted %d connections, want %d", got, n)
	}
}

func TestTunnelLargeTransferKeepsTheBytes(t *testing.T) {
	const size = 64 << 20
	var upstream [sha256.Size]byte
	got := make(chan int64, 1)
	srv := listenTCP(t, func(c net.Conn) {
		h := sha256.New()
		n, _ := io.Copy(h, c)
		copy(upstream[:], h.Sum(nil))
		got <- n
		c.Write(upstream[:])
	})
	a := startAgentWith(t, TunnelStreams)
	conn := a.dial(t)
	openRPC(t, conn)
	st, br := openTunnel(t, conn, srv.port())
	st.SetDeadline(time.Now().Add(120 * time.Second))
	sent := sha256.New()
	go func() {
		src := io.LimitReader(rand.New(rand.NewSource(7)), size)
		io.Copy(io.MultiWriter(st, sent), src)
		st.Close()
	}()
	reply, err := io.ReadAll(br)
	if err != nil {
		t.Fatal(err)
	}
	if n := <-got; n != size {
		t.Fatalf("the server received %d bytes, want %d", n, size)
	}
	if !bytes.Equal(reply, sent.Sum(nil)) {
		t.Fatalf("sha256 of the 64 MiB the server received differs from the one sent")
	}
}
