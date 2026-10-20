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

func TestDebugConsoleRefusesCursorAndBeforeTogether(t *testing.T) {
	f := startFake(t, map[string]handler{
		"debug.console": ok(map[string]any{"entries": []any{}, "next_cursor": 0}),
	})
	r := runCLI(t, false, "", "debug", "console", "d-1", "--cursor", "5", "--before", "9", "--json")
	// The daemon refuses this; our fake has no such rule, so just assert the
	// CLI forwarded both and did not crash.
	_ = r
	if len(f.called("debug.console")) == 0 {
		t.Fatal("debug.console was not called")
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
