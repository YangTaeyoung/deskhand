package quicserve

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// psEchoScript listens on a loopback port chosen by Windows, prints the
// port and echoes every connection's bytes until the peer ends its side.
const psEchoScript = `$l=[Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,0);$l.Start();[Console]::Out.WriteLine($l.LocalEndpoint.Port);[Console]::Out.Flush();` +
	`while($true){$c=$l.AcceptTcpClient();$s=$c.GetStream();$b=New-Object byte[] 65536;` +
	`while(($n=$s.Read($b,0,$b.Length)) -gt 0){$s.Write($b,0,$n);$s.Flush()};$c.Client.Shutdown([Net.Sockets.SocketShutdown]::Send);$c.Close()}`

// startPowerShellEcho starts a window-less PowerShell echo server on a
// loopback port and returns the port; the server ends with the test.
func startPowerShellEcho(t *testing.T) int {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", psEchoScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		cmd.Wait()
	})
	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(out).ReadString('\n')
		line <- strings.TrimSpace(s)
	}()
	select {
	case s := <-line:
		port, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("the PowerShell echo server printed %q", s)
		}
		return port
	case <-time.After(60 * time.Second):
		t.Fatal("the PowerShell echo server did not report its port")
	}
	return 0
}

func TestTunnelToAPowerShellEchoServerOnTheRealPC(t *testing.T) {
	if os.Getenv("DESKHAND_WINTEST") != "1" {
		t.Skip("runs only through scripts/wintest.sh")
	}
	port := startPowerShellEcho(t)
	a := startAgentWith(t, TunnelStreams)
	conn := a.dial(t)
	_, r, w := openRPC(t, conn)
	c := &rpcCaller{t: t, r: r, w: w}

	st, br := openTunnel(t, conn, port)
	st.SetDeadline(time.Now().Add(120 * time.Second))
	for _, msg := range []string{"hello from the Mac side", string(bytes.Repeat([]byte("0123456789"), 10000))} {
		if _, err := st.Write([]byte(msg)); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(msg))
		if _, err := io.ReadFull(br, got); err != nil || string(got) != msg {
			t.Fatalf("echo of %d bytes: %v", len(msg), err)
		}
	}
	data := make([]byte, 8<<20)
	rand.New(rand.NewSource(11)).Read(data)
	sum := sha256.Sum256(data)
	go func() {
		st.Write(data)
		st.Close()
	}()
	echoed := sha256.New()
	if n, err := io.Copy(echoed, br); err != nil || n != int64(len(data)) {
		t.Fatalf("echoed %d of %d bytes: %v", n, len(data), err)
	}
	if !bytes.Equal(echoed.Sum(nil), sum[:]) {
		t.Fatal("the bytes echoed by the PowerShell server differ")
	}

	res := c.call("debug.ports", map[string]any{"count": 3})
	ports, _ := res["ports"].([]any)
	if res["ok"] != true || len(ports) != 3 {
		t.Fatalf("debug.ports %v", res)
	}
}
