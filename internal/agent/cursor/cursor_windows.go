//go:build windows

package cursor

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")

	procGetCursorInfo = user32.NewProc("GetCursorInfo")
	procGetIconInfo   = user32.NewProc("GetIconInfo")
	procGetDC         = user32.NewProc("GetDC")
	procReleaseDC     = user32.NewProc("ReleaseDC")
	procGetObjectW    = gdi32.NewProc("GetObjectW")
	procGetDIBits     = gdi32.NewProc("GetDIBits")
	procDeleteObject  = gdi32.NewProc("DeleteObject")
)

const (
	cursorShowing = 0x00000001
	dibRGBColors  = 0
	biRGB         = 0
)

// cursorInfo is CURSORINFO.
type cursorInfo struct {
	size, flags uint32
	cursor      uintptr
	x, y        int32
}

// iconInfo is ICONINFO.
type iconInfo struct {
	icon              int32
	hotX, hotY        uint32
	hbmMask, hbmColor uintptr
}

// bitmap is BITMAP.
type bitmap struct {
	typ, width, height, widthBytes int32
	planes, bitsPixel              uint16
	bits                           uintptr
}

// bitmapInfo is BITMAPINFO with the two colors of a 1 bpp image.
type bitmapInfo struct {
	size                     uint32
	width, height            int32
	planes, bitCount         uint16
	compression, sizeImage   uint32
	xPelsPerMeter, yPelsPerM int32
	clrUsed, clrImportant    uint32
	colors                   [2]uint32
}

// windowsSource reads the cursor of the input desktop.
type windowsSource struct{}

func platformSource() source { return windowsSource{} }

func (windowsSource) sample() sample {
	ci := cursorInfo{size: uint32(unsafe.Sizeof(cursorInfo{}))}
	if r, _, _ := procGetCursorInfo.Call(uintptr(unsafe.Pointer(&ci))); r == 0 {
		return sample{}
	}
	return sample{ok: true, showing: ci.flags&cursorShowing != 0, x: int(ci.x), y: int(ci.y), handle: ci.cursor}
}

// shape reads the bitmaps of cursor h. Without a color bitmap the mask
// holds the AND mask above the XOR mask; a color bitmap with any alpha is
// drawn with that alpha, and one without alpha with the AND mask.
func (windowsSource) shape(h uintptr) (*wire.CursorShape, error) {
	var ii iconInfo
	if r, _, err := procGetIconInfo.Call(h, uintptr(unsafe.Pointer(&ii))); r == 0 {
		return nil, fmt.Errorf("GetIconInfo: %w", err)
	}
	defer procDeleteObject.Call(ii.hbmMask)
	if ii.hbmColor != 0 {
		defer procDeleteObject.Call(ii.hbmColor)
	}
	dc, _, _ := procGetDC.Call(0)
	if dc == 0 {
		return nil, fmt.Errorf("GetDC failed")
	}
	defer procReleaseDC.Call(0, dc)
	x, y := int(ii.hotX), int(ii.hotY)
	if ii.hbmColor == 0 {
		w, mh, err := bitmapSize(ii.hbmMask)
		if err != nil {
			return nil, err
		}
		mask, pitch, err := dibits(dc, ii.hbmMask, w, mh, 1)
		if err != nil {
			return nil, err
		}
		return converted(capture.PointerShape(capture.PointerMonochrome, w, mh, pitch, x, y, mask))
	}
	w, ch, err := bitmapSize(ii.hbmColor)
	if err != nil {
		return nil, err
	}
	color, pitch, err := dibits(dc, ii.hbmColor, w, ch, 32)
	if err != nil {
		return nil, err
	}
	if hasAlpha(color) {
		return converted(capture.PointerShape(capture.PointerColor, w, ch, pitch, x, y, color))
	}
	mask, maskPitch, err := dibits(dc, ii.hbmMask, w, ch, 1)
	if err != nil {
		return nil, err
	}
	for py := 0; py < ch; py++ {
		for px := 0; px < w; px++ {
			a := byte(0)
			if mask[py*maskPitch+px/8]&(0x80>>(px%8)) != 0 {
				a = 0xFF
			}
			color[py*pitch+px*4+3] = a
		}
	}
	return converted(capture.PointerShape(capture.PointerMaskedColor, w, ch, pitch, x, y, color))
}

func converted(s *wire.CursorShape) (*wire.CursorShape, error) {
	if s == nil {
		return nil, fmt.Errorf("the cursor bitmap has a size or form the agent cannot convert")
	}
	return s, nil
}

// bitmapSize returns the width and height of bitmap h.
func bitmapSize(h uintptr) (int, int, error) {
	var bm bitmap
	if r, _, _ := procGetObjectW.Call(h, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm))); r == 0 {
		return 0, 0, fmt.Errorf("GetObject of a cursor bitmap failed")
	}
	if bm.width <= 0 || bm.height <= 0 || bm.width > 1024 || bm.height > 2048 {
		return 0, 0, fmt.Errorf("the cursor bitmap is %dx%d", bm.width, bm.height)
	}
	return int(bm.width), int(bm.height), nil
}

// dibits returns the rows of bitmap h, w×hgt pixels, from the top at bits
// bits per pixel, and the bytes of a row.
func dibits(dc, h uintptr, w, hgt int, bits uint16) ([]byte, int, error) {
	pitch := (w*int(bits) + 31) / 32 * 4
	buf := make([]byte, pitch*hgt)
	bi := bitmapInfo{width: int32(w), height: -int32(hgt), planes: 1, bitCount: bits, compression: biRGB}
	bi.size = uint32(unsafe.Offsetof(bi.colors))
	if r, _, _ := procGetDIBits.Call(dc, h, 0, uintptr(hgt), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), dibRGBColors); int(r) != hgt {
		return nil, 0, fmt.Errorf("GetDIBits copied %d of %d rows", r, hgt)
	}
	return buf, pitch, nil
}

// hasAlpha reports whether any pixel of 32 bpp rows has a non-zero alpha.
func hasAlpha(bgra []byte) bool {
	for i := 3; i < len(bgra); i += 4 {
		if bgra[i] != 0 {
			return true
		}
	}
	return false
}
