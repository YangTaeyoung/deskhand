package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTunnelToolsMapToTheTunnelMethods(t *testing.T) {
	info := map[string]any{"tunnel_id": "tn-1", "host_id": "win", "remote_port": 9229, "local_port": 5555, "local_address": "127.0.0.1:5555", "owner": "user"}
	f := startFakeAPI(t, map[string]handler{
		"tunnel.open":  func(map[string]any) (any, *rpcFail) { return info, nil },
		"tunnel.close": func(map[string]any) (any, *rpcFail) { return map[string]any{"closed": true, "tunnel_id": "tn-1"}, nil },
		"tunnel.list":  func(map[string]any) (any, *rpcFail) { return map[string]any{"tunnels": []any{info}}, nil },
	})
	cs := connect(t)
	cases := []struct {
		tool   string
		args   map[string]any
		method string
		want   map[string]any
	}{
		{"tunnel_open", map[string]any{"host": "win", "remote_port": 9229}, "tunnel.open", map[string]any{"host_id": "win", "remote_port": float64(9229)}},
		{"tunnel_open", map[string]any{"host": "win", "remote_port": 9229, "local_port": 5555}, "tunnel.open",
			map[string]any{"host_id": "win", "local_port": float64(5555), "remote_port": float64(9229)}},
		{"tunnel_close", map[string]any{"tunnel_id": "tn-1"}, "tunnel.close", map[string]any{"tunnel_id": "tn-1"}},
		{"tunnel_close", map[string]any{"local_port": 5555}, "tunnel.close", map[string]any{"local_port": float64(5555)}},
		{"tunnel_list", map[string]any{}, "tunnel.list", map[string]any{}},
		{"tunnel_list", map[string]any{"host": "win"}, "tunnel.list", map[string]any{"host_id": "win"}},
	}
	for _, c := range cases {
		res := callTool(t, cs, c.tool, c.args)
		if res.IsError {
			t.Errorf("%s: %s", c.tool, text(res))
			continue
		}
		got := f.called(c.method)
		gj, _ := json.Marshal(got[len(got)-1])
		wj, _ := json.Marshal(c.want)
		if string(gj) != string(wj) {
			t.Errorf("%s params = %s, want %s", c.tool, gj, wj)
		}
	}
	if res := callTool(t, cs, "tunnel_open", map[string]any{"host": "win", "remote_port": 9229}); !strings.Contains(text(res), "127.0.0.1:5555") {
		t.Errorf("tunnel_open summary %q does not give the local address", text(res))
	}
}

func TestTunnelToolsRefuseBadArguments(t *testing.T) {
	f := startFakeAPI(t, map[string]handler{})
	cs := connect(t)
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"tunnel_open", map[string]any{"host": "win"}},
		{"tunnel_open", map[string]any{"host": "win", "remote_port": 0}},
		{"tunnel_open", map[string]any{"host": "win", "remote_port": 70000}},
		{"tunnel_open", map[string]any{"host": "win", "remote_port": 80, "local_port": 70000}},
		{"tunnel_close", map[string]any{}},
		{"tunnel_close", map[string]any{"tunnel_id": "tn-1", "local_port": 5555}},
	} {
		if res := callTool(t, cs, c.tool, c.args); !res.IsError {
			t.Errorf("%s %v was sent", c.tool, c.args)
		}
	}
	if len(f.called("tunnel.open"))+len(f.called("tunnel.close")) != 0 {
		t.Error("a bad call reached the daemon")
	}
}

func TestTunnelToolDescriptionsSayBothEndsAreLoopback(t *testing.T) {
	cs := connect(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	desc := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		desc[tool.Name] = tool
	}
	open := desc["tunnel_open"].Description
	for _, want := range []string{"127.0.0.1", "loopback", "both ends", "tunnel_close", "not reachable from the network"} {
		if !strings.Contains(open, want) {
			t.Errorf("tunnel_open description lacks %q: %s", want, open)
		}
	}
	for _, name := range []string{"tunnel_close", "tunnel_list"} {
		if !strings.Contains(desc[name].Description, "tunnel_id") {
			t.Errorf("%s description does not name tunnel_id: %s", name, desc[name].Description)
		}
	}
}
