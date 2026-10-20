package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDiagToolsMapToTheDiagMethods(t *testing.T) {
	ok := func(map[string]any) (any, *rpcFail) { return envelope(nil), nil }
	f := startFakeAPI(t, map[string]handler{"diag.system": ok, "diag.services": ok, "diag.events": ok, "diag.crashes": ok, "proc.kill": ok})
	cs := connect(t)
	cases := []struct {
		tool   string
		args   map[string]any
		method string
		want   map[string]any
	}{
		{"diag_system", map[string]any{"host": "win", "save": true}, "diag.system", map[string]any{"host_id": "win", "save": true}},
		{"diag_services", map[string]any{"host": "win", "name": "update"}, "diag.services", map[string]any{"host_id": "win", "name": "update"}},
		{"diag_events", map[string]any{"host": "win", "log": "Application", "since": "2026-10-20T00:00:00Z", "until": "2026-10-21T00:00:00Z",
			"levels": []any{"error", "critical"}, "provider": "Application Error", "max": 50},
			"diag.events", map[string]any{"host_id": "win", "levels": []any{"error", "critical"}, "log": "Application", "max": float64(50),
				"provider": "Application Error", "since": "2026-10-20T00:00:00Z", "until": "2026-10-21T00:00:00Z"}},
		{"diag_events", map[string]any{"host": "win"}, "diag.events", map[string]any{"host_id": "win"}},
		{"diag_crashes", map[string]any{"host": "win", "since": "2026-10-01T00:00:00Z"}, "diag.crashes", map[string]any{"host_id": "win", "since": "2026-10-01T00:00:00Z"}},
		{"proc_kill", map[string]any{"host": "win", "pid": 12, "started": "2026-10-20T01:00:00.000Z"}, "proc.kill",
			map[string]any{"host_id": "win", "pid": float64(12), "started": "2026-10-20T01:00:00.000Z"}},
	}
	for _, c := range cases {
		res := callTool(t, cs, c.tool, c.args)
		if res.IsError {
			t.Errorf("%s: %s", c.tool, text(res))
			continue
		}
		got := f.called(c.method)
		if len(got) == 0 {
			t.Errorf("%s: %s not called", c.tool, c.method)
			continue
		}
		gj, _ := json.Marshal(got[len(got)-1])
		wj, _ := json.Marshal(c.want)
		if string(gj) != string(wj) {
			t.Errorf("%s params = %s, want %s", c.tool, gj, wj)
		}
	}
	for _, bad := range []map[string]any{{"host": "win", "levels": []any{"fatal"}}, {"host": "win", "log": "Security"}, {"host": "win", "max": 0}, {"host": "win", "max": 1001}} {
		if res := callTool(t, cs, "diag_events", bad); !res.IsError {
			t.Errorf("diag_events %v was sent", bad)
		}
	}
}

func TestAFailedDiagEnvelopeIsAToolError(t *testing.T) {
	startFakeAPI(t, map[string]handler{"diag.system": func(map[string]any) (any, *rpcFail) {
		return envelope(map[string]any{"status": "failed", "action": map[string]any{"executed": false, "outcome": "not_started"},
			"error": map[string]any{"code": "CAPABILITY_UNSUPPORTED", "message": "diagnostics are read from Windows only", "retryable": false}}), nil
	}})
	res := callTool(t, connect(t), "diag_system", map[string]any{"host": "win"})
	if !res.IsError {
		t.Fatal("IsError = false")
	}
	if m := structured(t, res); m["code"] != "CAPABILITY_UNSUPPORTED" || m["retryable"] != false {
		t.Fatalf("structured = %v", m)
	}
}

func TestDiagToolSummariesNameTheMainFacts(t *testing.T) {
	startFakeAPI(t, map[string]handler{
		"diag.system": func(map[string]any) (any, *rpcFail) {
			return envelope(map[string]any{"computer_name": "DEV-PC", "os": map[string]any{"name": "Windows 11 Pro", "version": "25H2", "build": "26200.9278"},
				"cpu": map[string]any{"usage_pct": 13.3}, "memory": map[string]any{"total_bytes": 34257387520, "used_bytes": 16575488000}, "uptime_s": 53703}), nil
		},
		"diag.events": func(map[string]any) (any, *rpcFail) {
			return envelope(map[string]any{"events": []any{map[string]any{"record_id": 1}}, "truncated": true}), nil
		},
		"diag.crashes": func(map[string]any) (any, *rpcFail) {
			return envelope(map[string]any{"dumps": []any{map[string]any{"app": "Shop.exe", "kind": "minidump"}}}), nil
		},
	})
	cs := connect(t)
	for tool, wants := range map[string][]string{
		"diag_system":  {"DEV-PC", "Windows 11 Pro", "26200.9278", "13.3"},
		"diag_events":  {"1 events", "truncated"},
		"diag_crashes": {"1 crash", "files_pull"},
	} {
		got := text(callTool(t, cs, tool, map[string]any{"host": "win"}))
		for _, w := range wants {
			if !strings.Contains(got, w) {
				t.Errorf("%s text lacks %q: %s", tool, w, got)
			}
		}
	}
}
