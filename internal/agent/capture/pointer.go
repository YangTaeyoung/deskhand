package capture

import (
	"encoding/binary"
	"hash/fnv"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Pointer shape types of DXGI_OUTDUPL_POINTER_SHAPE_INFO.
const (
	pointerMonochrome  = 1
	pointerColor       = 2
	pointerMaskedColor = 4
)

// pixelKind is what a pointer pixel does to the screen under it.
type pixelKind byte

const (
	pixelClear pixelKind = iota
	pixelPaint
	pixelInvert
)

// pointerShape converts a DXGI pointer shape of type typ to
// non-premultiplied BGRA. w and h are the shape's width and height (for a
// monochrome shape h covers the AND mask and the XOR mask below it), pitch
// the bytes of a row of buf. Pixels that invert the screen become black
// with a white outline on the clear pixels around them. The ID is a hash
// of the pixels and the hot spot. It returns nil for an unknown type or a
// buffer too short for the size.
func pointerShape(typ uint32, w, h, pitch, hotX, hotY int, buf []byte) *wire.CursorShape {
	if w <= 0 || h <= 0 || w > 0xFFFF || h > 0xFFFF || pitch <= 0 {
		return nil
	}
	rowBytes := w * 4
	if typ == pointerMonochrome {
		if h%2 != 0 {
			return nil
		}
		rowBytes = (w + 7) / 8
	}
	if pitch < rowBytes || len(buf) < pitch*(h-1)+rowBytes {
		return nil
	}
	outH := h
	if typ == pointerMonochrome {
		outH = h / 2
	}
	bgra := make([]byte, w*outH*4)
	kinds := make([]pixelKind, w*outH)
	for y := 0; y < outH; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			px := bgra[i*4 : i*4+4]
			switch typ {
			case pointerMonochrome:
				bit := byte(0x80) >> (x % 8)
				and := buf[y*pitch+x/8]&bit != 0
				xor := buf[(y+outH)*pitch+x/8]&bit != 0
				switch {
				case !and && !xor:
					px[3], kinds[i] = 255, pixelPaint
				case !and:
					px[0], px[1], px[2], px[3], kinds[i] = 255, 255, 255, 255, pixelPaint
				case xor:
					kinds[i] = pixelInvert
				}
			case pointerColor:
				copy(px, buf[y*pitch+x*4:y*pitch+x*4+4])
				kinds[i] = pixelPaint
			case pointerMaskedColor:
				s := buf[y*pitch+x*4 : y*pitch+x*4+4]
				switch {
				case s[3] == 0:
					px[0], px[1], px[2], px[3], kinds[i] = s[0], s[1], s[2], 255, pixelPaint
				case s[0]|s[1]|s[2] != 0:
					kinds[i] = pixelInvert
				}
			default:
				return nil
			}
		}
	}
	outlineInverted(bgra, kinds, w, outH)
	s := &wire.CursorShape{W: uint16(w), H: uint16(outH), HotX: uint16(clamp(hotX, 0, w-1)), HotY: uint16(clamp(hotY, 0, outH-1)), BGRA: bgra}
	s.ID = shapeID(s)
	return s
}

// outlineInverted paints the inverting pixels opaque black and the clear
// pixels next to them (left, right, above, below) opaque white.
func outlineInverted(bgra []byte, kinds []pixelKind, w, h int) {
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if kinds[y*w+x] != pixelInvert {
				continue
			}
			i := (y*w + x) * 4
			bgra[i], bgra[i+1], bgra[i+2], bgra[i+3] = 0, 0, 0, 255
			for _, d := range [4][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
				nx, ny := x+d[0], y+d[1]
				if nx < 0 || ny < 0 || nx >= w || ny >= h || kinds[ny*w+nx] != pixelClear {
					continue
				}
				j := (ny*w + nx) * 4
				bgra[j], bgra[j+1], bgra[j+2], bgra[j+3] = 255, 255, 255, 255
			}
		}
	}
}

// shapeID is the FNV-1a hash of the size, hot spot and pixels of s, never
// 0.
func shapeID(s *wire.CursorShape) uint32 {
	h := fnv.New32a()
	var head [8]byte
	binary.LittleEndian.PutUint16(head[0:], s.W)
	binary.LittleEndian.PutUint16(head[2:], s.H)
	binary.LittleEndian.PutUint16(head[4:], s.HotX)
	binary.LittleEndian.PutUint16(head[6:], s.HotY)
	h.Write(head[:])
	h.Write(s.BGRA)
	return max(h.Sum32(), 1)
}

func clamp(v, lo, hi int) int { return min(max(v, lo), hi) }
