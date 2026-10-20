package daemon_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"math/rand"
	"net"
	"strconv"
	"testing"
	"time"
)

// macEcho is a TCP echo server on a loopback port; the agent of the tests
// runs on this machine, so the port is also a port of the PC.
func macEcho(t *testing.T) int {
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

func tunnelAddr(res map[string]any) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(int(res["local_port"].(float64))))
}

func echoOnce(t *testing.T, addr, msg string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
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

func TestTunnelMethodsOverTheLocalAPI(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	remote := macEcho(t)
	host := e.call(t, "host.list", nil)["hosts"].([]any)[0].(map[string]any)
	if c := host["caps"].(map[string]any)["tunnel"].(map[string]any); c["state"] != "ready" {
		t.Fatalf("caps.tunnel %v", c)
	}

	res := e.call(t, "tunnel.open", map[string]any{"host_id": "pc", "remote_port": remote})
	id, _ := res["tunnel_id"].(string)
	if id == "" || res["remote_port"] != float64(remote) || res["local_port"].(float64) < 1 || res["owner"] != "user" || res["host_id"] != "pc" ||
		res["local_address"] != tunnelAddr(res) {
		t.Fatalf("tunnel.open %v", res)
	}
	echoOnce(t, tunnelAddr(res), "through the tunnel")

	var entry map[string]any
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		list := e.call(t, "tunnel.list", map[string]any{"host_id": "pc"})["tunnels"].([]any)
		if len(list) == 1 {
			entry = list[0].(map[string]any)
			if entry["bytes_to_remote"] == float64(18) && entry["bytes_from_remote"] == float64(18) && entry["connections"] == float64(0) {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("tunnel.list %v", list)
		}
	}
	if entry["tunnel_id"] != id || entry["total_connections"] != float64(1) || entry["owner"] != "user" {
		t.Fatalf("list entry %v", entry)
	}
	ctxList, cancelList := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelList()
	if err := e.c.Call(ctxList, "tunnel.list", map[string]any{"host_id": "no-such-pc"}, nil); apiCode(err) != "NOT_FOUND" {
		t.Fatalf("list of an unknown host: %v", err)
	}

	closed := e.call(t, "tunnel.close", map[string]any{"local_port": res["local_port"]})
	if closed["closed"] != true || closed["tunnel_id"] != id {
		t.Fatalf("tunnel.close %v", closed)
	}
	if c, err := net.DialTimeout("tcp", tunnelAddr(res), time.Second); err == nil {
		c.Close()
		t.Fatal("the closed tunnel still accepts")
	}
	if list := e.call(t, "tunnel.list", nil)["tunnels"].([]any); len(list) != 0 {
		t.Fatalf("tunnels after the close %v", list)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.c.Call(ctx, "tunnel.close", map[string]any{"tunnel_id": id}, nil); apiCode(err) != "NOT_FOUND" {
		t.Fatalf("closing a closed tunnel: %v", err)
	}
	if err := e.c.Call(ctx, "tunnel.open", map[string]any{"host_id": "pc", "remote_port": 0}, nil); apiCode(err) != "INVALID_ARGUMENT" {
		t.Fatalf("open of port 0: %v", err)
	}
	if err := e.c.Call(ctx, "tunnel.open", map[string]any{"remote_port": 80}, nil); apiCode(err) != "INVALID_ARGUMENT" {
		t.Fatalf("open without a host: %v", err)
	}
	if err := e.c.Call(ctx, "tunnel.close", map[string]any{}, nil); apiCode(err) != "INVALID_ARGUMENT" {
		t.Fatalf("close without a tunnel: %v", err)
	}
}

func TestTunnelListenerSurvivesHostReconnect(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	res := e.call(t, "tunnel.open", map[string]any{"host_id": "pc", "remote_port": macEcho(t)})
	addr := tunnelAddr(res)
	echoOnce(t, addr, "before")

	e.call(t, "host.disconnect", map[string]any{"host_id": "pc"})
	e.waitState(t, "offline")
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("the listener closed with the host connection: %v", err)
	}
	start := time.Now()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("a connection while the host is offline: read %v after %s", err, time.Since(start))
	}
	c.Close()
	list := e.call(t, "tunnel.list", nil)["tunnels"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["failed_connections"] != float64(1) || list[0].(map[string]any)["last_error"] == "" {
		t.Fatalf("tunnel.list while offline %v", list)
	}

	e.call(t, "host.connect", map[string]any{"host_id": "pc"})
	e.waitState(t, "ready")
	echoOnce(t, addr, "after")
}

func TestTunnelCarriesLargeDataIntact(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	res := e.call(t, "tunnel.open", map[string]any{"host_id": "pc", "remote_port": macEcho(t)})
	c, err := net.Dial("tcp", tunnelAddr(res))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(60 * time.Second))
	const size = 16 << 20
	sent := sha256.New()
	go func() {
		io.Copy(io.MultiWriter(c, sent), io.LimitReader(rand.New(rand.NewSource(3)), size))
		c.(*net.TCPConn).CloseWrite()
	}()
	got := sha256.New()
	n, err := io.Copy(got, c)
	if err != nil || n != size {
		t.Fatalf("read %d of %d bytes: %v", n, size, err)
	}
	if !bytes.Equal(got.Sum(nil), sent.Sum(nil)) {
		t.Fatal("the echoed bytes differ from the sent ones")
	}
}
