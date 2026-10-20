package cli

import (
	"strings"
	"testing"
)

func debugEnvelope(extra map[string]any) map[string]any {
	return envelope("succeeded", extra)
}

func TestDebugLaunchSendsInspectorRequest(t *testing.T) {
	session := map[string]any{"debug_id": "d-1", "host_id": "windows-dev", "name": "App", "state": "running",
		"targets": []any{map[string]any{"id": "node-1", "role": "main", "type": "node", "url": "file:///app/main.js", "paused": false}}}
	f := startFake(t, map[string]handler{
		"debug.electron.launch": ok(debugEnvelope(map[string]any{"session": session})),
	})
	r := runCLI(t, false, "", "debug", "launch", "windows-dev", `C:\app\App.exe`, "--break", "--bp", "file_name:main.js:10")
	if r.code != 0 {
		t.Fatalf("exit %d stderr %q", r.code, r.err)
	}
	p := f.called("debug.electron.launch")[0]
	if p["host_id"] != "windows-dev" || p["path"] != `C:\app\App.exe` || p["pause_on_start"] != true {
		t.Fatalf("launch params = %v", p)
	}
	bps, _ := p["breakpoints"].([]any)
	if len(bps) != 1 {
		t.Fatalf("breakpoints = %v", p["breakpoints"])
	}
	bp, _ := bps[0].(map[string]any)
	if bp["match"] != "file_name" || bp["pattern"] != "main.js" || bp["line"].(float64) != 10 {
		t.Fatalf("breakpoint spec = %v", bp)
	}
	if !strings.Contains(r.stdout, "node-1") {
		t.Fatalf("stdout did not show the target: %q", r.stdout)
	}
}

func TestDebugLaunchAndAttachOfAnOpenSessionExitZero(t *testing.T) {
	session := map[string]any{"debug_id": "d-1", "host_id": "windows-dev", "name": "App", "state": "paused",
		"targets": []any{map[string]any{"id": "node-1", "role": "main", "type": "node", "url": "file:///app/main.js", "paused": true}}}
	startFake(t, map[string]handler{
		"debug.electron.launch": ok(envelope("running", map[string]any{"session": session})),
		"debug.attach":          ok(envelope("running", map[string]any{"session": session})),
	})
	for _, args := range [][]string{
		{"debug", "launch", "windows-dev", `C:\app\App.exe`, "--break"},
		{"--json", "debug", "launch", "windows-dev", `C:\app\App.exe`},
		{"debug", "attach", "windows-dev", "--port", "9229"},
	} {
		r := runCLI(t, false, "", args...)
		if r.code != 0 || r.err != "" {
			t.Fatalf("%v: exit %d stderr %q", args, r.code, r.err)
		}
	}
}

func TestDebugLaunchBreakSpecParsesURLWithColonsFromTheRight(t *testing.T) {
	session := map[string]any{"debug_id": "d-1", "host_id": "windows-dev", "name": "App", "state": "running", "targets": []any{}}
	f := startFake(t, map[string]handler{
		"debug.electron.launch": ok(debugEnvelope(map[string]any{"session": session})),
	})
	runCLI(t, false, "", "debug", "launch", "windows-dev", `C:\app\App.exe`, "--bp", "url:file:///C:/app/main.js:12:4")
	p := f.called("debug.electron.launch")[0]
	bps, _ := p["breakpoints"].([]any)
	if len(bps) != 1 {
		t.Fatalf("breakpoints = %v", p["breakpoints"])
	}
	bp, _ := bps[0].(map[string]any)
	if bp["match"] != "url" || bp["pattern"] != "file:///C:/app/main.js" || bp["line"].(float64) != 12 || bp["column"].(float64) != 4 {
		t.Fatalf("breakpoint spec = %v", bp)
	}
}

func TestDebugStepPassesKind(t *testing.T) {
	f := startFake(t, map[string]handler{
		"debug.step": ok(debugEnvelope(map[string]any{"target": map[string]any{"id": "node-1", "paused": true}})),
	})
	r := runCLI(t, false, "", "debug", "step", "d-1", "node-1", "into")
	if r.code != 0 {
		t.Fatalf("exit %d stderr %q", r.code, r.err)
	}
	p := f.called("debug.step")[0]
	if p["debug_id"] != "d-1" || p["target_id"] != "node-1" || p["kind"] != "into" {
		t.Fatalf("step params = %v", p)
	}
}

func TestDebugConsoleForwardsCursorAndBefore(t *testing.T) {
	f := startFake(t, map[string]handler{
		"debug.console": ok(map[string]any{"entries": []any{}, "next_cursor": 0}),
	})
	runCLI(t, false, "", "debug", "console", "d-1", "--cursor", "5", "--before", "9", "--json")
	p := f.called("debug.console")[0]
	if p["cursor"] != float64(5) || p["before"] != float64(9) {
		t.Fatalf("console params = %v, want cursor 5 and before 9 forwarded", p)
	}
}

func TestDebugVarsPrintsProperties(t *testing.T) {
	startFake(t, map[string]handler{
		"debug.variables": ok(map[string]any{"properties": []any{
			map[string]any{"name": "total", "value_preview": "52000", "type": "number"}}, "total": 1}),
	})
	r := runCLI(t, false, "", "debug", "vars", "d-1", "node-1", "scope-1")
	if r.code != 0 || !strings.Contains(r.stdout, "total = 52000") {
		t.Fatalf("vars output = %q (exit %d)", r.stdout, r.code)
	}
}
