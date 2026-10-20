package wire

import (
	"encoding/binary"
	"errors"
)

const (
	cursorShapeHeaderLen = 12
	cursorPosLen         = 13
)

var errCursorBGRA = errors.New("wire: cursor BGRA length must equal width*height*4")

// CursorShape is a cursor bitmap (BGRA, non-premultiplied alpha).
type CursorShape struct {
	ID         uint32
	W, H       uint16
	HotX, HotY uint16
	BGRA       []byte
}

// Marshal returns the frame payload without the kind byte.
func (c CursorShape) Marshal() ([]byte, error) {
	if len(c.BGRA) != int(c.W)*int(c.H)*4 {
		return nil, errCursorBGRA
	}
	b := make([]byte, cursorShapeHeaderLen+len(c.BGRA))
	binary.BigEndian.PutUint32(b, c.ID)
	binary.BigEndian.PutUint16(b[4:], c.W)
	binary.BigEndian.PutUint16(b[6:], c.H)
	binary.BigEndian.PutUint16(b[8:], c.HotX)
	binary.BigEndian.PutUint16(b[10:], c.HotY)
	copy(b[cursorShapeHeaderLen:], c.BGRA)
	return b, nil
}

// UnmarshalCursorShape parses a shape payload. BGRA aliases p.
func UnmarshalCursorShape(p []byte) (CursorShape, error) {
	if len(p) < cursorShapeHeaderLen {
		return CursorShape{}, errors.New("wire: cursor shape payload shorter than its 12-byte header")
	}
	c := CursorShape{
		ID:   binary.BigEndian.Uint32(p),
		W:    binary.BigEndian.Uint16(p[4:]),
		H:    binary.BigEndian.Uint16(p[6:]),
		HotX: binary.BigEndian.Uint16(p[8:]),
		HotY: binary.BigEndian.Uint16(p[10:]),
		BGRA: p[cursorShapeHeaderLen:],
	}
	if len(c.BGRA) != int(c.W)*int(c.H)*4 {
		return CursorShape{}, errCursorBGRA
	}
	return c, nil
}

// CursorPos is the cursor position in capture-display pixels.
type CursorPos struct {
	X, Y    int32
	Visible bool
	ShapeID uint32
}

// Marshal returns the frame payload without the kind byte.
func (c CursorPos) Marshal() []byte {
	b := make([]byte, cursorPosLen)
	binary.BigEndian.PutUint32(b, uint32(c.X))
	binary.BigEndian.PutUint32(b[4:], uint32(c.Y))
	if c.Visible {
		b[8] = 1
	}
	binary.BigEndian.PutUint32(b[9:], c.ShapeID)
	return b
}

// UnmarshalCursorPos parses a pos payload.
func UnmarshalCursorPos(p []byte) (CursorPos, error) {
	if len(p) != cursorPosLen {
		return CursorPos{}, errors.New("wire: cursor pos payload must be exactly 13 bytes")
	}
	return CursorPos{
		X:       int32(binary.BigEndian.Uint32(p)),
		Y:       int32(binary.BigEndian.Uint32(p[4:])),
		Visible: p[8] != 0,
		ShapeID: binary.BigEndian.Uint32(p[9:]),
	}, nil
}
