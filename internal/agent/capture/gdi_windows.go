//go:build windows

package capture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/agent/monoclock"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")

	procGetDC              = user32.NewProc("GetDC")
	procReleaseDC          = user32.NewProc("ReleaseDC")
	procGetSystemMetrics   = user32.NewProc("GetSystemMetrics")
	procGetCursorInfo      = user32.NewProc("GetCursorInfo")
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBM = gdi32.NewProc("CreateCompatibleBitmap")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procBitBlt             = gdi32.NewProc("BitBlt")
	procGdiFlush           = gdi32.NewProc("GdiFlush")
)

const (
	// ropCapture is SRCCOPY | CAPTUREBLT: copy the screen with its layered
	// windows.
	ropCapture = 0x00CC0020 | 0x40000000
	// smXVirtualScreen is SM_XVIRTUALSCREEN; SM_YVIRTUALSCREEN,
	// SM_CXVIRTUALSCREEN and SM_CYVIRTUALSCREEN follow it.
	smXVirtualScreen = 76
	// cursorShowing is CURSOR_SHOWING of CURSORINFO.flags.
	cursorShowing = 1
	// gdiPoll is the time between two captures while the image does not
	// change.
	gdiPoll = 15 * time.Millisecond
)

// bitmapInfo is BITMAPINFO with its BITMAPINFOHEADER and one color.
type bitmapInfo struct {
	Size                       uint32
	Width, Height              int32
	Planes, BitCount           uint16
	Compression, SizeImage     uint32
	XPelsPerMeter, YPelsPerMtr int32
	ClrUsed, ClrImportant      uint32
	Colors                     [1]uint32
}

// cursorInfo is CURSORINFO.
type cursorInfo struct {
	Size, Flags uint32
	Cursor      uintptr
	X, Y        int32
}

// dib is a top-down 32-bit DIB section and its pixels.
type dib struct {
	bmp uintptr
	pix []byte
}

// gdiSource captures one display with BitBlt from the screen into two DIB
// sections in turn: the next capture goes into the section the frame
// returned last does not use and is compared with it.
type gdiSource struct {
	dev *d3d.Device
	// ownsDev reports that Close closes dev.
	ownsDev bool
	disp    display.Info
	screen  uintptr
	mem     uintptr
	// oldBmp is the bitmap of mem before the first DIB was selected.
	oldBmp uintptr
	bufs   [2]dib
	// cur is the buffer of the frame returned last, -1 before the first.
	cur int
	// pending reports that bufs[0] holds the capture of openGDI, taken at
	// pendingMono and not returned yet.
	pending     bool
	pendingMono uint64
	// virt is the virtual screen, SM_XVIRTUALSCREEN to SM_CYVIRTUALSCREEN,
	// when the buffers were made.
	virt [4]uintptr
	out  outage
	reg  *entry
}

// openGDI makes a GDI source of display d and captures it once. dev, which
// may be nil, becomes the source's device; the source does not own it.
func openGDI(dev *d3d.Device, d display.Info) (*gdiSource, error) {
	s := &gdiSource{dev: dev, disp: d, cur: -1}
	screen, _, err := procGetDC.Call(0)
	if screen == 0 {
		return nil, fmt.Errorf("GetDC of the screen: %w", err)
	}
	s.screen = screen
	mem, _, err := procCreateCompatibleDC.Call(screen)
	if mem == 0 {
		s.free()
		return nil, fmt.Errorf("CreateCompatibleDC: %w", err)
	}
	s.mem = mem
	if err := s.alloc(); err != nil {
		s.free()
		return nil, err
	}
	if err := s.grab(0); err != nil {
		s.free()
		return nil, err
	}
	s.pending, s.pendingMono = true, monoclock.NowUS()
	return s, nil
}

// alloc makes the two DIB sections of the display's size and records the
// virtual screen.
func (s *gdiSource) alloc() error {
	w, h := s.disp.Width, s.disp.Height
	for i := range s.bufs {
		bi := bitmapInfo{Size: 40, Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
		var bits unsafe.Pointer
		bmp, _, err := procCreateDIBSection.Call(s.mem, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
		if bmp == 0 || bits == nil {
			return fmt.Errorf("CreateDIBSection %dx%d: %w", w, h, err)
		}
		s.bufs[i] = dib{bmp: bmp, pix: unsafe.Slice((*byte)(bits), w*h*4)}
	}
	s.virt = virtualScreen()
	return nil
}

// release deletes the DIB sections.
func (s *gdiSource) release() {
	if s.oldBmp != 0 {
		procSelectObject.Call(s.mem, s.oldBmp)
		s.oldBmp = 0
	}
	for i := range s.bufs {
		if s.bufs[i].bmp != 0 {
			procDeleteObject.Call(s.bufs[i].bmp)
		}
		s.bufs[i] = dib{}
	}
}

// free releases the DIB sections and the device contexts.
func (s *gdiSource) free() {
	s.release()
	if s.mem != 0 {
		procDeleteDC.Call(s.mem)
		s.mem = 0
	}
	if s.screen != 0 {
		procReleaseDC.Call(0, s.screen)
		s.screen = 0
	}
}

// grab copies the display into buffer i.
func (s *gdiSource) grab(i int) error {
	old, _, _ := procSelectObject.Call(s.mem, s.bufs[i].bmp)
	if s.oldBmp == 0 {
		s.oldBmp = old
	}
	d := s.disp
	r, _, err := procBitBlt.Call(s.mem, 0, 0, uintptr(d.Width), uintptr(d.Height), s.screen, uintptr(d.X), uintptr(d.Y), ropCapture)
	procGdiFlush.Call()
	if r == 0 {
		return fmt.Errorf("BitBlt of %s: %w", d.Name, err)
	}
	return nil
}

func virtualScreen() [4]uintptr {
	var v [4]uintptr
	for i := range v {
		v[i], _, _ = procGetSystemMetrics.Call(uintptr(smXVirtualScreen + i))
	}
	return v
}

func (s *gdiSource) Device() *d3d.Device { return s.dev }

func (s *gdiSource) Size() (int, int) { return s.disp.Width, s.disp.Height }

func (s *gdiSource) Close() {
	s.free()
	if s.ownsDev {
		s.dev.Close()
	}
	s.reg.close()
}

// Next captures the display until its image differs from the frame
// returned last, every gdiPoll. A change of the virtual screen makes the
// buffers again for the display's new size and returns ErrReset, or the
// error at the deadline when the display is gone. A failed capture starts
// an outage that retries on its schedule.
func (s *gdiSource) Next(ctx context.Context, timeout time.Duration) (Frame, error) {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return Frame{}, err
		}
		if virtualScreen() != s.virt {
			if err := s.resize(); err != nil {
				_ = sleepUntil(ctx, deadline)
				return Frame{}, err
			}
			return Frame{}, ErrReset
		}
		if s.pending {
			s.pending, s.cur = false, 0
			return s.frame(0, s.pendingMono), nil
		}
		now := time.Now()
		wake := now.Add(gdiPoll)
		if !s.out.active() || !now.Before(s.out.nextTry) {
			into := 0
			if s.cur == 0 {
				into = 1
			}
			if err := s.grab(into); err != nil {
				hr := hresultOf(err)
				s.out.begin(now)
				s.out.failed(now, hr, classifyNow(hr))
			} else {
				if s.out.active() {
					s.out = outage{}
					s.reg.set(nil)
				}
				if s.cur < 0 || !bytes.Equal(s.bufs[into].pix, s.bufs[s.cur].pix) {
					s.cur = into
					return s.frame(into, monoclock.NowUS()), nil
				}
			}
		}
		var err error
		if s.out.active() {
			err = s.out.err(now, "BitBlt of "+s.disp.Name)
			var u *Unavailable
			if errors.As(err, &u) {
				s.reg.set(u)
			}
			wake = s.out.nextTry
		}
		if !now.Before(deadline) {
			if err != nil {
				return Frame{}, err
			}
			return Frame{}, ErrNoNewFrame
		}
		if err := sleepUntil(ctx, minTime(deadline, wake)); err != nil {
			return Frame{}, err
		}
	}
}

// resize finds the display by name again and makes the buffers for its
// size.
func (s *gdiSource) resize() error {
	list, err := display.List()
	if err != nil {
		return classifyErr(err)
	}
	for _, d := range list {
		if d.Name == s.disp.Name {
			s.release()
			s.disp, s.cur, s.pending = d, -1, false
			return s.alloc()
		}
	}
	return fmt.Errorf("display %s is no longer attached; open the capture again: %w", s.disp.Name, &noDisplayError{index: s.disp.Index, count: len(list)})
}

// frame is the frame of buffer i with the pointer as it is now.
func (s *gdiSource) frame(i int, mono uint64) Frame {
	return Frame{CPU: s.bufs[i].pix, Width: s.disp.Width, Height: s.disp.Height, MonoUS: mono, Dirty: true, Pointer: s.pointer()}
}

// pointer is the pointer position relative to the display and whether it
// shows on it.
func (s *gdiSource) pointer() PointerInfo {
	ci := cursorInfo{Size: uint32(unsafe.Sizeof(cursorInfo{}))}
	if r, _, _ := procGetCursorInfo.Call(uintptr(unsafe.Pointer(&ci))); r == 0 {
		return PointerInfo{}
	}
	x, y := int(ci.X)-s.disp.X, int(ci.Y)-s.disp.Y
	visible := ci.Flags&cursorShowing != 0 && x >= 0 && y >= 0 && x < s.disp.Width && y < s.disp.Height
	return PointerInfo{X: x, Y: y, Visible: visible}
}

// copyPixel copies the screen pixel at (x, y) of the virtual desktop into
// a bitmap of its own and returns the error when it cannot.
func copyPixel(x, y int) error {
	screen, _, err := procGetDC.Call(0)
	if screen == 0 {
		return fmt.Errorf("GetDC of the screen: %w", err)
	}
	defer procReleaseDC.Call(0, screen)
	mem, _, err := procCreateCompatibleDC.Call(screen)
	if mem == 0 {
		return fmt.Errorf("CreateCompatibleDC: %w", err)
	}
	defer procDeleteDC.Call(mem)
	bmp, _, err := procCreateCompatibleBM.Call(screen, 1, 1)
	if bmp == 0 {
		return fmt.Errorf("CreateCompatibleBitmap: %w", err)
	}
	defer procDeleteObject.Call(bmp)
	old, _, _ := procSelectObject.Call(mem, bmp)
	defer procSelectObject.Call(mem, old)
	r, _, err := procBitBlt.Call(mem, 0, 0, 1, 1, screen, uintptr(x), uintptr(y), ropCapture)
	if r == 0 {
		return fmt.Errorf("BitBlt of the pixel (%d,%d): %w", x, y, err)
	}
	return nil
}
