// Package capture takes images of a display of the agent's desktop: frames
// for the video stream (DXGI Desktop Duplication, or GDI when duplication is
// refused) and still images for screen.capture (docs/protocol.md §3.1).
//
// A Source holds COM objects: open it on a goroutine locked to its thread
// (runtime.LockOSThread) and call its methods and Close there.
package capture

import (
	"context"
	"errors"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Frame is one image of a Source with the pointer as it was then.
type Frame struct {
	// Texture is the source's own BGRA ID3D11Texture2D holding the image
	// on the Desktop Duplication path, 0 on the GDI path. The source keeps
	// it; it stays valid until Close and holds the newest image after each
	// Next.
	Texture uintptr
	// CPU is the image as Width×Height BGRA pixels, rows of Width*4 bytes
	// with the alpha byte unset, on the GDI path, nil on the Desktop
	// Duplication path. It stays valid until the next call of Next.
	CPU           []byte
	Width, Height int
	// MonoUS is the monoclock reading of the moment the image was taken.
	MonoUS uint64
	// Dirty reports whether the image changed since the frame before; a
	// frame that is not dirty carries a pointer update only.
	Dirty   bool
	Pointer PointerInfo
}

// PointerInfo is the mouse pointer of a frame, in pixels of the display.
type PointerInfo struct {
	X, Y    int
	Visible bool
	// ShapeChanged reports that Shape is a new pointer shape; the Desktop
	// Duplication path reports shapes, the GDI path positions only.
	ShapeChanged bool
	Shape        *wire.CursorShape
}

// Source takes the frames of one display.
type Source interface {
	// Next waits up to timeout for a new frame. It returns ErrNoNewFrame
	// when none came, ErrReset after it recreated the capture (check Size
	// again), an *Unavailable while the display cannot be captured, and
	// another error when the source is broken and has to be opened again.
	Next(ctx context.Context, timeout time.Duration) (Frame, error)
	// Device is the D3D11 device of the display's adapter.
	Device() *d3d.Device
	// Size is the size of the frames in pixels.
	Size() (w, h int)
	Close()
}

// Errors of Next, Open and CaptureStill.
var (
	// ErrNoNewFrame: no new image or pointer update came within the
	// timeout.
	ErrNoNewFrame = errors.New("no new frame within the timeout")
	// ErrReset: the capture was recreated, after a mode change for
	// example; the size of the frames may have changed.
	ErrReset = errors.New("the capture was recreated; check the frame size again")
	// ErrUnsupported is the error of every capture on a system other than
	// Windows.
	ErrUnsupported = errors.New("screen capture needs a Windows agent; ask a Windows PC")
	// ErrNoDisplay is wrapped by the error for a display index the desktop
	// does not have.
	ErrNoDisplay = errors.New("no such display")
)

// Reasons of an Unavailable, the fixed reason strings of the screen
// capability and of DISPLAY_UNAVAILABLE.
const (
	ReasonSession0            = "session0"
	ReasonRDPOutputSuppressed = "rdp_output_suppressed"
	ReasonSecureDesktop       = "secure_desktop"
	ReasonSessionLocked       = "session_locked"
	ReasonSessionDisconnected = "session_disconnected"
	ReasonAccessLost          = "access_lost"
	ReasonNoOutput            = "no_output"
)

// Unavailable is the error of a capture that the session or the display
// does not allow now. Reason is one of the Reason constants and Detail
// the failure behind it.
type Unavailable struct {
	Reason string
	Detail string
}

// NeedsUser reports whether a person at the PC has to act before the
// display can be captured again.
func (u *Unavailable) NeedsUser() bool {
	switch u.Reason {
	case ReasonSecureDesktop, ReasonSessionLocked, ReasonRDPOutputSuppressed:
		return true
	}
	return false
}

// Error is one sentence with the reason, the detail and the next step.
func (u *Unavailable) Error() string {
	what, next := reasonText(u.Reason)
	if u.Detail != "" {
		return what + " (" + u.Reason + ": " + u.Detail + "); " + next
	}
	return what + " (" + u.Reason + "); " + next
}

// reasonText returns what an unavailable reason means and what to do next.
func reasonText(reason string) (what, next string) {
	switch reason {
	case ReasonSession0:
		return "the agent runs in Session 0, which has no screen", "start deskhand-agent through its scheduled task in the signed-in user's session"
	case ReasonRDPOutputSuppressed:
		return "the PC's screen is not drawn while its Remote Desktop window is hidden", "show the Remote Desktop window or move the session to the PC's console, then try again"
	case ReasonSecureDesktop:
		return "a secure desktop such as a UAC prompt is in front on the PC", "answer it at the PC, then try again"
	case ReasonSessionLocked:
		return "the PC is locked", "unlock it at the PC, then try again"
	case ReasonSessionDisconnected:
		return "the user's session on the PC is disconnected", "sign in at the PC or reconnect to it, then try again"
	case ReasonAccessLost:
		return "the PC's screen is switching and cannot be captured right now", "try again in a few seconds"
	case ReasonNoOutput:
		return "the PC has no display to capture", "turn a display on or connect one, then try again"
	}
	return "the PC's screen cannot be captured", "try again, and restart deskhand-agent if it persists"
}

// StillResult is one still image of screen.capture.
type StillResult struct {
	// CaptureID names the image: "c-" and 12 hex digits.
	CaptureID string
	// Data is the encoded PNG or JPEG image of Width×Height pixels.
	Data          []byte
	Width, Height int
	// Scale is Width divided by the display's width in pixels.
	Scale float64
	// OriginX and OriginY place the display on the virtual desktop; DPI is
	// its effective DPI.
	OriginX, OriginY, DPI int
	// MonoUS is the monoclock reading of the moment the image was taken.
	MonoUS uint64
	// Method is "dd" for Desktop Duplication and "gdi" for GDI.
	Method string
}

// Still image formats and defaults of screen.capture.
const (
	FormatPNG  = "png"
	FormatJPEG = "jpeg"
	// DefaultStillMaxWidth is the max_width of a screen.capture that names
	// none.
	DefaultStillMaxWidth = 1568
	// DefaultJPEGQuality is the quality of a JPEG screen.capture that
	// names none.
	DefaultJPEGQuality = 80
)

// Methods of a StillResult and of the video stream's info frame.
const (
	MethodDD  = "dd"
	MethodGDI = "gdi"
)
