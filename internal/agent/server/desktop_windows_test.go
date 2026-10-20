package server

import (
	"bytes"
	"encoding/base64"
	"image/jpeg"
	"os"
	"strings"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/proto"
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
	if st, _ := caps["input"].(map[string]any); st["state"] != capReady {
		t.Fatalf("caps.input %v in the user's session", caps["input"])
	}
	screen, _ := caps["screen"].(map[string]any)
	t.Logf("caps.screen %v", screen)
	unlockedConsole := sess["protocol"] == "console" && sess["input_desktop"] == "Default" && sess["locked"] == false
	if (unlockedConsole && screen["state"] != capReady) || screen["state"] == capUnsupported {
		t.Fatalf("caps.screen %v in session %v", screen, sess)
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
	caps, _ := hello["caps"].(map[string]any)
	if st, _ := caps["input"].(map[string]any); st["state"] != capUnavailable || st["reason"] != "session0" {
		t.Fatalf("caps.input %v in Session 0", caps["input"])
	}
	c := &rpcClient{t: t, r: r, w: w}
	got := c.call("display.list", nil)
	t.Logf("display.list %v", got)
	if got.String("code") != wire.CodeDisplayUnavailable || !strings.Contains(got.String("error"), "Session 0") {
		t.Fatalf("display.list %v", got)
	}
}

func TestScreenCaptureInTheUserSession(t *testing.T) {
	if os.Getenv("DESKHAND_WINTEST") == "" {
		t.Skip("needs the signed-in user's session; run through scripts/wintest.sh")
	}
	_, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	expect(t, r, "hello")
	c := &rpcClient{t: t, r: r, w: w}
	got := c.call("screen.capture", proto.Message{"display": 0, "format": "jpeg", "max_width": 800})
	if got["ok"] != true {
		if code := got.String("code"); code == wire.CodeDisplayUnavailable || code == wire.CodeSessionLocked {
			t.Skipf("the session cannot be captured now: %v", got)
		}
		t.Fatalf("screen.capture %v", got)
	}
	data, err := base64.StdEncoding.DecodeString(got.String("data"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	origin, _ := got["origin"].(map[string]any)
	t.Logf("screen.capture %v×%v scale %v origin %v dpi %v method %v id %v, %d bytes", got["width"], got["height"], got["scale"], origin,
		got["dpi"], got["method"], got["capture_id"], len(data))
	if err != nil || cfg.Width != 800 || float64(cfg.Height) != got["height"] || got["width"] != float64(800) || origin == nil ||
		!strings.HasPrefix(got.String("capture_id"), "c-") || got["mono_us"].(float64) <= 0 {
		t.Fatalf("screen.capture %v: image %+v %v", got, cfg, err)
	}
}

func TestScreenCaptureOverSSHReportsSession0(t *testing.T) {
	if os.Getenv("SSH_CONNECTION") == "" {
		t.Skip("needs a process started by sshd, which runs in Session 0; run scripts/wintest.sh --ssh")
	}
	_, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	hello := expect(t, r, "hello")
	caps, _ := hello["caps"].(map[string]any)
	if st, _ := caps["screen"].(map[string]any); st["state"] != capUnavailable || st["reason"] != "session0" {
		t.Fatalf("caps.screen %v", caps["screen"])
	}
	c := &rpcClient{t: t, r: r, w: w}
	got := c.call("screen.capture", nil)
	t.Logf("screen.capture %v", got)
	if got.String("code") != wire.CodeDisplayUnavailable || !strings.Contains(got.String("error"), "session0") || got["retryable"] != false {
		t.Fatalf("screen.capture %v", got)
	}
}
