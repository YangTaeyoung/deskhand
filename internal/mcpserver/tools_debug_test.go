package mcpserver

import (
	"encoding/json"
	"testing"
)

func TestDebugToolsMapToTheDebugMethods(t *testing.T) {
	sess := map[string]any{"debug_id": "d-1", "host_id": "win", "name": "App", "state": "running", "targets": []any{}}
	env := func(extra map[string]any) map[string]any {
		m := map[string]any{"schema_version": 1, "status": "succeeded", "action": map[string]any{"executed": true, "outcome": "done"},
			"error": nil, "host_id": "win"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	f := startFakeAPI(t, map[string]handler{
		"debug.electron.launch": func(map[string]any) (any, *rpcFail) { return env(map[string]any{"session": sess}), nil },
		"debug.attach":          func(map[string]any) (any, *rpcFail) { return env(map[string]any{"session": sess}), nil },
		"debug.list":            func(map[string]any) (any, *rpcFail) { return map[string]any{"sessions": []any{sess}}, nil },
		"debug.get":             func(map[string]any) (any, *rpcFail) { return sess, nil },
		"debug.breakpoint.set": func(map[string]any) (any, *rpcFail) {
			return env(map[string]any{"breakpoint": map[string]any{"breakpoint_id": "bp-1"}}), nil
		},
		"debug.breakpoint.remove": func(map[string]any) (any, *rpcFail) {
			return env(map[string]any{"breakpoint": map[string]any{"breakpoint_id": "bp-1"}}), nil
		},
		"debug.exceptions": func(map[string]any) (any, *rpcFail) { return env(map[string]any{"session": sess}), nil },
		"debug.pause": func(map[string]any) (any, *rpcFail) {
			return env(map[string]any{"target": map[string]any{"id": "t-1"}}), nil
		},
		"debug.resume": func(map[string]any) (any, *rpcFail) {
			return env(map[string]any{"target": map[string]any{"id": "t-1"}}), nil
		},
		"debug.step": func(map[string]any) (any, *rpcFail) {
			return env(map[string]any{"target": map[string]any{"id": "t-1"}}), nil
		},
		"debug.state":     func(map[string]any) (any, *rpcFail) { return map[string]any{"target_id": "t-1", "paused": true}, nil },
		"debug.variables": func(map[string]any) (any, *rpcFail) { return map[string]any{"properties": []any{}, "total": 0}, nil },
		"debug.evaluate": func(map[string]any) (any, *rpcFail) {
			return env(map[string]any{"value_preview": "52000", "type": "number"}), nil
		},
		"debug.scripts": func(map[string]any) (any, *rpcFail) { return map[string]any{"scripts": []any{}}, nil },
		"debug.source": func(map[string]any) (any, *rpcFail) {
			return map[string]any{"script_id": "s1", "text": "x", "truncated": false}, nil
		},
		"debug.console": func(map[string]any) (any, *rpcFail) { return map[string]any{"entries": []any{}, "next_cursor": 0}, nil },
		"debug.close":   func(map[string]any) (any, *rpcFail) { return env(map[string]any{"session": sess}), nil },
	})
	cs := connect(t)
	cases := []struct {
		tool   string
		args   map[string]any
		method string
		want   map[string]any
	}{
		{"debug_launch", map[string]any{"host": "win", "path": `C:\app\App.exe`, "pause_on_start": true}, "debug.electron.launch",
			map[string]any{"host_id": "win", "path": `C:\app\App.exe`, "pause_on_start": true}},
		{"debug_attach", map[string]any{"host": "win", "port": 9229}, "debug.attach", map[string]any{"host_id": "win", "port": float64(9229)}},
		{"debug_list", map[string]any{"host": "win"}, "debug.list", map[string]any{"host_id": "win"}},
		{"debug_get", map[string]any{"debug_id": "d-1"}, "debug.get", map[string]any{"debug_id": "d-1"}},
		{"debug_breakpoint_set", map[string]any{"debug_id": "d-1", "match": "file_name", "pattern": "main.js", "line": 10}, "debug.breakpoint.set",
			map[string]any{"debug_id": "d-1", "match": "file_name", "pattern": "main.js", "line": float64(10)}},
		{"debug_breakpoint_remove", map[string]any{"debug_id": "d-1", "breakpoint_id": "bp-1"}, "debug.breakpoint.remove",
			map[string]any{"debug_id": "d-1", "breakpoint_id": "bp-1"}},
		{"debug_exceptions", map[string]any{"debug_id": "d-1", "mode": "all"}, "debug.exceptions", map[string]any{"debug_id": "d-1", "mode": "all"}},
		{"debug_pause", map[string]any{"debug_id": "d-1", "target_id": "t-1"}, "debug.pause", map[string]any{"debug_id": "d-1", "target_id": "t-1"}},
		{"debug_resume", map[string]any{"debug_id": "d-1", "target_id": "t-1"}, "debug.resume", map[string]any{"debug_id": "d-1", "target_id": "t-1"}},
		{"debug_step", map[string]any{"debug_id": "d-1", "target_id": "t-1", "kind": "over"}, "debug.step",
			map[string]any{"debug_id": "d-1", "target_id": "t-1", "kind": "over"}},
		{"debug_state", map[string]any{"debug_id": "d-1", "target_id": "t-1"}, "debug.state", map[string]any{"debug_id": "d-1", "target_id": "t-1"}},
		{"debug_variables", map[string]any{"debug_id": "d-1", "target_id": "t-1", "object_id": "o1"}, "debug.variables",
			map[string]any{"debug_id": "d-1", "target_id": "t-1", "object_id": "o1"}},
		{"debug_evaluate", map[string]any{"debug_id": "d-1", "target_id": "t-1", "expression": "1+1"}, "debug.evaluate",
			map[string]any{"debug_id": "d-1", "target_id": "t-1", "expression": "1+1"}},
		{"debug_scripts", map[string]any{"debug_id": "d-1"}, "debug.scripts", map[string]any{"debug_id": "d-1"}},
		{"debug_source", map[string]any{"debug_id": "d-1", "target_id": "t-1", "script_id": "s1"}, "debug.source",
			map[string]any{"debug_id": "d-1", "target_id": "t-1", "script_id": "s1"}},
		{"debug_console", map[string]any{"debug_id": "d-1", "limit": 50}, "debug.console", map[string]any{"debug_id": "d-1", "limit": float64(50)}},
		{"debug_close", map[string]any{"debug_id": "d-1", "keep_process": true}, "debug.close", map[string]any{"debug_id": "d-1", "keep_process": true}},
	}
	for _, c := range cases {
		res := callTool(t, cs, c.tool, c.args)
		if res.IsError {
			t.Errorf("%s: %s", c.tool, text(res))
			continue
		}
		got := f.called(c.method)
		if len(got) == 0 {
			t.Errorf("%s did not call %s", c.tool, c.method)
			continue
		}
		gj, _ := json.Marshal(got[len(got)-1])
		wj, _ := json.Marshal(c.want)
		if string(gj) != string(wj) {
			t.Errorf("%s params = %s, want %s", c.tool, gj, wj)
		}
	}
}

func TestDebugAttachRefusesBothPortAndPID(t *testing.T) {
	f := startFakeAPI(t, map[string]handler{})
	cs := connect(t)
	if res := callTool(t, cs, "debug_attach", map[string]any{"host": "win", "port": 9229, "pid": 100}); !res.IsError {
		t.Error("debug_attach with both port and pid was sent")
	}
	if len(f.called("debug.attach")) != 0 {
		t.Error("a bad debug_attach reached the daemon")
	}
}
