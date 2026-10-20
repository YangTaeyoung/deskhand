//go:build windows

package capture

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/agent/monoclock"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// vtable indices (docs/adr/0001-screen-pipeline.md).
const (
	output1DuplicateOutput   = 22
	duplGetDesc              = 7
	duplAcquireNextFrame     = 8
	duplGetFramePointerShape = 11
	duplReleaseFrame         = 14
)

// DXGI_MODE_ROTATION values the duplication captures upright.
const (
	rotationUnspecified = 0
	rotationIdentity    = 1
)

// acquireSlice bounds one AcquireNextFrame wait; Next checks its context
// between the waits.
const acquireSlice = 100 * time.Millisecond

var (
	errRotated     = errors.New("the display is rotated")
	errModeChanged = errors.New("the desktop image changed its size")
)

// duplDesc is DXGI_OUTDUPL_DESC.
type duplDesc struct {
	Width, Height, RefreshNum, RefreshDen, Format, ScanlineOrdering, Scaling uint32
	Rotation                                                                 uint32
	DesktopImageInSystemMemory                                               int32
}

// frameInfo is DXGI_OUTDUPL_FRAME_INFO.
type frameInfo struct {
	LastPresentTime, LastMouseUpdateTime            int64
	AccumulatedFrames                               uint32
	RectsCoalesced, ProtectedContentMaskedOut       int32
	PointerX, PointerY                              int32
	PointerVisible                                  int32
	TotalMetadataBufferSize, PointerShapeBufferSize uint32
}

// shapeInfo is DXGI_OUTDUPL_POINTER_SHAPE_INFO.
type shapeInfo struct {
	Type, Width, Height, Pitch uint32
	HotX, HotY                 int32
}

// ddSource captures one output with Desktop Duplication into a BGRA
// texture of its own.
type ddSource struct {
	dev *d3d.Device
	// ownsDev reports that Close closes dev.
	ownsDev bool
	disp    display.Info
	// dup is the IDXGIOutputDuplication, 0 during an outage.
	dup uintptr
	// tex is the texture of w×h pixels the desktop image is copied to;
	// haveImage reports that it holds one. It is the source's own unless
	// stage, which then gives it, is set.
	tex       uintptr
	stage     func(w, h int) (uintptr, error)
	w, h      int
	haveImage bool
	// fresh reports that no frame was acquired since the duplication was
	// made; the first one holds the whole desktop image.
	fresh bool
	// ptr is the last pointer position and visibility.
	ptr      PointerInfo
	shapeBuf []byte
	out      outage
	reg      *entry
}

// duplicate duplicates the output and makes the texture fit it. A failed
// DuplicateOutput is its HRESULT with a nil error; a rotated display is
// errRotated.
func (s *ddSource) duplicate() (com.HRESULT, error) {
	var dup uintptr
	hr := com.Call(s.dev.Output, output1DuplicateOutput, s.dev.Device, uintptr(unsafe.Pointer(&dup)))
	if hr.Failed() {
		return hr, nil
	}
	var desc duplDesc
	com.CallRaw(dup, duplGetDesc, uintptr(unsafe.Pointer(&desc)))
	if desc.Rotation != rotationUnspecified && desc.Rotation != rotationIdentity {
		com.Release(dup)
		return hr, errRotated
	}
	w, h := int(desc.Width), int(desc.Height)
	if s.tex == 0 || w != s.w || h != s.h {
		var tex uintptr
		var err error
		if s.stage != nil {
			tex, err = s.stage(w, h)
		} else {
			tex, err = s.dev.NewTexture(w, h, d3d.FormatBGRA, d3d.UsageDefault, d3d.BindRenderTarget|d3d.BindShaderResource, 0)
		}
		if err != nil {
			com.Release(dup)
			return hr, err
		}
		if s.stage == nil {
			com.Release(s.tex)
		}
		s.tex, s.w, s.h, s.haveImage = tex, w, h, false
	}
	s.dup, s.fresh = dup, true
	return hr, nil
}

func (s *ddSource) Device() *d3d.Device { return s.dev }

func (s *ddSource) Size() (int, int) { return s.w, s.h }

func (s *ddSource) Close() {
	com.Release(s.dup)
	if s.stage == nil {
		com.Release(s.tex)
	}
	s.dup, s.tex = 0, 0
	if s.ownsDev {
		s.dev.Close()
	}
	s.reg.close()
}

// Next acquires the next frame. A frame carries an image or a pointer
// update, and only comes once the texture holds an image. Access lost or
// denied, or a desktop image of another size, starts an outage in which
// Next duplicates the output again on the outage's schedule.
func (s *ddSource) Next(ctx context.Context, timeout time.Duration) (Frame, error) {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return Frame{}, err
		}
		if s.dup == 0 {
			return s.recover(ctx, deadline)
		}
		ms := uint32(0)
		if wait := min(time.Until(deadline), acquireSlice); wait > 0 {
			ms = uint32((wait + time.Millisecond - 1) / time.Millisecond)
		}
		var fi frameInfo
		var res uintptr
		hr := com.Call(s.dup, duplAcquireNextFrame, uintptr(ms), uintptr(unsafe.Pointer(&fi)), uintptr(unsafe.Pointer(&res)))
		switch {
		case hr == com.DXGI_ERROR_WAIT_TIMEOUT:
		case accessDenied(hr):
			s.lose(hr)
			continue
		case hr.Failed():
			return Frame{}, fmt.Errorf("IDXGIOutputDuplication::AcquireNextFrame of %s: %w", s.disp.Name, hr)
		default:
			f, ok, err := s.take(&fi, res)
			switch {
			case errors.Is(err, errModeChanged):
				s.lose(com.DXGI_ERROR_ACCESS_LOST)
				continue
			case err != nil:
				return Frame{}, err
			case ok:
				return f, nil
			}
		}
		if !time.Now().Before(deadline) {
			return Frame{}, ErrNoNewFrame
		}
	}
}

// lose releases the duplication and starts the outage, or goes on with
// the one whose duplication was made again without an image, as
// access_lost with hr as its detail.
func (s *ddSource) lose(hr com.HRESULT) {
	com.Release(s.dup)
	s.dup = 0
	s.out.begin(time.Now(), hr, &Unavailable{Reason: ReasonAccessLost, Detail: hr.Error()})
}

// recover duplicates the output again whenever a retry is due until the
// deadline. It returns ErrReset once the duplication is back, the error
// of the outage at the deadline once it reports one, and ErrNoNewFrame
// before; a display that cannot be duplicated any more, rotated for
// example, is an error at the deadline. The outage goes on until take
// copies an image, so a duplication lost again before one waits for the
// next retry. The registry entry records each error reported.
func (s *ddSource) recover(ctx context.Context, deadline time.Time) (Frame, error) {
	for {
		now := time.Now()
		if s.out.due(now) {
			hr, err := s.duplicate()
			if err != nil {
				err = fmt.Errorf("display %s cannot be duplicated again (%w); open the capture again", s.disp.Name, err)
				s.reg.set(asUnavailable(err))
				_ = sleepUntil(ctx, deadline)
				return Frame{}, err
			}
			if hr.Succeeded() {
				s.out.retried(now)
				return Frame{}, ErrReset
			}
			s.out.failed(now, hr, redupReason(hr))
		}
		err := s.out.err(now, "IDXGIOutput1::DuplicateOutput of "+s.disp.Name)
		if err != nil {
			s.reg.set(asUnavailable(err))
		}
		if !now.Before(deadline) {
			if err != nil {
				return Frame{}, err
			}
			return Frame{}, ErrNoNewFrame
		}
		if err := sleepUntil(ctx, minTime(deadline, s.out.nextTry)); err != nil {
			return Frame{}, err
		}
	}
}

// take copies the acquired desktop image into the texture when the frame
// has one, reads the pointer and releases the frame. It reports whether
// the frame is one for Next to return.
func (s *ddSource) take(fi *frameInfo, res uintptr) (Frame, bool, error) {
	defer func() {
		com.Release(res)
		com.Call(s.dup, duplReleaseFrame)
	}()
	moved := fi.LastMouseUpdateTime != 0
	if moved {
		s.ptr.X, s.ptr.Y, s.ptr.Visible = int(fi.PointerX), int(fi.PointerY), fi.PointerVisible != 0
	}
	ptr := PointerInfo{X: s.ptr.X, Y: s.ptr.Y, Visible: s.ptr.Visible}
	if fi.PointerShapeBufferSize > 0 {
		if shape := s.pointerShape(fi.PointerShapeBufferSize); shape != nil {
			ptr.ShapeChanged, ptr.Shape = true, shape
		}
	}
	image := s.fresh || (fi.LastPresentTime != 0 && fi.AccumulatedFrames > 0)
	if image {
		tex, hr := com.QueryInterface(res, &d3d.IID_ID3D11Texture2D)
		if hr.Failed() {
			return Frame{}, false, fmt.Errorf("the desktop image of %s is not a texture: %w", s.disp.Name, hr)
		}
		desc := d3d.DescOf(tex)
		switch {
		case int(desc.Width) != s.w || int(desc.Height) != s.h:
			com.Release(tex)
			return Frame{}, false, errModeChanged
		case desc.Format != d3d.FormatBGRA:
			com.Release(tex)
			return Frame{}, false, fmt.Errorf("the desktop image of %s has DXGI format %d, not BGRA; open the capture again", s.disp.Name, desc.Format)
		}
		s.dev.CopyResource(s.tex, tex)
		com.Release(tex)
		s.fresh, s.haveImage = false, true
		if s.out.active() {
			s.out = outage{}
			s.reg.set(nil)
		}
	}
	if !s.haveImage || (!image && !moved && !ptr.ShapeChanged) {
		return Frame{}, false, nil
	}
	mono := monoclock.NowUS()
	if image && fi.LastPresentTime != 0 {
		mono = qpcMono(fi.LastPresentTime)
	}
	return Frame{Texture: s.tex, Width: s.w, Height: s.h, MonoUS: mono, Dirty: image, Pointer: ptr}, true, nil
}

// pointerShape reads the new pointer shape of the acquired frame.
func (s *ddSource) pointerShape(size uint32) *wire.CursorShape {
	if len(s.shapeBuf) < int(size) {
		s.shapeBuf = make([]byte, size)
	}
	var need uint32
	var si shapeInfo
	hr := com.Call(s.dup, duplGetFramePointerShape, uintptr(len(s.shapeBuf)), uintptr(unsafe.Pointer(&s.shapeBuf[0])),
		uintptr(unsafe.Pointer(&need)), uintptr(unsafe.Pointer(&si)))
	if hr.Failed() || int(need) > len(s.shapeBuf) {
		return nil
	}
	return PointerShape(si.Type, int(si.Width), int(si.Height), int(si.Pitch), int(si.HotX), int(si.HotY), s.shapeBuf[:need])
}

// redupReason is the reason of a failed duplication of the output again:
// Classify's with the session now, and access_lost for
// DXGI_ERROR_NOT_CURRENTLY_AVAILABLE unless the session names another.
func redupReason(hr com.HRESULT) *Unavailable {
	u := classifyNow(hr)
	if hr == com.DXGI_ERROR_NOT_CURRENTLY_AVAILABLE && (u == nil || u.Reason == ReasonNoOutput) {
		return &Unavailable{Reason: ReasonAccessLost, Detail: hr.Error()}
	}
	return u
}
