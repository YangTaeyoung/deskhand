package screen

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// videoPeer is the daemon's end of a video stream served by ServeVideo
// over a net.Pipe, with fake sources and encoders on the real clock.
type videoPeer struct {
	conn net.Conn
	r    *bufio.Reader
	op   *opener
	encs *encoders
	done chan struct{}
}

func startVideo(t *testing.T, h wire.StreamHeader) *videoPeer {
	t.Helper()
	return startVideoWith(t, h, func(*videoPeer) {})
}

// startVideoWith is startVideo with set applied to the peer before the
// stream starts.
func startVideoWith(t *testing.T, h wire.StreamHeader, set func(*videoPeer)) *videoPeer {
	t.Helper()
	clk := realClock{}
	daemon, agent := net.Pipe()
	vp := &videoPeer{conn: daemon, r: bufio.NewReader(daemon), op: &opener{clk: clk, p: periodic(time.Second / 30), w: 1920, h: 1080},
		encs: &encoders{clk: clk}, done: make(chan struct{})}
	set(vp)
	go func() {
		defer close(vp.done)
		ServeVideo(context.Background(), agent, h, Options{Open: vp.op.open, NewEncoder: vp.encs.new})
	}()
	t.Cleanup(func() {
		daemon.Close()
		<-vp.done
	})
	return vp
}

// next reads the next frame that is not stats within 5 s.
func (vp *videoPeer) next(t *testing.T) recorded {
	t.Helper()
	_ = vp.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		kind, payload, err := wire.ReadFrame(vp.r, wire.MaxFrame)
		if err != nil {
			t.Fatalf("reading a frame: %v", err)
		}
		if kind != wire.KindVideoStats {
			return recorded{kind, payload}
		}
	}
}

// until reads frames until one of kind and returns it.
func (vp *videoPeer) until(t *testing.T, kind byte, ok func(recorded) bool) recorded {
	t.Helper()
	for {
		f := vp.next(t)
		if f.kind == kind && (ok == nil || ok(f)) {
			return f
		}
	}
}

func (vp *videoPeer) control(t *testing.T, c wire.VideoControl) {
	t.Helper()
	b, _ := json.Marshal(c)
	go func() { _ = wire.WriteFrame(vp.conn, wire.KindClientControl, b) }()
}

func keyAU(f recorded) bool {
	au, err := wire.UnmarshalAU(f.payload)
	return err == nil && au.Key && au.HasConfig
}

func TestVideoStreamStartsWithInfo(t *testing.T) {
	vp := startVideo(t, wire.StreamHeader{S: "video", MaxWidth: 1280, Display: 0})
	first := vp.next(t)
	if first.kind != wire.KindVideoInfo {
		t.Fatalf("first frame kind %#x, want info", first.kind)
	}
	info := decode[wire.VideoInfo](t, first)
	if info.Codec != "h264" || info.Width != 1280 || info.Height != 720 || info.SrcWidth != 1920 || info.FPS != 60 ||
		info.Bitrate != 6_000_000 || info.Encoder != "fake" || !info.Hardware || info.Capture != "dd" {
		t.Fatalf("info %+v", info)
	}
	au := vp.next(t)
	if au.kind != wire.KindAU || !keyAU(au) {
		t.Fatalf("second frame kind %#x, key %v", au.kind, keyAU(au))
	}
	a, _ := wire.UnmarshalAU(au.payload)
	if a.Seq != 1 || a.Width != 1280 || a.Height != 720 {
		t.Fatalf("first access unit %+v", a)
	}
	b, _ := wire.UnmarshalAU(vp.until(t, wire.KindAU, nil).payload)
	if b.Seq != 2 || b.Key {
		t.Fatalf("second access unit %+v", b)
	}
}

func TestVideoStreamRefusesAnUnknownCodec(t *testing.T) {
	vp := startVideo(t, wire.StreamHeader{S: "video", Codec: "hevc"})
	_ = vp.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var ref struct {
		OK    bool   `json:"ok"`
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := wire.ReadHeader(vp.r, &ref); err != nil {
		t.Fatal(err)
	}
	if ref.OK || ref.Code != wire.CodeCapabilityUnsupported || ref.Error == "" {
		t.Fatalf("refusal %+v", ref)
	}
	if _, err := vp.r.ReadByte(); err == nil {
		t.Fatal("bytes after the refusal")
	}
	if vp.op.calls != 0 {
		t.Fatalf("%d opens for a refused stream", vp.op.calls)
	}
}

func TestVideoStreamControls(t *testing.T) {
	vp := startVideo(t, wire.StreamHeader{S: "video"})
	vp.until(t, wire.KindAU, keyAU)
	vp.control(t, wire.VideoControl{Op: wire.ControlPause})
	if r := decode[wire.VideoPaused](t, vp.until(t, wire.KindVideoPaused, nil)).Reason; r != wire.PausedNoViewer {
		t.Fatalf("paused reason %q", r)
	}
	vp.control(t, wire.VideoControl{Op: wire.ControlResume})
	if f := vp.next(t); f.kind != wire.KindVideoInfo {
		t.Fatalf("frame kind %#x after resume, want info", f.kind)
	}
	if f := vp.next(t); f.kind != wire.KindAU || !keyAU(f) {
		t.Fatalf("frame kind %#x after the info, key %v", f.kind, keyAU(f))
	}
	vp.until(t, wire.KindAU, func(f recorded) bool { return !keyAU(f) })
	vp.control(t, wire.VideoControl{Op: wire.ControlKeyframe})
	vp.until(t, wire.KindAU, keyAU)
	vp.control(t, wire.VideoControl{Op: wire.ControlConfig, MaxWidth: 640})
	if i := decode[wire.VideoInfo](t, vp.until(t, wire.KindVideoInfo, nil)); i.Width != 640 || i.Height != 360 {
		t.Fatalf("info after config %+v", i)
	}
	if f := vp.next(t); f.kind != wire.KindAU || !keyAU(f) {
		t.Fatalf("frame kind %#x after the new info, key %v", f.kind, keyAU(f))
	}
}

func TestVideoStreamEndsWhenThePeerCloses(t *testing.T) {
	vp := startVideo(t, wire.StreamHeader{S: "video"})
	vp.until(t, wire.KindAU, keyAU)
	vp.conn.Close()
	select {
	case <-vp.done:
	case <-time.After(5 * time.Second):
		t.Fatal("ServeVideo did not return after the peer closed the stream")
	}
	for _, s := range vp.op.sources() {
		if !s.closed {
			t.Fatal("a source is still open")
		}
	}
	for _, e := range vp.encs.list() {
		if !e.closed {
			t.Fatal("an encoder is still open")
		}
	}
}

// failingStream is a Stream whose writes fail after ok writes and whose
// reads wait until Close.
type failingStream struct {
	mu     sync.Mutex
	ok     int
	r      *io.PipeReader
	w      *io.PipeWriter
	closed bool
}

func newFailingStream(ok int) *failingStream {
	r, w := io.Pipe()
	return &failingStream{ok: ok, r: r, w: w}
}

func (s *failingStream) Read(p []byte) (int, error) { return s.r.Read(p) }

func (s *failingStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ok == 0 {
		return 0, errors.New("the peer reset the stream")
	}
	s.ok--
	return len(p), nil
}

func (s *failingStream) SetWriteDeadline(time.Time) error { return nil }

func (s *failingStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.w.Close()
	}
	return nil
}

// settles waits up to 2 s for the number of goroutines to fall to n.
func settles(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > n {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("%d goroutines, %d before the stream:\n%s", runtime.NumGoroutine(), n, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServeVideoLeavesNoGoroutinesAfterAWriteFailure(t *testing.T) {
	before := runtime.NumGoroutine()
	clk := realClock{}
	op := &opener{clk: clk, p: periodic(time.Second / 30), w: 1920, h: 1080}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ServeVideo(context.Background(), newFailingStream(3), wire.StreamHeader{S: "video"}, Options{Open: op.open, NewEncoder: (&encoders{clk: clk}).new})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ServeVideo did not return after a write failed")
	}
	settles(t, before)
}

func TestServeVideoLeavesNoGoroutinesAfterAPauseWithAHeldAccessUnit(t *testing.T) {
	before := runtime.NumGoroutine()
	vp := startVideoWith(t, wire.StreamHeader{S: "video"}, func(vp *videoPeer) { vp.encs.hold = make(chan struct{}) })
	vp.until(t, wire.KindVideoInfo, nil)
	vp.control(t, wire.VideoControl{Op: wire.ControlPause})
	vp.until(t, wire.KindVideoPaused, nil)
	vp.conn.Close()
	select {
	case <-vp.done:
	case <-time.After(5 * time.Second):
		t.Fatal("ServeVideo did not return after the peer closed the stream")
	}
	settles(t, before)
}
