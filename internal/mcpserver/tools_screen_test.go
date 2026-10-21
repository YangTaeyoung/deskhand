package mcpserver

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestScreenAndControlToolsMapToDaemon(t *testing.T) {
	methods := []string{"control.get", "control.acquire", "control.release", "observe", "input.click", "input.move", "input.drag", "input.scroll", "input.key", "input.type", "ui.tree", "ui.find", "ui.text", "ui.act", "ui.at", "win.list", "win.action"}
	h := map[string]handler{}
	for _, method := range methods {
		h[method] = func(map[string]any) (any, *rpcFail) { return envelope(nil), nil }
	}
	f := startFakeAPI(t, h)
	cs := connect(t)
	cases := []struct {
		tool, method string
		args         map[string]any
		want         map[string]any
	}{
		{"control_status", "control.get", map[string]any{"host": "win"}, map[string]any{"host_id": "win"}},
		{"control_request", "control.acquire", map[string]any{"host": "win"}, map[string]any{"host_id": "win", "as": "ai"}},
		{"control_release", "control.release", map[string]any{"host": "win"}, map[string]any{"host_id": "win"}},
		{"observe", "observe", map[string]any{"host": "win", "level": "ui", "settle_ms": 200}, map[string]any{"host_id": "win", "level": "ui", "settle_ms": float64(200)}},
		{"input_click", "input.click", map[string]any{"host": "win", "capture_id": "c-0123456789ab", "x": 12.5, "y": 4, "button": "right"}, map[string]any{"host_id": "win", "capture_id": "c-0123456789ab", "x": 12.5, "y": float64(4), "button": "right"}},
		{"input_move", "input.move", map[string]any{"host": "win", "capture_id": "c-0123456789ab", "x": 12, "y": 4}, map[string]any{"host_id": "win", "capture_id": "c-0123456789ab", "x": float64(12), "y": float64(4)}},
		{"input_drag", "input.drag", map[string]any{"host": "win", "capture_id": "c-0123456789ab", "x": 12, "y": 4, "to_x": 0, "to_y": 40}, map[string]any{"host_id": "win", "capture_id": "c-0123456789ab", "x": float64(12), "y": float64(4), "to_x": float64(0), "to_y": float64(40)}},
		{"input_scroll", "input.scroll", map[string]any{"host": "win", "capture_id": "c-0123456789ab", "x": 12, "y": 4, "dy": 2}, map[string]any{"host_id": "win", "capture_id": "c-0123456789ab", "x": float64(12), "y": float64(4), "dy": float64(2)}},
		{"input_key", "input.key", map[string]any{"host": "win", "keys": "ctrl+shift+esc"}, map[string]any{"host_id": "win", "keys": "ctrl+shift+esc"}},
		{"input_type", "input.type", map[string]any{"host": "win", "text": "안녕😀", "method": "auto"}, map[string]any{"host_id": "win", "text": "안녕😀", "method": "auto"}},
		{"ui_tree", "ui.tree", map[string]any{"host": "win", "window": "foreground", "filter": "interactive"}, map[string]any{"host_id": "win", "window": "foreground", "filter": "interactive"}},
		{"ui_find", "ui.find", map[string]any{"host": "win", "nameContains": "확인"}, map[string]any{"host_id": "win", "nameContains": "확인"}},
		{"ui_text", "ui.text", map[string]any{"host": "win", "ref": "e12"}, map[string]any{"host_id": "win", "ref": "e12"}},
		{"ui_act", "ui.act", map[string]any{"host": "win", "ref": "e12", "action": "invoke"}, map[string]any{"host_id": "win", "ref": "e12", "action": "invoke"}},
		{"ui_at", "ui.at", map[string]any{"host": "win", "capture_id": "c-0123456789ab", "x": 2, "y": 3}, map[string]any{"host_id": "win", "capture_id": "c-0123456789ab", "x": float64(2), "y": float64(3)}},
		{"win_list", "win.list", map[string]any{"host": "win"}, map[string]any{"host_id": "win"}},
		{"win_action", "win.action", map[string]any{"host": "win", "window": "0x123", "action": "focus"}, map[string]any{"host_id": "win", "window": "0x123", "action": "focus"}},
	}
	for _, c := range cases {
		res := callTool(t, cs, c.tool, c.args)
		if res.IsError {
			t.Errorf("%s: %s", c.tool, text(res))
			continue
		}
		calls := f.called(c.method)
		if len(calls) == 0 {
			t.Errorf("%s did not call %s", c.tool, c.method)
			continue
		}
		got, _ := json.Marshal(calls[len(calls)-1])
		want, _ := json.Marshal(c.want)
		if !bytes.Equal(got, want) {
			t.Errorf("%s params=%s, want %s", c.tool, got, want)
		}
	}
}

func TestScreenCaptureReturnsImageAndCoordinateMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.png")
	data := []byte("\x89PNG\r\n\x1a\nimage bytes")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f := startFakeAPI(t, map[string]handler{"screen.capture": func(map[string]any) (any, *rpcFail) {
		return map[string]any{"capture_id": "c-0123456789ab", "host_id": "win", "display": 0, "width": 640, "height": 360, "format": "png", "artifact": map[string]any{"path": path}}, nil
	}})
	res := callTool(t, connect(t), "screen_capture", map[string]any{"host": "win"})
	if res.IsError || structured(t, res)["capture_id"] != "c-0123456789ab" || len(res.Content) != 2 {
		t.Fatalf("capture result: %+v", res)
	}
	img, ok := res.Content[1].(*mcp.ImageContent)
	if !ok || img.MIMEType != "image/png" {
		t.Fatalf("image content: %T %+v", res.Content[1], res.Content[1])
	}
	if !bytes.Equal(img.Data, data) {
		t.Fatalf("image data %q, want %q", img.Data, data)
	}
	if got := f.called("screen.capture"); len(got) != 1 || got[0]["max_width"] != float64(1568) {
		t.Fatalf("capture params: %v", got)
	}
}

func TestScreenInputRespectsHumanControlError(t *testing.T) {
	startFakeAPI(t, map[string]handler{"input.click": func(map[string]any) (any, *rpcFail) {
		return envelope(map[string]any{"status": "failed", "action": map[string]any{"executed": false, "outcome": "not_started"}, "error": map[string]any{"code": "CONTROL_HELD_BY_HUMAN", "message": "사람이 조작 중", "retryable": false, "next_action": "human_release"}}), nil
	}})
	res := callTool(t, connect(t), "input_click", map[string]any{"host": "win", "capture_id": "c-0123456789ab", "x": 1, "y": 2})
	if !res.IsError || structured(t, res)["code"] != "CONTROL_HELD_BY_HUMAN" || !strings.Contains(text(res), "human_release") {
		t.Fatalf("denied input: %+v", res)
	}
}
