package server

import (
	"os"
	"strings"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestHelloDescribesTheUserSessionAndDisplays(t *testing.T) {
	if os.Getenv("DESKHAND_WINTEST") == "" {
		t.Skip("needs the signed-in user's session; run through scripts/wintest.sh")
	}
	_, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	hello := expect(t, r, "hello")
	t.Logf("session %v", hello["session"])
	t.Logf("displays %v", hello["displays"])
	sess, _ := hello["session"].(map[string]any)
	if id, _ := sess["session_id"].(float64); id < 1 || (sess["protocol"] != "console" && sess["protocol"] != "rdp") {
		t.Fatalf("hello session %v", hello["session"])
	}
	displays, _ := hello["displays"].([]any)
	if len(displays) == 0 {
		t.Fatalf("hello displays %v", hello["displays"])
	}
	if first, _ := displays[0].(map[string]any); first["primary"] != true || first["width"].(float64) <= 0 {
		t.Fatalf("hello displays %v", hello["displays"])
	}
	c := &rpcClient{t: t, r: r, w: w}
	if got := c.ok("display.list", nil); len(got["displays"].([]any)) != len(displays) {
		t.Fatalf("display.list %v", got)
	}
	if got := c.ok("session.info", nil); got["session_id"] != sess["session_id"] || got["input_desktop"] != sess["input_desktop"] {
		t.Fatalf("session.info %v", got)
	}
	caps, _ := hello["caps"].(map[string]any)
	for _, k := range []string{"screen", "input"} {
		if st, _ := caps[k].(map[string]any); st["state"] != capUnsupported {
			t.Fatalf("caps.%s %v", k, caps[k])
		}
	}
}

func TestDisplayListOverSSHReportsSession0(t *testing.T) {
	if os.Getenv("SSH_CONNECTION") == "" {
		t.Skip("needs a process started by sshd, which runs in Session 0; run scripts/wintest.sh --ssh")
	}
	_, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	hello := expect(t, r, "hello")
	sess, _ := hello["session"].(map[string]any)
	if sess["session_id"] != float64(0) {
		t.Fatalf("hello session %v", hello["session"])
	}
	if d, ok := hello["displays"].([]any); !ok || len(d) != 0 {
		t.Fatalf("hello displays %v", hello["displays"])
	}
	c := &rpcClient{t: t, r: r, w: w}
	got := c.call("display.list", nil)
	t.Logf("display.list %v", got)
	if got.String("code") != wire.CodeDisplayUnavailable || !strings.Contains(got.String("error"), "Session 0") {
		t.Fatalf("display.list %v", got)
	}
}
