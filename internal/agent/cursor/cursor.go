// Package cursor serves the cursor stream of docs/protocol.md §5: the
// mouse pointer's shape and position on one display, read from the
// desktop whether or not a video stream runs.
package cursor

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// posInterval is the time between two readings of the cursor, which
	// bounds the position frames to 120 a second.
	posInterval = time.Second / 120
	// shapeRecheck is how often the shape of an unchanged cursor handle is
	// read again.
	shapeRecheck = 250 * time.Millisecond
	// displayRecheck is how often the display's place is read again.
	displayRecheck = 2 * time.Second
	// maxPeerFrame bounds a frame the daemon sends on the stream.
	maxPeerFrame = 64 << 10
	// writeTimeout bounds the write of one line or frame.
	writeTimeout = 10 * time.Second
)

// Options configure a cursor stream.
type Options struct {
	Logger *log.Logger
	// Displays lists the displays; nil means display.List.
	Displays func() ([]display.Info, error)
}

// Stream is the agent's end of a cursor stream after its header line.
// Close ends both directions.
type Stream interface {
	io.Reader
	io.Writer
	SetWriteDeadline(t time.Time) error
	Close() error
}

// sample is one reading of the cursor: whether it could be read, whether
// it is shown, its hot spot on the virtual desktop and its handle.
type sample struct {
	ok, showing bool
	x, y        int
	handle      uintptr
}

// source reads the cursor of the desktop.
type source interface {
	sample() sample
	// shape returns the image of cursor handle h as non-premultiplied BGRA
	// with an ID that follows its pixels.
	shape(h uintptr) (*wire.CursorShape, error)
}

// frame is a frame of the stream without its length.
type frame struct {
	kind    byte
	payload []byte
}

// tracker turns readings of the cursor into the frames that report what
// changed.
type tracker struct {
	src source

	handle  uintptr
	shapeAt time.Time
	shapeID uint32

	last wire.CursorPos
	sent bool
}

// step reads the cursor at now and returns the frames for display d: a
// shape frame when the pixels of the cursor changed, then a pos frame
// when the position, the visibility or the shape changed. The shape is
// read when the handle changes and every shapeRecheck. The position is
// relative to d's origin and visible only while the cursor is shown on d;
// a cursor that cannot be read keeps its last position, hidden.
func (t *tracker) step(now time.Time, d display.Info) []frame {
	var frames []frame
	s := t.src.sample()
	if s.ok && s.showing && s.handle != 0 && (s.handle != t.handle || now.Sub(t.shapeAt) >= shapeRecheck) {
		t.handle, t.shapeAt = s.handle, now
		if shape, err := t.src.shape(s.handle); err == nil && shape.ID != t.shapeID {
			if b, err := shape.Marshal(); err == nil {
				t.shapeID = shape.ID
				frames = append(frames, frame{wire.KindCursorShape, b})
			}
		}
	}
	pos := t.last
	pos.Visible = false
	if s.ok {
		pos.X, pos.Y = int32(s.x-d.X), int32(s.y-d.Y)
		pos.Visible = s.showing && s.x >= d.X && s.y >= d.Y && s.x < d.X+d.Width && s.y < d.Y+d.Height
	}
	pos.ShapeID = t.shapeID
	if !t.sent || pos != t.last {
		t.last, t.sent = pos, true
		frames = append(frames, frame{wire.KindCursorPos, pos.Marshal()})
	}
	return frames
}

// accepted is the line written to an accepted stream.
type accepted struct {
	OK bool `json:"ok"`
}

// refusal is the line written to a refused stream.
type refusal struct {
	OK    bool   `json:"ok"`
	Code  string `json:"code"`
	Error string `json:"error"`
}

// Serve serves the cursor stream st with header h (docs/protocol.md §2,
// §5): {"ok":true}, then the shape and the position of the cursor on
// display h.Display, each when it changes, read every posInterval. It
// returns once ctx ends, the daemon ends its direction or a write fails,
// with st closed. A display that does not exist is refused with
// NOT_FOUND, and a system without a cursor source with
// CAPABILITY_UNSUPPORTED.
func Serve(ctx context.Context, st Stream, h wire.StreamHeader, opt Options) {
	serve(ctx, st, h, opt, platformSource())
}

func serve(ctx context.Context, st Stream, h wire.StreamHeader, opt Options, src source) {
	if opt.Logger == nil {
		opt.Logger = log.Default()
	}
	if opt.Displays == nil {
		opt.Displays = display.List
	}
	defer st.Close()
	write := func(f func() error) error {
		_ = st.SetWriteDeadline(time.Now().Add(writeTimeout))
		return f()
	}
	refuse := func(code, msg string) {
		_ = write(func() error { return wire.WriteHeader(st, refusal{Code: code, Error: msg}) })
	}
	if src == nil {
		refuse(wire.CodeCapabilityUnsupported, "the cursor stream needs a Windows agent; open it to a Windows PC")
		return
	}
	d, err := findDisplay(opt.Displays, h.Display)
	if err != nil {
		refuse(err.code, err.msg)
		return
	}
	if err := write(func() error { return wire.WriteHeader(st, accepted{OK: true}) }); err != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopClosing := context.AfterFunc(ctx, func() { _ = st.Close() })
	defer stopClosing()
	go func() {
		defer cancel()
		for {
			if _, _, err := wire.ReadFrame(st, maxPeerFrame); err != nil {
				return
			}
		}
	}()
	tr := &tracker{src: src}
	tick := time.NewTicker(posInterval)
	defer tick.Stop()
	displayAt := time.Now()
	for {
		now := time.Now()
		if now.Sub(displayAt) >= displayRecheck {
			displayAt = now
			if nd, err := findDisplay(opt.Displays, h.Display); err == nil {
				d = nd
			}
		}
		for _, f := range tr.step(now, d) {
			if err := write(func() error { return wire.WriteFrame(st, f.kind, f.payload) }); err != nil {
				if ctx.Err() == nil {
					opt.Logger.Printf("cursor: the stream ended: %v", err)
				}
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// streamError is a refusal of the stream.
type streamError struct{ code, msg string }

// findDisplay returns display index of list.
func findDisplay(list func() ([]display.Info, error), index int) (display.Info, *streamError) {
	ds, err := list()
	if err != nil {
		return display.Info{}, &streamError{wire.CodeDisplayUnavailable, fmt.Sprintf("the displays cannot be read (%v); check that the PC's user is signed in, then open the stream again", err)}
	}
	for _, d := range ds {
		if d.Index == index {
			return d, nil
		}
	}
	return display.Info{}, &streamError{wire.CodeNotFound, fmt.Sprintf("display %d does not exist; call display.list and open the stream for one of its displays", index)}
}
