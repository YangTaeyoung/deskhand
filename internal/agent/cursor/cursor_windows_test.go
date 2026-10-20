package cursor

import (
	"bufio"
	"io"
	"log"
	"net"
	"os"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func needsUserSession(t *testing.T) {
	t.Helper()
	if os.Getenv("DESKHAND_WINTEST") == "" {
		t.Skip("needs the signed-in user's session; run through scripts/wintest.sh")
	}
	display.EnsureDPIAware()
}

var procLoadCursorW = user32.NewProc("LoadCursorW")

// systemCursor returns the shared handle of the system cursor id.
func systemCursor(t *testing.T, id uintptr) uintptr {
	t.Helper()
	h, _, err := procLoadCursorW.Call(0, id)
	if h == 0 {
		t.Fatalf("LoadCursor(%d): %v", id, err)
	}
	return h
}

func TestCursorShapeBGRA(t *testing.T) {
	needsUserSession(t)
	src := windowsSource{}
	s := src.sample()
	t.Logf("the desktop cursor: %+v", s)
	cursors := map[string]uintptr{}
	if s.ok && s.showing && s.handle != 0 {
		cursors["current"] = s.handle
	}
	for name, id := range map[string]uintptr{"arrow": 32512, "ibeam": 32513, "wait": 32514, "hand": 32649, "sizeall": 32646} {
		cursors[name] = systemCursor(t, id)
	}
	ids := map[uint32]string{}
	for name, h := range cursors {
		shape, err := src.shape(h)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if shape.W == 0 || shape.H == 0 || len(shape.BGRA) != int(shape.W)*int(shape.H)*4 || shape.ID == 0 {
			t.Fatalf("%s: shape %dx%d, %d bytes, id %d", name, shape.W, shape.H, len(shape.BGRA), shape.ID)
		}
		if shape.HotX >= shape.W || shape.HotY >= shape.H {
			t.Fatalf("%s: hot spot (%d,%d) outside %dx%d", name, shape.HotX, shape.HotY, shape.W, shape.H)
		}
		opaque, partial := 0, 0
		for i := 3; i < len(shape.BGRA); i += 4 {
			switch a := shape.BGRA[i]; {
			case a == 255:
				opaque++
			case a != 0:
				partial++
			}
		}
		if opaque == 0 {
			t.Fatalf("%s: the cursor shape has no opaque pixel", name)
		}
		again, err := src.shape(h)
		if err != nil || again.ID != shape.ID {
			t.Fatalf("%s: a second read has id %v (%v), want %#x", name, again, err, shape.ID)
		}
		if other, ok := ids[shape.ID]; ok && name != "current" && other != "current" {
			t.Fatalf("%s and %s share the shape id %#x", name, other, shape.ID)
		}
		ids[shape.ID] = name
		t.Logf("%s: %dx%d hot (%d,%d), %d opaque and %d partly transparent pixels, id %#x",
			name, shape.W, shape.H, shape.HotX, shape.HotY, opaque, partial, shape.ID)
	}
}

func TestCursorStreamReportsTheDesktopCursor(t *testing.T) {
	needsUserSession(t)
	displays, err := display.List()
	if err != nil {
		t.Fatal(err)
	}
	s := windowsSource{}.sample()
	d := displays[0]
	for _, x := range displays {
		if s.x >= x.X && s.y >= x.Y && s.x < x.X+x.Width && s.y < x.Y+x.Height {
			d = x
		}
	}
	a, b := net.Pipe()
	defer a.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		Serve(t.Context(), b, wire.StreamHeader{S: "cursor", Display: d.Index}, Options{Logger: log.New(io.Discard, "", 0)})
	}()
	_ = a.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(a)
	var first map[string]any
	if err := wire.ReadHeader(r, &first); err != nil || first["ok"] != true {
		t.Fatalf("first line %v %v", first, err)
	}
	var shape wire.CursorShape
	kind, payload, err := wire.ReadFrame(r, wire.MaxFrame)
	if err == nil && kind == wire.KindCursorShape {
		if shape, err = wire.UnmarshalCursorShape(payload); err != nil {
			t.Fatal(err)
		}
		kind, payload, err = wire.ReadFrame(r, wire.MaxFrame)
	}
	if err != nil || kind != wire.KindCursorPos {
		t.Fatalf("frame %#x %v", kind, err)
	}
	pos, err := wire.UnmarshalCursorPos(payload)
	if err != nil {
		t.Fatal(err)
	}
	want := wire.CursorPos{X: int32(s.x - d.X), Y: int32(s.y - d.Y), Visible: s.showing, ShapeID: shape.ID}
	if s.showing != (shape.ID != 0) || pos != want {
		t.Fatalf("pos %+v after shape %#x, want %+v for the cursor %+v on display %d at (%d,%d)", pos, shape.ID, want, s, d.Index, d.X, d.Y)
	}
	t.Logf("display %d: shape %dx%d %#x, pos %+v", d.Index, shape.W, shape.H, shape.ID, pos)
	a.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end after its peer closed")
	}
}
