package server

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

var (
	testSession = session.Info{SessionID: 1, User: `PC\apxkf`, SID: "S-1-5-21-1-2-3-1001", ConnectState: "active",
		Protocol: "console", Locked: false, InputDesktop: "Default", Integrity: "high"}
	testDisplays = []display.Info{
		{Index: 0, Name: `\\.\DISPLAY1`, Adapter: "GPU", X: 0, Y: 0, Width: 3840, Height: 2160, DPI: 144, Scale: 1.5, Primary: true, RefreshHz: 60},
		{Index: 1, Name: `\\.\DISPLAY2`, Adapter: "GPU", X: 3840, Y: 0, Width: 1920, Height: 1080, DPI: 96, Scale: 1, RefreshHz: 75},
	}
)

// wantSessionFields are the session.info keys of testSession.
var wantSessionFields = map[string]any{"session_id": float64(1), "user": `PC\apxkf`, "sid": "S-1-5-21-1-2-3-1001",
	"connect_state": "active", "protocol": "console", "locked": false, "input_desktop": "Default", "integrity": "high"}

// wantDisplays are testDisplays as JSON values.
func wantDisplays(t *testing.T) []any {
	t.Helper()
	b, err := json.Marshal(testDisplays)
	if err != nil {
		t.Fatal(err)
	}
	var v []any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// mediaClient connects to a server without exec whose session and display
// sources answer as given.
func mediaClient(t *testing.T, info session.Info, infoErr error, displays []display.Info, displayErr error) (*rpcClient, proto.Message) {
	t.Helper()
	_, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir(),
		SessionInfo: func() (session.Info, error) { return info, infoErr },
		Displays:    func() ([]display.Info, error) { return displays, displayErr }})
	hello := expect(t, r, "hello")
	return &rpcClient{t: t, r: r, w: w}, hello
}

func onlyKeys(m proto.Message, keys map[string]any) map[string]any {
	out := map[string]any{}
	for k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}

func TestSessionInfoReturnsTheProtocolFields(t *testing.T) {
	c, _ := mediaClient(t, testSession, nil, testDisplays, nil)
	got := c.ok("session.info", nil)
	if fields := onlyKeys(got, wantSessionFields); !reflect.DeepEqual(fields, wantSessionFields) {
		t.Fatalf("session.info %v", got)
	}
}

func TestDisplayListReturnsTheDisplays(t *testing.T) {
	c, _ := mediaClient(t, testSession, nil, testDisplays, nil)
	got := c.ok("display.list", nil)
	if !reflect.DeepEqual(got["displays"], wantDisplays(t)) {
		t.Fatalf("display.list %v", got)
	}
}

func TestHelloCarriesTheSessionAndDisplays(t *testing.T) {
	_, hello := mediaClient(t, testSession, nil, testDisplays, nil)
	sess, _ := hello["session"].(map[string]any)
	if !reflect.DeepEqual(sess, wantSessionFields) {
		t.Fatalf("hello session %v", hello["session"])
	}
	if !reflect.DeepEqual(hello["displays"], wantDisplays(t)) {
		t.Fatalf("hello displays %v", hello["displays"])
	}
}

func TestDisplayListInSessionZeroIsUnavailable(t *testing.T) {
	zero := testSession
	zero.SessionID, zero.ConnectState, zero.InputDesktop = 0, "disconnected", "unknown"
	c, hello := mediaClient(t, zero, nil, nil, errors.New("the displays of this session cannot be read: DXGI_ERROR_NOT_CURRENTLY_AVAILABLE (0x887A0022)"))
	if d, ok := hello["displays"].([]any); !ok || len(d) != 0 {
		t.Fatalf("hello displays %v", hello["displays"])
	}
	got := c.call("display.list", nil)
	if got["ok"] != false || got.String("code") != wire.CodeDisplayUnavailable || !strings.Contains(got.String("error"), "Session 0") {
		t.Fatalf("display.list %v", got)
	}
}

func TestSessionAndDisplayOpsUnsupportedOffWindows(t *testing.T) {
	c, hello := mediaClient(t, session.Info{}, session.ErrUnsupported, nil, display.ErrUnsupported)
	if d, ok := hello["displays"].([]any); !ok || len(d) != 0 {
		t.Fatalf("hello displays %v", hello["displays"])
	}
	for _, op := range []string{"session.info", "display.list"} {
		if got := c.call(op, nil); got["ok"] != false || got.String("code") != wire.CodeCapabilityUnsupported || got.String("error") == "" {
			t.Fatalf("%s %v", op, got)
		}
	}
}

func TestSessionCapFollowsTheSession(t *testing.T) {
	zero := testSession
	zero.SessionID = 0
	for _, c := range []struct {
		name        string
		implemented bool
		info        session.Info
		err         error
		want        capState
	}{
		{"not implemented", false, testSession, nil, capState{State: capUnsupported, Reason: notImplemented}},
		{"user session", true, testSession, nil, capState{State: capReady}},
		{"session 0", true, zero, nil, capState{State: capUnavailable, Reason: "session0"}},
		{"no windows", true, session.Info{}, session.ErrUnsupported, capState{State: capUnsupported, Reason: session.ErrUnsupported.Error()}},
		{"unreadable", true, session.Info{}, errors.New("WTSQuerySessionInformation failed"), capState{State: capUnavailable, Reason: "WTSQuerySessionInformation failed"}},
	} {
		if got := sessionCap(c.implemented, c.info, c.err); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}
