package cli

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a bytes buffer that a test reads while the command writes.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func tunnelInfo(id string, local, remote int, owner string) map[string]any {
	return map[string]any{"tunnel_id": id, "host_id": "windows-dev", "remote_port": remote, "local_port": local, "local_address": fmt.Sprintf("127.0.0.1:%d", local),
		"owner": owner, "created_at": "2026-10-21T01:02:03Z", "connections": 1, "total_connections": 4, "failed_connections": 0,
		"bytes_to_remote": 2048, "bytes_from_remote": 1048576}
}

func TestTunnelRunsUntilInterruptedAndThenClosesTheTunnel(t *testing.T) {
	info := tunnelInfo("tn-1", 5555, 9229, "user")
	f := startFake(t, map[string]handler{
		"tunnel.open":  ok(info),
		"tunnel.list":  ok(map[string]any{"tunnels": []any{info}}),
		"tunnel.close": ok(map[string]any{"closed": true, "tunnel_id": "tn-1"}),
	})
	var out, errb syncBuffer
	app := New("test")
	app.Stdin, app.Stdout, app.Stderr = strings.NewReader(""), &out, &errb
	app.PollInterval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- app.Run(ctx, []string{"tunnel", "windows-dev", "9229", "--local", "5555"}) }()
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(out.String(), "127.0.0.1:5555"); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no address printed: stdout %q stderr %q", out.String(), errb.String())
		}
	}
	select {
	case code := <-done:
		t.Fatalf("the command returned %d before the interrupt: %s", code, errb.String())
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the command did not return after the interrupt")
	}
	if calls := f.called("tunnel.open"); len(calls) != 1 || calls[0]["host_id"] != "windows-dev" || calls[0]["remote_port"] != float64(9229) || calls[0]["local_port"] != float64(5555) {
		t.Fatalf("tunnel.open calls %v", calls)
	}
	if calls := f.called("tunnel.close"); len(calls) != 1 || calls[0]["tunnel_id"] != "tn-1" {
		t.Fatalf("tunnel.close calls %v", calls)
	}
}

func TestTunnelEndsWhenTheDaemonDropsTheTunnel(t *testing.T) {
	info := tunnelInfo("tn-2", 5556, 80, "user")
	startFake(t, map[string]handler{
		"tunnel.open": ok(info),
		"tunnel.list": ok(map[string]any{"tunnels": []any{}}),
	})
	r := runCLI(t, false, "", "tunnel", "windows-dev", "80")
	if r.code != exitFailure || !strings.Contains(r.err, "터널") {
		t.Fatalf("exit %d, stderr %q", r.code, r.err)
	}
}

func TestTunnelRefusesWrongArguments(t *testing.T) {
	startFake(t, map[string]handler{})
	for _, args := range [][]string{{"tunnel"}, {"tunnel", "win"}, {"tunnel", "win", "abc"}, {"tunnel", "win", "0"}, {"tunnel", "win", "70000"},
		{"tunnel", "win", "80", "--local", "70000"}, {"tunnel", "win", "80", "extra"}, {"tunnel", "close"}, {"tunnel", "close", "a", "b"}} {
		if r := runCLI(t, false, "", args...); r.code != exitUsage {
			t.Errorf("%v: exit %d, stderr %q", args, r.code, r.err)
		}
	}
}

func TestTunnelListPrintsATable(t *testing.T) {
	f := startFake(t, map[string]handler{"tunnel.list": ok(map[string]any{"tunnels": []any{
		tunnelInfo("tn-1", 5555, 9229, "user"), tunnelInfo("tn-2", 5556, 9222, "debug:d-7")}})})
	r := runCLI(t, false, "", "tunnel", "list", "windows-dev")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	for _, want := range []string{"tn-1", "127.0.0.1:5555", "9229", "debug:d-7", "2.0 KiB", "1.0 MiB"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, r.stdout)
		}
	}
	if calls := f.called("tunnel.list"); len(calls) != 1 || calls[0]["host_id"] != "windows-dev" {
		t.Fatalf("calls %v", calls)
	}
	r = runCLI(t, false, "", "--json", "tunnel", "list")
	if r.code != 0 || !strings.Contains(r.stdout, `"tunnels"`) {
		t.Fatalf("json: exit %d %q", r.code, r.stdout)
	}
	if calls := f.called("tunnel.list"); calls[1]["host_id"] != nil {
		t.Fatalf("a list without a host sent %v", calls[1])
	}
}

func TestTunnelCloseTakesALocalPortOrATunnelID(t *testing.T) {
	f := startFake(t, map[string]handler{"tunnel.close": ok(map[string]any{"closed": true, "tunnel_id": "tn-1", "local_port": 5555, "remote_port": 9229, "host_id": "windows-dev"})})
	if r := runCLI(t, false, "", "tunnel", "close", "5555"); r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	if r := runCLI(t, false, "", "tunnel", "close", "tn-1"); r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.err)
	}
	calls := f.called("tunnel.close")
	if len(calls) != 2 || calls[0]["local_port"] != float64(5555) || calls[0]["tunnel_id"] != nil || calls[1]["tunnel_id"] != "tn-1" || calls[1]["local_port"] != nil {
		t.Fatalf("calls %v", calls)
	}
}
