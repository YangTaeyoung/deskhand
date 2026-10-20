package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// stillCall records the arguments of a fake capture.
type stillCall struct {
	display           int
	format            string
	maxWidth, quality int
}

// screenClient connects to a server whose still capture answers with res
// and err and records its calls, and whose screen state is state.
func screenClient(t *testing.T, res capture.StillResult, err error, state error) (*rpcClient, proto.Message, *[]stillCall) {
	t.Helper()
	var calls []stillCall
	_, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir(),
		Capture: func(display int, format string, maxWidth, quality int) (capture.StillResult, error) {
			calls = append(calls, stillCall{display, format, maxWidth, quality})
			return res, err
		},
		ScreenState: func() error { return state }})
	hello := expect(t, r, "hello")
	return &rpcClient{t: t, r: r, w: w}, hello, &calls
}

var testStill = capture.StillResult{CaptureID: "c-0123456789ab", Data: []byte("\x89PNG fake"), Width: 1568, Height: 882,
	Scale: 1568.0 / 3840, OriginX: 3840, OriginY: 0, DPI: 144, MonoUS: 123456, Method: capture.MethodDD}

func TestScreenCaptureReturnsTheProtocolFields(t *testing.T) {
	c, _, calls := screenClient(t, testStill, nil, nil)
	got := c.ok("screen.capture", nil)
	want := map[string]any{"capture_id": "c-0123456789ab", "data": base64.StdEncoding.EncodeToString(testStill.Data),
		"width": float64(1568), "height": float64(882), "scale": 1568.0 / 3840, "origin": map[string]any{"x": float64(3840), "y": float64(0)},
		"dpi": float64(144), "mono_us": float64(123456), "method": "dd"}
	if fields := onlyKeys(got, want); !reflect.DeepEqual(fields, want) {
		t.Fatalf("screen.capture %v", got)
	}
	if want := []stillCall{{0, capture.FormatPNG, capture.DefaultStillMaxWidth, capture.DefaultJPEGQuality}}; !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls %+v, want %+v", *calls, want)
	}
}

func TestScreenCapturePassesItsParameters(t *testing.T) {
	c, _, calls := screenClient(t, testStill, nil, nil)
	c.ok("screen.capture", proto.Message{"display": 1, "format": "jpeg", "max_width": 0, "quality": 55})
	c.ok("screen.capture", proto.Message{"display": 0, "format": "png", "max_width": 800, "quality": nil, "window": nil})
	want := []stillCall{{1, capture.FormatJPEG, 0, 55}, {0, capture.FormatPNG, 800, capture.DefaultJPEGQuality}}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls %+v, want %+v", *calls, want)
	}
}

func TestScreenCaptureRejectsBadParameters(t *testing.T) {
	c, _, calls := screenClient(t, testStill, nil, nil)
	for _, p := range []proto.Message{
		{"display": -1}, {"display": 1.5}, {"display": "0"},
		{"format": "gif"}, {"format": 1},
		{"max_width": -1}, {"max_width": 10.5}, {"max_width": "1568"},
		{"quality": 0}, {"quality": 101}, {"quality": "high"},
	} {
		got := c.call("screen.capture", p)
		if got["ok"] != false || got.String("code") != wire.CodeInvalidArgument || got.String("error") == "" || got["retryable"] != false {
			t.Errorf("screen.capture %v: %v", p, got)
		}
	}
	got := c.call("screen.capture", proto.Message{"window": "w12"})
	if got.String("code") != wire.CodeCapabilityUnsupported || !strings.Contains(got.String("error"), "win.list") {
		t.Errorf("screen.capture with a window: %v", got)
	}
	if len(*calls) != 0 {
		t.Fatalf("capture ran for bad parameters: %+v", *calls)
	}
}

func TestScreenCaptureErrorsCarryTheirCodes(t *testing.T) {
	for _, c := range []struct {
		err       error
		code      string
		retryable bool
		text      string
	}{
		{&capture.Unavailable{Reason: capture.ReasonSessionLocked}, wire.CodeSessionLocked, false, "session_locked"},
		{&capture.Unavailable{Reason: capture.ReasonRDPOutputSuppressed, Detail: "E_ACCESSDENIED (0x80070005)"}, wire.CodeDisplayUnavailable, false, "rdp_output_suppressed"},
		{&capture.Unavailable{Reason: capture.ReasonSecureDesktop}, wire.CodeDisplayUnavailable, false, "secure_desktop"},
		{&capture.Unavailable{Reason: capture.ReasonSession0}, wire.CodeDisplayUnavailable, false, "session0"},
		{&capture.Unavailable{Reason: capture.ReasonAccessLost}, wire.CodeDisplayUnavailable, true, "access_lost"},
		{capture.ErrUnsupported, wire.CodeCapabilityUnsupported, false, "Windows"},
		{fmt.Errorf("display 3 does not exist: %w", capture.ErrNoDisplay), wire.CodeNotFound, false, "display 3"},
		{errors.New("AcquireNextFrame: DXGI_ERROR_DEVICE_REMOVED (0x887A0005)"), wire.CodeInternal, true, "DEVICE_REMOVED"},
	} {
		cl, _, _ := screenClient(t, capture.StillResult{}, c.err, nil)
		got := cl.call("screen.capture", nil)
		if got["ok"] != false || got.String("code") != c.code || got["retryable"] != c.retryable || !strings.Contains(got.String("error"), c.text) {
			t.Errorf("%v: %v", c.err, got)
		}
	}
}

func TestScreenCaptureTooLargeForAReplyAsksForLess(t *testing.T) {
	big := testStill
	big.Data = make([]byte, maxStillData+1)
	c, _, _ := screenClient(t, big, nil, nil)
	got := c.call("screen.capture", proto.Message{"max_width": 0})
	if got.String("code") != wire.CodeInvalidArgument || !strings.Contains(got.String("error"), "max_width") {
		t.Fatalf("screen.capture %v", got)
	}

	fits := testStill
	fits.Data = make([]byte, maxStillData)
	s := New(Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir(), ScreenState: func() error { return nil },
		Capture: func(int, string, int, int) (capture.StillResult, error) { return fits, nil }})
	var res proto.Message
	var line []byte
	s.dispatch(context.Background(), proto.Message{"t": "req", "id": 1, "op": "screen.capture"}, func(m proto.Message, l []byte) { res, line = m, l })
	if res["ok"] != true || len(res.String("data")) != base64.StdEncoding.EncodedLen(maxStillData) || len(line) >= maxReplyLine {
		t.Fatalf("reply ok %v with %d characters of data in a line of %d bytes", res["ok"], len(res.String("data")), len(line))
	}
}

func TestScreenCapFollowsTheScreenState(t *testing.T) {
	for _, c := range []struct {
		state error
		want  capState
	}{
		{nil, capState{State: capReady}},
		{&capture.Unavailable{Reason: capture.ReasonSecureDesktop}, capState{State: capNeedsUser, Reason: "secure_desktop"}},
		{&capture.Unavailable{Reason: capture.ReasonSessionLocked}, capState{State: capNeedsUser, Reason: "session_locked"}},
		{&capture.Unavailable{Reason: capture.ReasonRDPOutputSuppressed}, capState{State: capNeedsUser, Reason: "rdp_output_suppressed"}},
		{&capture.Unavailable{Reason: capture.ReasonSession0}, capState{State: capUnavailable, Reason: "session0"}},
		{&capture.Unavailable{Reason: capture.ReasonAccessLost}, capState{State: capUnavailable, Reason: "access_lost"}},
		{&capture.Unavailable{Reason: capture.ReasonNoOutput}, capState{State: capUnavailable, Reason: "no_output"}},
		{capture.ErrUnsupported, capState{State: capUnsupported, Reason: capture.ErrUnsupported.Error()}},
		{errors.New("BitBlt failed"), capState{State: capUnavailable, Reason: "access_lost"}},
	} {
		if got := screenCap(c.state); got != c.want {
			t.Errorf("%v: %+v, want %+v", c.state, got, c.want)
		}
	}

	_, hello, _ := screenClient(t, testStill, nil, &capture.Unavailable{Reason: capture.ReasonRDPOutputSuppressed})
	caps, _ := hello["caps"].(map[string]any)
	if st, _ := caps["screen"].(map[string]any); st["state"] != capNeedsUser || st["reason"] != "rdp_output_suppressed" {
		t.Fatalf("caps.screen %v", caps["screen"])
	}
}
