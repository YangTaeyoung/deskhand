package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// capNeedsUser is the capability state that waits for a person at the PC.
const capNeedsUser = "needs_user"

// maxStillData bounds the encoded image of a screen.capture reply: its
// base64 and 4 KiB of other fields fit in maxReplyLine.
const maxStillData = (maxReplyLine - 4096) / 4 * 3

// registerScreen adds screen.capture.
func (s *Server) registerScreen() {
	s.register("screen.capture", false, s.screenCaptureOp)
}

// screenCap is the screen capability for the result of
// Options.ScreenState: ready without an error, needs_user for an outage a
// person at the PC clears, unsupported off Windows and unavailable
// otherwise, with the outage's reason, access_lost for an error without
// one.
func screenCap(err error) capState {
	var u *capture.Unavailable
	switch {
	case err == nil:
		return capState{State: capReady}
	case errors.Is(err, capture.ErrUnsupported):
		return capState{State: capUnsupported, Reason: err.Error()}
	case errors.As(err, &u) && u.NeedsUser():
		return capState{State: capNeedsUser, Reason: u.Reason}
	case errors.As(err, &u):
		return capState{State: capUnavailable, Reason: u.Reason}
	}
	return capState{State: capUnavailable, Reason: capture.ReasonAccessLost}
}

// stillParams are the parameters of a screen.capture.
type stillParams struct {
	display, maxWidth, quality int
	format                     string
}

// readStillParams reads display (0), format (png), max_width (1568, 0 for
// the display's width) and quality (80, for jpeg), each a default when
// absent or null.
func readStillParams(req proto.Message) (stillParams, error) {
	p := stillParams{format: capture.FormatPNG}
	var err error
	if p.display, err = wholeField(req, "display", 0, 0, math.MaxInt32); err != nil {
		return p, err
	}
	if p.maxWidth, err = wholeField(req, "max_width", capture.DefaultStillMaxWidth, 0, math.MaxInt32); err != nil {
		return p, err
	}
	if p.quality, err = wholeField(req, "quality", capture.DefaultJPEGQuality, 1, 100); err != nil {
		return p, err
	}
	switch v := req["format"].(type) {
	case nil:
	case string:
		if v != capture.FormatPNG && v != capture.FormatJPEG {
			return p, coded(wire.CodeInvalidArgument, fmt.Sprintf("screen.capture format '%s' is unknown; use png or jpeg", cutChars(v, 32)), false)
		}
		p.format = v
	default:
		return p, coded(wire.CodeInvalidArgument, "screen.capture format is not a string; use png or jpeg", false)
	}
	return p, nil
}

// wholeField reads req[key] as a whole number within [lo, hi], or def when
// it is absent or null.
func wholeField(req proto.Message, key string, def, lo, hi int) (int, error) {
	v, ok := req[key]
	if !ok || v == nil {
		return def, nil
	}
	f, isNum := v.(float64)
	if n, isInt := v.(int); isInt {
		f, isNum = float64(n), true
	}
	if !isNum || f != math.Trunc(f) || f < float64(lo) || f > float64(hi) {
		return 0, coded(wire.CodeInvalidArgument, fmt.Sprintf("screen.capture %s must be a whole number from %d to %d", key, lo, hi), false)
	}
	return int(f), nil
}

// screenCaptureOp answers screen.capture with one still image of a
// display.
func (s *Server) screenCaptureOp(_ context.Context, req proto.Message) (proto.Message, error) {
	if req["window"] != nil {
		return nil, coded(wire.CodeCapabilityUnsupported, "screen.capture of one window is not supported by this agent; capture its display and crop the image to the window's x, y, w and h from win.list", false)
	}
	p, err := readStillParams(req)
	if err != nil {
		return nil, err
	}
	res, err := s.opt.Capture(p.display, p.format, p.maxWidth, p.quality)
	if err != nil {
		return nil, stillError(err)
	}
	if len(res.Data) > maxStillData {
		return nil, coded(wire.CodeInvalidArgument, fmt.Sprintf("the %dx%d %s image is %d bytes, more than one reply carries; ask for a smaller max_width or format jpeg",
			res.Width, res.Height, p.format, len(res.Data)), false)
	}
	return proto.Message{"capture_id": res.CaptureID, "data": base64.StdEncoding.EncodeToString(res.Data), "width": res.Width,
		"height": res.Height, "scale": res.Scale, "origin": map[string]any{"x": res.OriginX, "y": res.OriginY}, "dpi": res.DPI,
		"mono_us": res.MonoUS, "method": res.Method}, nil
}

// stillError gives a failed still capture its code: SESSION_LOCKED for a
// locked PC, DISPLAY_UNAVAILABLE for another outage (retryable for
// access_lost), CAPABILITY_UNSUPPORTED off Windows, NOT_FOUND for a
// missing display and INTERNAL otherwise.
func stillError(err error) error {
	var u *capture.Unavailable
	switch {
	case errors.As(err, &u) && u.Reason == capture.ReasonSessionLocked:
		return coded(wire.CodeSessionLocked, u.Error(), false)
	case errors.As(err, &u):
		return coded(wire.CodeDisplayUnavailable, u.Error(), u.Reason == capture.ReasonAccessLost)
	case errors.Is(err, capture.ErrUnsupported):
		return coded(wire.CodeCapabilityUnsupported, err.Error(), false)
	case errors.Is(err, capture.ErrNoDisplay):
		return coded(wire.CodeNotFound, err.Error(), false)
	}
	return coded(wire.CodeInternal, fmt.Sprintf("the screen could not be captured (%v); try again, and restart deskhand-agent if it persists", err), true)
}
