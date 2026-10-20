package server

import (
	"net"
	"strconv"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestDebugPortsAreFreeDistinctLoopbackPorts(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	res := c.call("debug.ports", proto.Message{"count": 8})
	ports, _ := res["ports"].([]any)
	if res["ok"] != true || len(ports) != 8 {
		t.Fatalf("debug.ports %v", res)
	}
	seen := map[float64]bool{}
	for _, p := range ports {
		n, _ := p.(float64)
		if n < 1 || n > 65535 || seen[n] {
			t.Fatalf("ports %v", ports)
		}
		seen[n] = true
		l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(n))))
		if err != nil {
			t.Fatalf("port %v is not free: %v", n, err)
		}
		l.Close()
	}
}

func TestDebugPortsRefusesACountOutsideOneToEight(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	for _, count := range []any{0, 9, -1, 1.5, "2", nil} {
		req := proto.Message{}
		if count != nil {
			req["count"] = count
		}
		if res := c.call("debug.ports", req); res["ok"] != false || res["code"] != wire.CodeInvalidArgument {
			t.Fatalf("count %v answered %v", count, res)
		}
	}
}

func TestDebugPortsNeedsExec(t *testing.T) {
	_, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	expect(t, r, "hello")
	send(t, w, proto.Message{"t": "welcome", "v": 1, "controller": "t"})
	send(t, w, proto.Message{"t": "req", "id": 1, "op": "debug.ports", "count": 1})
	if res := expect(t, r, "res"); res["ok"] != false {
		t.Fatalf("debug.ports without exec answered %v", res)
	}
}
