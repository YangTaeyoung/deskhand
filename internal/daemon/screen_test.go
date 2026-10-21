package daemon_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/monoclock"
	"github.com/YangTaeyoung/deskhand/internal/agent/quicserve"
	"github.com/YangTaeyoung/deskhand/internal/client"
	"github.com/YangTaeyoung/deskhand/internal/wire"
	"github.com/quic-go/quic-go"
)

// screenAgent serves video, cursor and input streams in the test's agent:
// a video stream writes an info frame and then the access units the test
// sends; an input stream records its events and acks each.
type screenAgent struct {
	aus chan wire.AU

	mu      sync.Mutex
	videos  []wire.StreamHeader
	cursors int
	events  []wire.InputEvent
}

func newScreenAgent() *screenAgent { return &screenAgent{aus: make(chan wire.AU, 16)} }

func (a *screenAgent) streams() quicserve.StreamHandlers {
	return quicserve.StreamHandlers{
		"video": func(ctx context.Context, st *quic.Stream, h wire.StreamHeader) {
			defer st.Close()
			defer st.CancelRead(0)
			a.mu.Lock()
			a.videos = append(a.videos, h)
			a.mu.Unlock()
			info, _ := json.Marshal(wire.VideoInfo{Codec: "h264", Width: 1280, Height: 720, SrcWidth: 2560, SrcHeight: 1440, FPS: 60,
				Bitrate: h.Bitrate, Encoder: "test", Capture: "dd", Display: h.Display})
			if wire.WriteFrame(st, wire.KindVideoInfo, info) != nil {
				return
			}
			ended := make(chan struct{})
			go func() {
				defer close(ended)
				br := bufio.NewReader(st)
				for {
					if _, _, err := wire.ReadFrame(br, 1<<20); err != nil {
						return
					}
				}
			}()
			for {
				select {
				case au := <-a.aus:
					au.CaptureMonoUS = monoclock.NowUS()
					if wire.WriteFrame(st, wire.KindAU, au.Marshal()) != nil {
						return
					}
				case <-ended:
					return
				case <-ctx.Done():
					return
				}
			}
		},
		"cursor": func(ctx context.Context, st *quic.Stream, h wire.StreamHeader) {
			defer st.Close()
			defer st.CancelRead(0)
			a.mu.Lock()
			a.cursors++
			a.mu.Unlock()
			if wire.WriteHeader(st, map[string]any{"ok": true}) != nil {
				return
			}
			br := bufio.NewReader(st)
			for {
				if _, _, err := wire.ReadFrame(br, 1<<20); err != nil {
					return
				}
			}
		},
		"input": func(ctx context.Context, st *quic.Stream, h wire.StreamHeader) {
			defer st.Close()
			defer st.CancelRead(0)
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
				a.mu.Lock()
				a.events = append(a.events, ev)
				a.mu.Unlock()
				b, _ := json.Marshal(wire.InputAck{Seq: ev.Seq, OK: true, DoneMonoUS: monoclock.NowUS()})
				if wire.WriteFrame(st, wire.KindInputAck, b) != nil {
					return
				}
			}
		},
	}
}

func (a *screenAgent) inputEvents() []wire.InputEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]wire.InputEvent(nil), a.events...)
}

func (a *screenAgent) videoHeaders() []wire.StreamHeader {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]wire.StreamHeader(nil), a.videos...)
}

// screenFrames reads the frames of a screen stream into a channel.
func screenFrames(s *client.Stream) chan ptyFrame {
	ch := make(chan ptyFrame, 4096)
	go func() {
		defer close(ch)
		for {
			kind, payload, err := s.ReadFrame(wire.MaxFrame)
			if err != nil {
				return
			}
			ch <- ptyFrame{kind, payload}
		}
	}()
	return ch
}

// nextFrame waits up to 10 s for the next frame of kind on ch whose
// payload accepts, skipping others.
func nextFrame(t *testing.T, ch chan ptyFrame, kind byte, accept func([]byte) bool) []byte {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				t.Fatalf("the screen stream ended waiting for a %#x frame", kind)
			}
			if f.kind == kind && (accept == nil || accept(f.payload)) {
				return f.payload
			}
		case <-timeout:
			t.Fatalf("no %#x frame within 10 s", kind)
		}
	}
}

func noticeOf(typ string) func([]byte) bool {
	return func(b []byte) bool {
		var m map[string]any
		return json.Unmarshal(b, &m) == nil && m["type"] == typ
	}
}

func attachScreen(t *testing.T, params map[string]any) (*client.Stream, error) {
	t.Helper()
	return attachScreenAs(t, "app", params)
}

// attachScreenAs attaches a screen stream as a client of kind.
func attachScreenAs(t *testing.T, kind string, params map[string]any) (*client.Stream, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := client.Attach(ctx, client.Options{Start: func(context.Context) error { return errors.New("no daemon") }}, kind, "Deskhand", "screen", params)
	if err == nil {
		t.Cleanup(func() { s.Close() })
	}
	return s, err
}

func TestScreenStreamRelaysTheAgentScreenAndHumanInput(t *testing.T) {
	agent := newScreenAgent()
	e := setup(t, setupOpts{trustDaemon: true, agentStreams: agent.streams()})
	e.waitState(t, "ready")
	s, err := attachScreen(t, map[string]any{"host_id": "pc", "display": 0, "preset": "saver"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Result["host_id"] != "pc" || s.Result["display"] != float64(0) || s.Result["preset"] != "saver" {
		t.Fatalf("stream.attach result %v", s.Result)
	}
	frames := screenFrames(s)
	var info wire.VideoInfo
	if err := json.Unmarshal(nextFrame(t, frames, wire.KindVideoInfo, nil), &info); err != nil || info.SrcWidth != 2560 {
		t.Fatalf("info %+v %v", info, err)
	}
	if h := agent.videoHeaders(); len(h) != 1 || h[0].MaxWidth != 1920 || h[0].Bitrate != 4_000_000 {
		t.Fatalf("agent video headers %+v", h)
	}
	agent.aus <- wire.AU{Key: true, HasConfig: true, Width: 1280, Height: 720, Seq: 1, Data: []byte{0, 0, 0, 1, 0x65}}
	au, err := wire.UnmarshalAU(nextFrame(t, frames, wire.KindAU, nil))
	if err != nil || au.Seq != 1 || !au.Key {
		t.Fatalf("access unit %+v %v", au, err)
	}
	if err := s.WriteFrame(wire.KindClientInput, []byte(`{"seq":41,"gen":0,"k":"key","sc":30,"ext":false,"down":true}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the key at the agent", func() bool { return len(agent.inputEvents()) == 1 })
	ctl := mustCall(t, e.c, "control.get", pc)
	ev := agent.inputEvents()[0]
	if ctl["owner"] != "human" || ctl["owner_client"] != "Deskhand" || ev.K != "key" || ev.SC != 30 || !ev.Down ||
		float64(ev.Gen) != ctl["gen"] || ev.Gen == 0 || ev.Seq != 1 {
		t.Fatalf("event %+v with control %v, want the key of gen %v from the viewer's human control", ev, ctl, ctl["gen"])
	}
	for i := 0; i < 3; i++ {
		agent.aus <- wire.AU{Width: 1280, Height: 720, Seq: uint64(2 + i), Data: []byte{0, 0, 0, 1, 0x41}}
	}
	var lat map[string]any
	_ = json.Unmarshal(nextFrame(t, frames, wire.KindNotice, noticeOf("latency")), &lat)
	if ms, ok := lat["capture_to_receive_ms"].(float64); !ok || ms < 0 || ms > 1000 {
		t.Fatalf("latency notice %v", lat)
	}
	if _, ok := lat["clock_offset_ms"].(float64); !ok {
		t.Fatalf("latency notice without clock_offset_ms %v", lat)
	}
	released := mustCall(t, e.c, "control.release", pc)
	owner := map[string]any{}
	_ = json.Unmarshal(nextFrame(t, frames, wire.KindNotice, func(b []byte) bool {
		var m map[string]any
		return json.Unmarshal(b, &m) == nil && m["type"] == "owner_changed" && m["owner"] == "none"
	}), &owner)
	if owner["gen"] != released["gen"] || owner["ai_paused"] != false {
		t.Fatalf("owner_changed %v after control.release answered %v", owner, released)
	}
}

func TestScreenStreamAttachRefusals(t *testing.T) {
	e := setup(t, setupOpts{trustDaemon: true})
	e.waitState(t, "ready")
	for _, tc := range []struct {
		params map[string]any
		code   string
	}{
		{map[string]any{"display": 0}, wire.CodeInvalidArgument},
		{map[string]any{"host_id": "nope"}, wire.CodeNotFound},
		{map[string]any{"host_id": "pc", "preset": "ultra"}, wire.CodeInvalidArgument},
		{map[string]any{"host_id": "pc"}, wire.CodeCapabilityUnsupported},
	} {
		_, err := attachScreen(t, tc.params)
		if got := apiCode(err); got != tc.code {
			t.Fatalf("stream.attach screen %v: %v, want %s", tc.params, err, tc.code)
		}
	}
}

func TestScreenStreamMaxFPSReachesTheAgentAndIsValidated(t *testing.T) {
	agent := newScreenAgent()
	e := setup(t, setupOpts{trustDaemon: true, agentStreams: agent.streams()})
	e.waitState(t, "ready")
	for _, fps := range []int{-1, 241} {
		if _, err := attachScreen(t, map[string]any{"host_id": "pc", "display": 0, "max_fps": fps}); apiCode(err) != wire.CodeInvalidArgument {
			t.Fatalf("max_fps %d: %v", fps, err)
		}
	}
	s, err := attachScreen(t, map[string]any{"host_id": "pc", "display": 0, "max_fps": 30})
	if err != nil {
		t.Fatal(err)
	}
	if s.Result["max_fps"] != float64(30) {
		t.Fatalf("stream.attach result %v", s.Result)
	}
	if h := agent.videoHeaders(); len(h) != 1 || h[0].MaxFPS != 30 {
		t.Fatalf("agent video headers %+v", h)
	}
}
