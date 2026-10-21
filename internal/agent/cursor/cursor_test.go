package cursor

import (
	"bufio"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// fakeSource is a cursor that tests move and reshape.
type fakeSource struct {
	mu     sync.Mutex
	now    sample
	shapes map[uintptr]*wire.CursorShape
	reads  int
}

func (f *fakeSource) sample() sample {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeSource) shape(h uintptr) (*wire.CursorShape, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	s, ok := f.shapes[h]
	if !ok {
		return nil, errors.New("fake: no shape")
	}
	return s, nil
}

func (f *fakeSource) set(s sample) {
	f.mu.Lock()
	f.now = s
	f.mu.Unlock()
}

func solid(id uint32, w, h uint16) *wire.CursorShape {
	return &wire.CursorShape{ID: id, W: w, H: h, HotX: 1, HotY: 2, BGRA: make([]byte, int(w)*int(h)*4)}
}

var twoDisplays = []display.Info{
	{Index: 0, X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
	{Index: 1, X: -1280, Y: 100, Width: 1280, Height: 720},
}

func posFrame(t *testing.T, payload []byte) wire.CursorPos {
	t.Helper()
	p, err := wire.UnmarshalCursorPos(payload)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTrackerSendsTheShapeBeforeThePosition(t *testing.T) {
	src := &fakeSource{now: sample{ok: true, showing: true, x: 100, y: 50, handle: 7}, shapes: map[uintptr]*wire.CursorShape{7: solid(70, 32, 32)}}
	tr := &tracker{src: src}
	frames := tr.step(time.Now(), twoDisplays[0])
	if len(frames) != 2 || frames[0].kind != wire.KindCursorShape || frames[1].kind != wire.KindCursorPos {
		t.Fatalf("frames %+v", frames)
	}
	shape, err := wire.UnmarshalCursorShape(frames[0].payload)
	if err != nil || shape.ID != 70 || shape.W != 32 || shape.HotY != 2 {
		t.Fatalf("shape %+v %v", shape, err)
	}
	if p := posFrame(t, frames[1].payload); p != (wire.CursorPos{X: 100, Y: 50, Visible: true, ShapeID: 70}) {
		t.Fatalf("pos %+v", p)
	}
}

func TestTrackerSendsThePositionOnlyWhenItChanges(t *testing.T) {
	src := &fakeSource{now: sample{ok: true, showing: true, x: 10, y: 10, handle: 7}, shapes: map[uintptr]*wire.CursorShape{7: solid(70, 32, 32)}}
	tr := &tracker{src: src}
	now := time.Now()
	tr.step(now, twoDisplays[0])
	if frames := tr.step(now.Add(posInterval), twoDisplays[0]); len(frames) != 0 {
		t.Fatalf("a still cursor sent %+v", frames)
	}
	src.set(sample{ok: true, showing: true, x: 11, y: 10, handle: 7})
	frames := tr.step(now.Add(2*posInterval), twoDisplays[0])
	if len(frames) != 1 || posFrame(t, frames[0].payload).X != 11 {
		t.Fatalf("frames %+v", frames)
	}
	src.set(sample{ok: true, showing: false, x: 11, y: 10})
	frames = tr.step(now.Add(3*posInterval), twoDisplays[0])
	if len(frames) != 1 || posFrame(t, frames[0].payload) != (wire.CursorPos{X: 11, Y: 10, ShapeID: 70}) {
		t.Fatalf("hidden cursor frames %+v", frames)
	}
}

func TestTrackerPositionIsRelativeToTheDisplayAndHiddenOffIt(t *testing.T) {
	src := &fakeSource{now: sample{ok: true, showing: true, x: -1000, y: 400, handle: 7}, shapes: map[uintptr]*wire.CursorShape{7: solid(70, 32, 32)}}
	tr := &tracker{src: src}
	frames := tr.step(time.Now(), twoDisplays[1])
	if p := posFrame(t, frames[len(frames)-1].payload); p != (wire.CursorPos{X: 280, Y: 300, Visible: true, ShapeID: 70}) {
		t.Fatalf("pos on display 1 %+v", p)
	}
	tr = &tracker{src: src}
	frames = tr.step(time.Now(), twoDisplays[0])
	if p := posFrame(t, frames[len(frames)-1].payload); p != (wire.CursorPos{X: -1000, Y: 400, ShapeID: 70}) {
		t.Fatalf("pos off display 0 %+v", p)
	}
}

func TestTrackerSendsAShapeOnlyWhenItsPixelsChange(t *testing.T) {
	src := &fakeSource{now: sample{ok: true, showing: true, x: 1, y: 1, handle: 7},
		shapes: map[uintptr]*wire.CursorShape{7: solid(70, 32, 32), 8: solid(70, 32, 32), 9: solid(90, 48, 48)}}
	tr := &tracker{src: src}
	now := time.Now()
	tr.step(now, twoDisplays[0])
	tr.step(now.Add(shapeRecheck/2), twoDisplays[0])
	if src.reads != 1 {
		t.Fatalf("the shape of an unchanged handle was read %d times before shapeRecheck", src.reads)
	}
	if frames := tr.step(now.Add(shapeRecheck), twoDisplays[0]); len(frames) != 0 || src.reads != 2 {
		t.Fatalf("re-reading the same pixels sent %+v (%d reads)", frames, src.reads)
	}
	src.set(sample{ok: true, showing: true, x: 1, y: 1, handle: 8})
	if frames := tr.step(now.Add(shapeRecheck+posInterval), twoDisplays[0]); len(frames) != 0 || src.reads != 3 {
		t.Fatalf("a new handle with the same pixels sent %+v (%d reads)", frames, src.reads)
	}
	src.set(sample{ok: true, showing: true, x: 1, y: 1, handle: 9})
	frames := tr.step(now.Add(shapeRecheck+2*posInterval), twoDisplays[0])
	if len(frames) != 2 || frames[0].kind != wire.KindCursorShape || posFrame(t, frames[1].payload).ShapeID != 90 {
		t.Fatalf("a new shape sent %+v", frames)
	}
}

func TestTrackerHidesACursorItCannotRead(t *testing.T) {
	src := &fakeSource{now: sample{ok: true, showing: true, x: 5, y: 6, handle: 7}, shapes: map[uintptr]*wire.CursorShape{7: solid(70, 32, 32)}}
	tr := &tracker{src: src}
	tr.step(time.Now(), twoDisplays[0])
	src.set(sample{})
	frames := tr.step(time.Now(), twoDisplays[0])
	if len(frames) != 1 || posFrame(t, frames[0].payload) != (wire.CursorPos{X: 5, Y: 6, ShapeID: 70}) {
		t.Fatalf("frames %+v", frames)
	}
}

// serveOnPipe serves a cursor stream for header h on a pipe and returns
// the daemon's end.
func serveOnPipe(t *testing.T, h wire.StreamHeader, src source, displays []display.Info) (net.Conn, *bufio.Reader, chan struct{}) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	done := make(chan struct{})
	opt := Options{Logger: log.New(io.Discard, "", 0), Displays: func() ([]display.Info, error) { return displays, nil }}
	go func() {
		defer close(done)
		serve(t.Context(), b, h, opt, src)
	}()
	_ = a.SetDeadline(time.Now().Add(10 * time.Second))
	return a, bufio.NewReader(a), done
}

func TestServeAcceptsThenStreamsUntilThePeerCloses(t *testing.T) {
	src := &fakeSource{now: sample{ok: true, showing: true, x: 3, y: 4, handle: 7}, shapes: map[uintptr]*wire.CursorShape{7: solid(70, 8, 8)}}
	a, r, done := serveOnPipe(t, wire.StreamHeader{S: "cursor", Display: 1}, src, twoDisplays)
	var first map[string]any
	if err := wire.ReadHeader(r, &first); err != nil || first["ok"] != true {
		t.Fatalf("first line %v %v", first, err)
	}
	kind, _, err := wire.ReadFrame(r, wire.MaxFrame)
	if err != nil || kind != wire.KindCursorShape {
		t.Fatalf("first frame %#x %v", kind, err)
	}
	kind, payload, err := wire.ReadFrame(r, wire.MaxFrame)
	if err != nil || kind != wire.KindCursorPos || posFrame(t, payload).X != 1283 {
		t.Fatalf("second frame %#x %v", kind, err)
	}
	src.set(sample{ok: true, showing: true, x: 30, y: 4, handle: 7})
	kind, payload, err = wire.ReadFrame(r, wire.MaxFrame)
	if err != nil || kind != wire.KindCursorPos || posFrame(t, payload).X != 1310 {
		t.Fatalf("frame after a move %#x %v", kind, err)
	}
	a.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end after its peer closed")
	}
}

func TestServeRefusesAMissingDisplayAndAMissingSource(t *testing.T) {
	src := &fakeSource{shapes: map[uintptr]*wire.CursorShape{}}
	for _, c := range []struct {
		src  source
		code string
	}{{src, wire.CodeNotFound}, {nil, wire.CodeCapabilityUnsupported}} {
		_, r, done := serveOnPipe(t, wire.StreamHeader{S: "cursor", Display: 2}, c.src, twoDisplays)
		var first map[string]any
		if err := wire.ReadHeader(r, &first); err != nil || first["ok"] != false || first["code"] != c.code || first["error"] == "" {
			t.Fatalf("first line %v %v", first, err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("a refused stream stays open")
		}
	}
}
