package capture

import (
	"bytes"
	"testing"
)

func pixel(bgra []byte, w, x, y int) [4]byte { i := (y*w + x) * 4; return [4]byte(bgra[i : i+4]) }

var (
	opaqueBlack = [4]byte{0, 0, 0, 255}
	opaqueWhite = [4]byte{255, 255, 255, 255}
	clearPixel  = [4]byte{0, 0, 0, 0}
)

func TestMonochromePointerShape(t *testing.T) {
	// 4×2 pointer, pitch 2: AND rows then XOR rows, the most significant
	// bit first. Row 0: black, white, clear, invert. Row 1: all clear.
	and := []byte{0b0011_0000, 0, 0b1111_0000, 0}
	xor := []byte{0b0101_0000, 0, 0b0000_0000, 0}
	s := pointerShape(pointerMonochrome, 4, 4, 2, 1, 9, append(and, xor...))
	if s == nil || s.W != 4 || s.H != 2 || s.HotX != 1 || s.HotY != 1 || len(s.BGRA) != 4*2*4 || s.ID == 0 {
		t.Fatalf("shape %+v", s)
	}
	for _, c := range []struct {
		x, y int
		want [4]byte
	}{
		{0, 0, opaqueBlack}, {1, 0, opaqueWhite}, {2, 0, opaqueWhite}, {3, 0, opaqueBlack},
		{0, 1, clearPixel}, {1, 1, clearPixel}, {2, 1, clearPixel}, {3, 1, opaqueWhite},
	} {
		if got := pixel(s.BGRA, 4, c.x, c.y); got != c.want {
			t.Errorf("pixel (%d,%d) %v, want %v", c.x, c.y, got, c.want)
		}
	}
}

func TestColorPointerShapeKeepsItsAlpha(t *testing.T) {
	buf := []byte{1, 2, 3, 128, 4, 5, 6, 0, 0xAA, 0xAA, 7, 8, 9, 255, 10, 11, 12, 64, 0xAA, 0xAA}
	s := pointerShape(pointerColor, 2, 2, 10, 0, 0, buf)
	if s == nil || s.W != 2 || s.H != 2 {
		t.Fatalf("shape %+v", s)
	}
	if want := []byte{1, 2, 3, 128, 4, 5, 6, 0, 7, 8, 9, 255, 10, 11, 12, 64}; !bytes.Equal(s.BGRA, want) {
		t.Fatalf("BGRA %v, want %v", s.BGRA, want)
	}
}

func TestMaskedColorPointerShape(t *testing.T) {
	// alpha 0: the color replaces the screen; alpha 0xFF: the color is
	// XORed with it, which leaves black clear and inverts otherwise.
	buf := []byte{10, 20, 30, 0, 0, 0, 0, 0xFF, 255, 255, 255, 0xFF}
	s := pointerShape(pointerMaskedColor, 3, 1, 12, 0, 0, buf)
	if s == nil {
		t.Fatal("no shape")
	}
	for x, want := range [][4]byte{{10, 20, 30, 255}, opaqueWhite, opaqueBlack} {
		if got := pixel(s.BGRA, 3, x, 0); got != want {
			t.Errorf("pixel %d %v, want %v", x, got, want)
		}
	}
}

func TestPointerShapeIDFollowsTheShape(t *testing.T) {
	buf := []byte{1, 2, 3, 255, 4, 5, 6, 255}
	a := pointerShape(pointerColor, 2, 1, 8, 0, 0, buf)
	b := pointerShape(pointerColor, 2, 1, 8, 0, 0, bytes.Clone(buf))
	c := pointerShape(pointerColor, 2, 1, 8, 1, 0, buf)
	if a.ID != b.ID || a.ID == c.ID {
		t.Fatalf("ids %d %d %d", a.ID, b.ID, c.ID)
	}
}

func TestPointerShapeRejectsAShortBuffer(t *testing.T) {
	for _, c := range []struct {
		typ                 uint32
		w, h, pitch, length int
	}{
		{pointerColor, 2, 2, 8, 15},
		{pointerColor, 2, 2, 4, 16},
		{pointerMonochrome, 8, 3, 1, 3},
		{pointerMonochrome, 8, 4, 1, 3},
		{pointerMaskedColor, 0, 1, 4, 4},
		{3, 1, 1, 4, 4},
	} {
		if s := pointerShape(c.typ, c.w, c.h, c.pitch, 0, 0, make([]byte, c.length)); s != nil {
			t.Errorf("type %d %dx%d pitch %d with %d bytes: %+v", c.typ, c.w, c.h, c.pitch, c.length, s)
		}
	}
}
