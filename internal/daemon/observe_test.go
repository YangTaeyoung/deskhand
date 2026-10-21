package daemon_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/agent/monoclock"
	"github.com/YangTaeyoung/deskhand/internal/agent/quicserve"
	"github.com/YangTaeyoung/deskhand/internal/agent/server"
	"github.com/YangTaeyoung/deskhand/internal/client"
	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/wire"
	"github.com/quic-go/quic-go"
)

// fakeScreen is the test agent's screen: display 0 of 3840×2160 at (0, 0)
// and display 1 of 3840×2160 at (3840, 0). A capture at the default width
// 1568 reports scale 0.408; every capture is one gray shade, which shade
// returns for the n-th capture.
type fakeScreen struct {
	mu    sync.Mutex
	n     int
	shade func(n int) uint8
}

func (f *fakeScreen) displays() ([]display.Info, error) {
	return []display.Info{
		{Index: 0, Name: `\\.\DISPLAY1`, X: 0, Y: 0, Width: 3840, Height: 2160, DPI: 144, Scale: 1.5, Primary: true, RefreshHz: 60},
		{Index: 1, Name: `\\.\DISPLAY2`, X: 3840, Y: 0, Width: 3840, Height: 2160, DPI: 144, Scale: 1.5, RefreshHz: 60},
	}, nil
}

func (f *fakeScreen) capture(disp int, format string, maxWidth, _ int) (capture.StillResult, error) {
	if disp > 1 {
		return capture.StillResult{}, &capture.Unavailable{Reason: capture.ReasonNoOutput}
	}
	f.mu.Lock()
	f.n++
	shade := uint8(200)
	if f.shade != nil {
		shade = f.shade(f.n)
	}
	f.mu.Unlock()
	w := 3840
	if maxWidth > 0 && maxWidth < w {
		w = maxWidth
	}
	h := (2160*w + 1920) / 3840
	scale := float64(w) / 3840
	if w == capture.DefaultStillMaxWidth {
		scale = 0.408
	}
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = shade
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return capture.StillResult{}, err
	}
	return capture.StillResult{CaptureID: newTestCaptureID(), Data: buf.Bytes(), Width: w, Height: h, Scale: scale,
		OriginX: 3840 * disp, OriginY: 0, DPI: 144, MonoUS: monoclock.NowUS(), Method: "dd"}, nil
}

var (
	captureSeqMu sync.Mutex
	captureSeq   int
)

func newTestCaptureID() string {
	captureSeqMu.Lock()
	defer captureSeqMu.Unlock()
	captureSeq++
	return "c-" + hex.EncodeToString([]byte{0, 0, 0, 0, byte(captureSeq >> 8), byte(captureSeq)})
}

func (f *fakeScreen) tune(o *server.Options) {
	o.Capture = f.capture
	o.Displays = f.displays
}

// inputRecorder serves the agent's input streams: it records each event
// and acks it, or with drop set resets the stream after reading the next
// event without acking it.
type inputRecorder struct {
	mu     sync.Mutex
	events []wire.InputEvent
	gens   []uint64
	drop   bool
}

func (r *inputRecorder) streams() quicserve.StreamHandlers {
	return quicserve.StreamHandlers{"input": func(ctx context.Context, st *quic.Stream, h wire.StreamHeader) {
		defer st.Close()
		if wire.WriteHeader(st, map[string]any{"ok": true, "gen": h.Gen}) != nil {
			return
		}
		br := bufio.NewReader(st)
		for {
			kind, payload, err := wire.ReadFrame(br, 4<<20)
			if err != nil {
				return
			}
			var ev wire.InputEvent
			if kind != wire.KindClientInput || json.Unmarshal(payload, &ev) != nil {
				continue
			}
			r.mu.Lock()
			r.events = append(r.events, ev)
			r.gens = append(r.gens, h.Gen)
			drop := r.drop
			r.mu.Unlock()
			if drop {
				st.CancelRead(0)
				st.CancelWrite(0)
				return
			}
			b, _ := json.Marshal(wire.InputAck{Seq: ev.Seq, OK: true, DoneMonoUS: monoclock.NowUS()})
			if wire.WriteFrame(st, wire.KindInputAck, b) != nil {
				return
			}
		}
	}}
}

func (r *inputRecorder) recorded() []wire.InputEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]wire.InputEvent(nil), r.events...)
}

func (r *inputRecorder) setDrop(drop bool) {
	r.mu.Lock()
	r.drop = drop
	r.mu.Unlock()
}

// screenEnv is a daemon whose agent has the fake screen, records input
// and serves ui, with an AI client and an app client.
type screenEnv struct {
	*env
	screen *fakeScreen
	input  *inputRecorder
	ui     *fakeUI
	ai     *client.Client
	app    *client.Client
}

func setupScreen(t *testing.T) *screenEnv {
	t.Helper()
	f, in, ui := &fakeScreen{}, &inputRecorder{}, &fakeUI{open: true}
	e := setup(t, setupOpts{trustDaemon: true, agentTune: f.tune, agentStreams: in.streams(), agentUI: ui})
	e.waitState(t, "ready")
	return &screenEnv{env: e, screen: f, input: in, ui: ui, ai: dialAs(t, "mcp", "claude"), app: dialAs(t, "app", "Deskhand")}
}

func (se *screenEnv) capture(t *testing.T, disp int) map[string]any {
	t.Helper()
	return mustCall(t, se.ai, "screen.capture", map[string]any{"host_id": "pc", "display": disp})
}

// wantEnvelope fails unless out is an envelope of status, executed (nil
// for unknown), outcome and error code (empty for none).
func wantEnvelope(t *testing.T, out map[string]any, status string, executed any, outcome, code string) {
	t.Helper()
	act, _ := out["action"].(map[string]any)
	errv, _ := out["error"].(map[string]any)
	gotCode := ""
	if errv != nil {
		gotCode, _ = errv["code"].(string)
	}
	if out["status"] != status || act["executed"] != executed || act["outcome"] != outcome || gotCode != code {
		t.Fatalf("envelope %v, want %s/%v/%s/%q", out, status, executed, outcome, code)
	}
	for _, k := range envelopeKeys {
		if _, ok := out[k]; !ok {
			t.Fatalf("envelope without %s: %v", k, out)
		}
	}
}

func TestClickMapsCaptureToDisplayPixels(t *testing.T) {
	se := setupScreen(t)
	c := se.capture(t, 1)
	origin, _ := c["origin"].(map[string]any)
	if c["width"] != float64(1568) || c["height"] != float64(882) || c["scale"] != 0.408 || origin["x"] != float64(3840) || origin["y"] != float64(0) ||
		c["display"] != float64(1) || c["format"] != "png" || c["method"] != "dd" {
		t.Fatalf("capture %v", c)
	}
	id, _ := c["capture_id"].(string)
	art, _ := c["artifact"].(map[string]any)
	data, err := os.ReadFile(art["path"].(string))
	if err != nil || art["kind"] != "screenshot" || art["mime"] != "image/png" {
		t.Fatalf("artifact %v: %v", art, err)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != art["sha256"] {
		t.Fatal("the artifact's sha256 is not its file's")
	}

	out := mustCall(t, se.ai, "input.click", map[string]any{"host_id": "pc", "capture_id": id, "x": 400, "y": 300})
	wantEnvelope(t, out, "succeeded", true, "done", "")
	tg, _ := out["target"].(map[string]any)
	if tg["display"] != float64(1) || tg["x"] != float64(981) || tg["y"] != float64(736) || tg["screen_x"] != float64(4821) || tg["screen_y"] != float64(736) {
		t.Fatalf("target %v", tg)
	}
	if out["events_total"] != float64(3) || out["events_done"] != float64(3) {
		t.Fatalf("events %v/%v", out["events_done"], out["events_total"])
	}
	evs := se.input.recorded()
	st := mustCall(t, se.app, "control.get", pc)
	if len(evs) != 3 || evs[0].K != "move" || evs[0].Display != 1 || evs[0].X != 981 || evs[0].Y != 736 ||
		evs[1].K != "button" || evs[1].B != "left" || !evs[1].Down || evs[2].K != "button" || evs[2].Down {
		t.Fatalf("events at the agent %+v", evs)
	}
	for _, ev := range evs {
		if float64(ev.Gen) != st["gen"] {
			t.Fatalf("event gen %d, control gen %v", ev.Gen, st["gen"])
		}
	}
	if st["owner"] != "ai" || st["owner_client_id"] != se.ai.ClientID {
		t.Fatalf("control after an AI click %v", st)
	}

	out = mustCall(t, se.ai, "input.click", map[string]any{"host_id": "pc", "capture_id": id, "x": 0, "y": 0, "button": "right", "count": 2})
	wantEnvelope(t, out, "succeeded", true, "done", "")
	if evs := se.input.recorded()[3:]; len(evs) != 5 || evs[0].X != 1 || evs[0].Y != 1 || evs[1].B != "right" || evs[3].B != "right" {
		t.Fatalf("double right click %+v", evs)
	}
	for _, bad := range []map[string]any{
		{"host_id": "pc", "capture_id": id, "x": 1568, "y": 10},
		{"host_id": "pc", "capture_id": id, "x": 10, "y": -1},
		{"host_id": "pc", "capture_id": id, "x": 10},
		{"host_id": "pc", "capture_id": id, "x": 10, "y": 10, "button": "side"},
	} {
		if _, e := callErr(t, se.ai, "input.click", bad); e == nil || e.Code != wire.CodeInvalidArgument {
			t.Fatalf("input.click %v: %+v", bad, e)
		}
	}
	if n := len(se.input.recorded()); n != 8 {
		t.Fatalf("refused clicks reached the agent: %d events", n)
	}
	tl := mustCall(t, se.app, "timeline.list", map[string]any{"host_id": "pc", "limit": 50})["entries"].([]any)
	kinds := map[string]bool{}
	for _, it := range tl {
		en := it.(map[string]any)
		if en["actor"] == "ai" {
			kinds[en["kind"].(string)] = true
		}
	}
	if !kinds["input"] || !kinds["observe"] {
		t.Fatalf("AI timeline kinds %v", kinds)
	}
}

func TestClickWithStaleCaptureRejected(t *testing.T) {
	se := setupScreen(t)
	id := se.capture(t, 0)["capture_id"].(string)
	before := se.host(t).Gen
	mustCall(t, se.app, "host.disconnect", pc)
	mustCall(t, se.app, "host.connect", pc)
	if h := se.waitState(t, "ready"); h.Gen == before {
		t.Fatalf("connection generation %v after the reconnect", h.Gen)
	}
	out := mustCall(t, se.ai, "input.click", map[string]any{"host_id": "pc", "capture_id": id, "x": 10, "y": 10})
	wantEnvelope(t, out, "failed", false, "not_started", wire.CodeStaleTarget)
	if out["next_action"] != apierr.NextScreenCapture || !strings.Contains(out["error"].(map[string]any)["message"].(string), "다시 캡처") {
		t.Fatalf("stale capture %v", out)
	}
	out = mustCall(t, se.ai, "input.click", map[string]any{"host_id": "pc", "capture_id": "c-0123456789ab", "x": 10, "y": 10})
	wantEnvelope(t, out, "failed", false, "not_started", wire.CodeStaleTarget)
	if n := len(se.input.recorded()); n != 0 {
		t.Fatalf("a stale click reached the agent: %d events", n)
	}
	tl := mustCall(t, se.app, "timeline.list", map[string]any{"host_id": "pc", "limit": 5})["entries"].([]any)
	if en := tl[0].(map[string]any); en["kind"] != "input" || en["outcome"] != "failed" || en["actor"] != "ai" || !strings.Contains(en["summary"].(string), "c-0123456789ab") {
		t.Fatalf("timeline of a stale click %v", en)
	}
	id = se.capture(t, 0)["capture_id"].(string)
	wantEnvelope(t, mustCall(t, se.ai, "input.click", map[string]any{"host_id": "pc", "capture_id": id, "x": 10, "y": 10}),
		"succeeded", true, "done", "")
}

func TestCaptureStaleAfterDisplayChange(t *testing.T) {
	se := setupScreen(t)
	evs, err := se.app.SubscribeHost(context.Background(), "pc", []string{"display.changed"})
	if err != nil {
		t.Fatal(err)
	}
	id := se.capture(t, 1)["capture_id"].(string)
	list, _ := se.screen.displays()
	se.agent.srv.HandleUIEvent("display.changed", map[string]any{"displays": list[:1]})
	select {
	case <-evs:
	case <-time.After(5 * time.Second):
		t.Fatal("no display.changed")
	}
	out := mustCall(t, se.ai, "input.move", map[string]any{"host_id": "pc", "capture_id": id, "x": 10, "y": 10})
	wantEnvelope(t, out, "failed", false, "not_started", wire.CodeStaleTarget)
	if out["next_action"] != apierr.NextScreenCapture || !strings.Contains(out["error"].(map[string]any)["message"].(string), "디스플레이 구성") {
		t.Fatalf("capture before a display change %v", out)
	}
	if n := len(se.input.recorded()); n != 0 {
		t.Fatalf("input reached the agent: %d events", n)
	}
	id = se.capture(t, 0)["capture_id"].(string)
	wantEnvelope(t, mustCall(t, se.ai, "input.move", map[string]any{"host_id": "pc", "capture_id": id, "x": 10, "y": 10}),
		"succeeded", true, "done", "")
}

func TestInputUnknownWhenAckLost(t *testing.T) {
	se := setupScreen(t)
	se.input.setDrop(true)
	out := mustCall(t, se.ai, "input.key", map[string]any{"host_id": "pc", "keys": "enter"})
	wantEnvelope(t, out, "unknown", nil, "unknown", wire.CodeExecutionOutcomeUnknown)
	if out["next_action"] != apierr.NextCheckState || out["events_done"] != float64(0) {
		t.Fatalf("lost ack %v", out)
	}
	if len(se.input.recorded()) == 0 {
		t.Fatal("the event never reached the agent")
	}
	tl := mustCall(t, se.app, "timeline.list", map[string]any{"host_id": "pc", "limit": 5})["entries"].([]any)
	found := false
	for _, it := range tl {
		en := it.(map[string]any)
		if en["kind"] == "input" && en["outcome"] == "unknown" && en["actor"] == "ai" {
			found = true
		}
	}
	if !found {
		t.Fatalf("timeline %v", tl)
	}
	se.input.setDrop(false)
	wantEnvelope(t, mustCall(t, se.ai, "input.key", map[string]any{"host_id": "pc", "keys": "enter"}), "succeeded", true, "done", "")
}

func TestUIActGatedByControl(t *testing.T) {
	se := setupScreen(t)
	mustCall(t, se.app, "control.acquire", acquireAs("human", false))
	act := map[string]any{"host_id": "pc", "name": "Send", "action": "invoke"}
	out := mustCall(t, se.ai, "ui.act", act)
	wantEnvelope(t, out, "failed", false, "not_started", wire.CodeControlHeldByHuman)
	if out["next_action"] != apierr.NextHumanRelease {
		t.Fatalf("ui.act while a person holds %v", out)
	}
	wantEnvelope(t, mustCall(t, se.ai, "win.action", map[string]any{"host_id": "pc", "window": "0x10", "action": "close"}),
		"failed", false, "not_started", wire.CodeControlHeldByHuman)
	if out := mustCall(t, se.ai, "input.key", map[string]any{"host_id": "pc", "keys": "ctrl+s"}); out["status"] != "failed" ||
		out["error"].(map[string]any)["code"] != wire.CodeControlHeldByHuman {
		t.Fatalf("input.key while a person holds %v", out)
	}
	found := mustCall(t, se.ai, "ui.find", map[string]any{"host_id": "pc", "name": "Send"})
	if nodes, _ := found["nodes"].([]any); len(nodes) != 1 || found["host_id"] != "pc" {
		t.Fatalf("ui.find while a person holds %v", found)
	}
	se.ui.mu.Lock()
	acts, open := len(se.ui.acts), se.ui.open
	se.ui.mu.Unlock()
	if acts != 0 || !open || len(se.input.recorded()) != 0 {
		t.Fatalf("refused UI ops reached the PC: %d acts, open %v, %d events", acts, open, len(se.input.recorded()))
	}

	mustCall(t, se.app, "control.release", pc)
	out = mustCall(t, se.ai, "ui.act", act)
	wantEnvelope(t, out, "succeeded", true, "done", "")
	st := mustCall(t, se.app, "control.get", pc)
	if st["owner"] != "ai" || st["owner_client_id"] != se.ai.ClientID {
		t.Fatalf("control after an AI ui.act %v", st)
	}
	if g := se.agent.srv.Input().Gen(); float64(g) != st["gen"] {
		t.Fatalf("agent writer gen %d, control %v", g, st["gen"])
	}
	se.ui.mu.Lock()
	acts = len(se.ui.acts)
	se.ui.mu.Unlock()
	if acts != 1 {
		t.Fatalf("ui.act reached the helper %d times", acts)
	}

	mustCall(t, se.app, "control.stop", pc)
	out = mustCall(t, se.ai, "ui.act", act)
	wantEnvelope(t, out, "failed", false, "not_started", wire.CodeControlHeldByHuman)
	if out["next_action"] != apierr.NextHumanResumeAI {
		t.Fatalf("ui.act during the emergency stop %v", out)
	}

	tl := mustCall(t, se.app, "timeline.list", map[string]any{"host_id": "pc", "limit": 50})["entries"].([]any)
	var denied, observed int
	for _, it := range tl {
		en := it.(map[string]any)
		if en["actor"] != "ai" {
			continue
		}
		if en["kind"] == "input" && en["outcome"] == "denied" {
			denied++
		}
		if en["kind"] == "observe" && strings.Contains(en["summary"].(string), "ui.find") {
			observed++
		}
	}
	if denied < 4 || observed != 1 {
		t.Fatalf("timeline: %d denied AI inputs, %d ui.find reads", denied, observed)
	}
}

func TestInputTypeAndKeyReachTheAgent(t *testing.T) {
	se := setupScreen(t)
	out := mustCall(t, se.ai, "input.type", map[string]any{"host_id": "pc", "text": "안녕하세요\r\n"})
	wantEnvelope(t, out, "succeeded", true, "done", "")
	if out["method"] != "unicode" {
		t.Fatalf("type %v", out)
	}
	out = mustCall(t, se.ai, "input.type", map[string]any{"host_id": "pc", "text": "완료 🎉"})
	if out["method"] != "paste" || out["status"] != "succeeded" {
		t.Fatalf("type with an emoji %v", out)
	}
	out = mustCall(t, se.ai, "input.key", map[string]any{"host_id": "pc", "keys": "ctrl+shift+esc"})
	if out["keys"] != "ctrl+shift+esc" || out["events_done"] != float64(6) {
		t.Fatalf("key %v", out)
	}
	evs := se.input.recorded()
	if len(evs) != 8 || evs[0].K != "text" || evs[0].Method != "unicode" || evs[0].Text != "안녕하세요\r\n" || evs[1].Method != "paste" {
		t.Fatalf("events %+v", evs)
	}
	if k := evs[2:]; k[0].SC != 0x1d || !k[0].Down || k[1].SC != 0x2a || k[2].SC != 0x01 || k[3].SC != 0x01 || k[3].Down || k[5].SC != 0x1d {
		t.Fatalf("key events %+v", k)
	}
	_, e := callErr(t, se.ai, "input.key", map[string]any{"host_id": "pc", "keys": "cmd+q"})
	if e == nil || e.Code != wire.CodeInvalidArgument || !strings.Contains(e.Message, "ctrl") {
		t.Fatalf("an unknown key: %+v", e)
	}
}

func TestObserveWaitsForTheScreenToSettle(t *testing.T) {
	se := setupScreen(t)
	id := se.capture(t, 0)["capture_id"].(string)
	se.screen.mu.Lock()
	start := se.screen.n
	se.screen.shade = func(n int) uint8 {
		if n-start <= 3 {
			return uint8(20 * (n - start))
		}
		return 90
	}
	se.screen.mu.Unlock()
	out := mustCall(t, se.ai, "input.click", map[string]any{"host_id": "pc", "capture_id": id, "x": 5, "y": 5,
		"observe": map[string]any{"level": "summary", "settle_ms": 3000}})
	wantEnvelope(t, out, "succeeded", true, "done", "")
	o, _ := out["observation"].(map[string]any)
	if o == nil || o["settled"] != true || o["changed"] != true || o["level"] != "summary" {
		t.Fatalf("observation %v", out["observation"])
	}
	if errs, _ := o["errors"].([]any); len(errs) == 0 {
		t.Fatalf("the fake UI without ui.focused reported no part error: %v", o)
	}

	obs := mustCall(t, se.ai, "observe", map[string]any{"host_id": "pc", "level": "none", "settle_ms": 1000})
	if obs["settled"] != true || obs["changed"] != false || obs["foreground"] != nil {
		t.Fatalf("observe of a still screen %v", obs)
	}

	se.screen.mu.Lock()
	se.screen.shade = func(n int) uint8 { return uint8(n * 37) }
	se.screen.mu.Unlock()
	obs = mustCall(t, se.ai, "observe", map[string]any{"host_id": "pc", "level": "image", "settle_ms": 400})
	if obs["settled"] != false || obs["changed"] != true || obs["capture"] == nil {
		t.Fatalf("observe of a screen that keeps changing %v", obs)
	}
	if _, e := callErr(t, se.ai, "observe", map[string]any{"host_id": "pc", "level": "everything"}); e == nil || e.Code != wire.CodeInvalidArgument {
		t.Fatalf("an unknown level: %+v", e)
	}
}
