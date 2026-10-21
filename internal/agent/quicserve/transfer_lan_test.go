package quicserve

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"hash"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/server"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/transport"
	"github.com/YangTaeyoung/deskhand/internal/wire"
	"github.com/quic-go/quic-go"
)

var (
	lanPeer     = flag.String("xfer.peer", "", "UDP address of the daemon of TestTransferThroughputOverLAN, which sets it")
	lanDaemonFP = flag.String("xfer.daemon-fp", "", "certificate fingerprint of that daemon")
	lanSize     = flag.Int64("xfer.size", 1<<30, "bytes of each file TestTransferThroughputOverLAN puts and gets")
)

// Seeds of the synthetic files: the one the daemon puts and the one the
// agent makes for the daemon to get.
var (
	putSeed = [32]byte{'p', 'u', 't'}
	getSeed = [32]byte{'g', 'e', 't'}
)

// synthetic returns size bytes of the ChaCha8 stream of seed.
func synthetic(seed [32]byte, size int64) io.Reader {
	return io.LimitReader(rand.NewChaCha8(seed), size)
}

// connectedConn is a UDP socket connected to peer as a net.PacketConn whose
// packets come from and go to peer.
type connectedConn struct {
	c    *net.UDPConn
	peer *net.UDPAddr
}

func (c connectedConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, err := c.c.Read(p)
	return n, c.peer, err
}
func (c connectedConn) WriteTo(p []byte, _ net.Addr) (int, error) { return c.c.Write(p) }
func (c connectedConn) Close() error                              { return c.c.Close() }
func (c connectedConn) LocalAddr() net.Addr                       { return c.c.LocalAddr() }
func (c connectedConn) SetDeadline(t time.Time) error             { return c.c.SetDeadline(t) }
func (c connectedConn) SetReadDeadline(t time.Time) error         { return c.c.SetReadDeadline(t) }
func (c connectedConn) SetWriteDeadline(t time.Time) error        { return c.c.SetWriteDeadline(t) }
func (c connectedConn) SetReadBuffer(n int) error                 { return c.c.SetReadBuffer(n) }
func (c connectedConn) SetWriteBuffer(n int) error                { return c.c.SetWriteBuffer(n) }
func (c connectedConn) SyscallConn() (syscall.RawConn, error)     { return c.c.SyscallConn() }

// TestTransferAgentOverLAN is the agent of TestTransferThroughputOverLAN,
// which runs it on the PC with scripts/wintest.sh --ssh. In the lan folder
// of its working folder it makes the file the daemon gets and writes
// lan.txt with the local port of a UDP socket connected to the daemon, its
// certificate fingerprint and that folder. Once go.txt appears it sends
// packets to the daemon until the daemon connects, serves the connection
// with the put and get streams until its rpc stream ends, and removes the
// lan folder.
func TestTransferAgentOverLAN(t *testing.T) {
	if *lanPeer == "" {
		t.Skip("runs from TestTransferThroughputOverLAN")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(wd, "lan")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(work)
	f, err := os.Create(filepath.Join(work, "get.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyBuffer(f, synthetic(getSeed, *lanSize), make([]byte, 4<<20)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	agentID, err := transport.NewIdentity("lan-agent")
	if err != nil {
		t.Fatal(err)
	}
	peer, err := net.ResolveUDPAddr("udp", *lanPeer)
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.DialUDP("udp", nil, peer)
	if err != nil {
		t.Fatal(err)
	}
	pc := connectedConn{udp, peer}
	defer pc.Close()
	ln, err := transport.ListenPacket(pc, agentID, func(fp string) bool { return fp == *lanDaemonFP })
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	line := fmt.Sprintf("%d %s %s\n", udp.LocalAddr().(*net.UDPAddr).Port, agentID.Fingerprint, work)
	if err := os.WriteFile(filepath.Join(wd, "lan.tmp"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(wd, "lan.tmp"), filepath.Join(wd, "lan.txt")); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(wd, "go.txt")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("go.txt did not appear within a minute")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	accepted := make(chan struct{})
	go func() {
		for {
			_, _ = udp.Write([]byte{0})
			select {
			case <-accepted:
				return
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}()
	conn, err := ln.Accept(ctx)
	close(accepted)
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(server.Options{Clipboard: fakeClipboard{}, HomeDir: work, AllowExec: true, Version: "lan", Logger: quiet})
	defer srv.Close()
	serveConn(ctx, conn, srv, TransferStreams(srv), &controllerConns{conns: map[string]*quic.Conn{}}, quiet)
}

// lanSSH runs command on the SSH host host and returns its output.
func lanSSH(host, command string) (string, error) {
	out, err := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes", "-o", "ConnectTimeout=10", host, command).Output()
	return string(out), err
}

// lanAddresses returns the address of the SSH host host and the local
// address that reaches it.
func lanAddresses(t *testing.T, host string) (remote, local net.IP) {
	t.Helper()
	out, err := exec.Command("ssh", "-G", host).Output()
	if err != nil {
		t.Fatalf("ssh -G %s: %v", host, err)
	}
	var name string
	for _, l := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(l, "hostname "); ok {
			name = strings.TrimSpace(v)
		}
	}
	addrs, err := net.LookupIP(name)
	if err != nil || len(addrs) == 0 {
		t.Fatalf("the address of %s (%s): %v", host, name, err)
	}
	probe, err := net.Dial("udp", net.JoinHostPort(addrs[0].String(), "9"))
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	return addrs[0], probe.LocalAddr().(*net.UDPAddr).IP
}

// openLANStream opens a stream with header h on conn.
func openLANStream(t *testing.T, ctx context.Context, conn *quic.Conn, h wire.StreamHeader) (*quic.Stream, *bufio.Reader) {
	t.Helper()
	st, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st.SetDeadline(time.Now().Add(5 * time.Minute))
	if err := wire.WriteHeader(st, h); err != nil {
		t.Fatal(err)
	}
	return st, bufio.NewReader(st)
}

// mbps returns n bytes over d in megabytes (10^6 bytes) per second.
func mbps(n int64, d time.Duration) float64 { return float64(n) / d.Seconds() / 1e6 }

// TestTransferThroughputOverLAN measures put and get of DESKHAND_XFER_LAN_SIZE
// bytes (default -xfer.size) between this computer, as the daemon, and the
// PC of the SSH host DESKHAND_XFER_LAN, which runs TestTransferAgentOverLAN
// through scripts/wintest.sh --ssh in its run folder. The daemon's socket
// sends a packet to the agent's before it creates go.txt, and it connects
// once a packet of the agent's socket arrives.
func TestTransferThroughputOverLAN(t *testing.T) {
	host := os.Getenv("DESKHAND_XFER_LAN")
	if host == "" {
		t.Skip("set DESKHAND_XFER_LAN to the SSH host of a Windows PC to measure put and get over the LAN")
	}
	size := *lanSize
	if v := os.Getenv("DESKHAND_XFER_LAN_SIZE"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			t.Fatalf("DESKHAND_XFER_LAN_SIZE %q is not a number of bytes", v)
		}
		size = n
	}
	pcIP, localIP := lanAddresses(t, host)
	udp, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	daemonID, err := transport.NewIdentity("lan-daemon")
	if err != nil {
		t.Fatal(err)
	}
	peer := net.JoinHostPort(localIP.String(), strconv.Itoa(udp.LocalAddr().(*net.UDPAddr).Port))

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(filepath.Join("..", "..", "..", "scripts", "wintest.sh"), "--ssh", "./internal/agent/quicserve",
		"-run=^TestTransferAgentOverLAN$", "-xfer.peer="+peer, "-xfer.daemon-fp="+daemonID.Fingerprint, fmt.Sprintf("-xfer.size=%d", size))
	cmd.Env = append(os.Environ(), "DESKHAND_WINTEST_WAIT=480", "DESKHAND_WINTEST_HOST="+host)
	cmd.Stdout = &stdout
	pr, pw := io.Pipe()
	cmd.Stderr = io.MultiWriter(&stderr, pw)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		pw.Close()
		waited <- err
	}()
	defer func() {
		select {
		case err := <-waited:
			t.Logf("PC side (%v):\n%s%s", err, stderr.String(), stdout.String())
		case <-time.After(9 * time.Minute):
			cmd.Process.Kill()
			t.Errorf("the PC side did not end:\n%s%s", stderr.String(), stdout.String())
		}
	}()

	remoteDir := make(chan string, 1)
	go func() {
		re := regexp.MustCompile(`→ \S+ (\S+) \(SSH`)
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			if m := re.FindStringSubmatch(sc.Text()); m != nil {
				remoteDir <- m[1]
			}
		}
		io.Copy(io.Discard, pr)
	}()
	var dir string
	select {
	case dir = <-remoteDir:
	case err := <-waited:
		t.Fatalf("wintest ended before it ran (%v):\n%s%s", err, stderr.String(), stdout.String())
	case <-time.After(3 * time.Minute):
		t.Fatalf("wintest named no run folder:\n%s", stderr.String())
	}

	var agentPort int
	var agentFP, work string
	for deadline := time.Now().Add(4 * time.Minute); ; time.Sleep(500 * time.Millisecond) {
		out, err := lanSSH(host, fmt.Sprintf(`if exist "%s\lan.txt" type "%s\lan.txt"`, dir, dir))
		if f := strings.Fields(out); err == nil && len(f) >= 3 {
			agentPort, _ = strconv.Atoi(f[0])
			agentFP, work = f[1], strings.Join(f[2:], " ")
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lan.txt did not appear in %s: %v %q", dir, err, out)
		}
	}
	agentAddr := &net.UDPAddr{IP: pcIP, Port: agentPort}
	if _, err := udp.WriteToUDP([]byte{0}, agentAddr); err != nil {
		t.Fatal(err)
	}
	if _, err := lanSSH(host, fmt.Sprintf(`type nul > "%s\go.txt"`, dir)); err != nil {
		t.Fatal(err)
	}
	udp.SetReadDeadline(time.Now().Add(30 * time.Second))
	buf := make([]byte, 2048)
	for {
		_, from, err := udp.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("no packet from the agent at %s: %v", agentAddr, err)
		}
		if from.IP.Equal(pcIP) && from.Port == agentPort {
			break
		}
	}
	udp.SetReadDeadline(time.Time{})

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	conn, err := transport.DialPacket(ctx, udp, agentAddr.String(), daemonID, agentFP)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseWithError(0, "")
	rpc, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.WriteHeader(rpc, wire.StreamHeader{S: "rpc"}); err != nil {
		t.Fatal(err)
	}
	r, w := proto.NewReader(rpc), proto.NewWriter(rpc)
	next(t, r, "hello")
	if err := w.Send(proto.Message{"t": "welcome", "v": 1, "controller": "lan"}); err != nil {
		t.Fatal(err)
	}
	c := &rpcCaller{t: t, r: r, w: w}

	dst := work + `\put.bin`
	if res := c.call("xfer.prepare", proto.Message{"transfer_id": "lan", "path": dst, "size": size}); res["ok"] != true {
		t.Fatalf("prepare %v", res)
	}
	st, br := openLANStream(t, ctx, conn, wire.StreamHeader{S: "put", TransferID: "lan", Path: dst, Size: size})
	if accept := readJSONLine(t, br); accept["ok"] != true {
		t.Fatalf("put answered %v", accept)
	}
	h := sha256.New()
	start := time.Now()
	if _, err := io.CopyBuffer(io.MultiWriter(st, h), synthetic(putSeed, size), make([]byte, 4<<20)); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	result := readJSONLine(t, br)
	putTime := time.Since(start)
	if result["ok"] != true || int64(result.Int("part_size")) != size {
		t.Fatalf("put %v", result)
	}
	start = time.Now()
	if res := c.call("xfer.commit", proto.Message{"transfer_id": "lan", "path": dst, "sha256": hex.EncodeToString(h.Sum(nil))}); res["ok"] != true {
		t.Fatalf("commit %v", res)
	}
	commitTime := time.Since(start)

	want := sha256.New()
	io.CopyBuffer(want, synthetic(getSeed, size), make([]byte, 4<<20))
	st, br = openLANStream(t, ctx, conn, wire.StreamHeader{S: "get", Path: work + `\get.bin`})
	start = time.Now()
	if accept := readJSONLine(t, br); accept["ok"] != true || int64(accept.Int("length")) != size {
		t.Fatalf("get answered %v", accept)
	}
	got := sha256.New()
	n, err := io.CopyBuffer(got, br, make([]byte, 4<<20))
	getTime := time.Since(start)
	if err != nil || n != size || !sameSum(got, want) {
		t.Fatalf("get carried %d bytes (%v), sha256 equal %v", n, err, sameSum(got, want))
	}
	rpc.CancelRead(0)
	rpc.Close()

	t.Logf("put %d bytes: %s, %.1f MB/s; commit (sha256 check and rename on the PC): %s", size, putTime.Round(time.Millisecond), mbps(size, putTime), commitTime.Round(time.Millisecond))
	t.Logf("get %d bytes: %s, %.1f MB/s", size, getTime.Round(time.Millisecond), mbps(size, getTime))
}

// sameSum reports whether a and b hold the same sum.
func sameSum(a, b hash.Hash) bool { return bytes.Equal(a.Sum(nil), b.Sum(nil)) }
