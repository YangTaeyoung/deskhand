package wire

import (
	"bytes"
	"reflect"
	"testing"
)

func TestCursorShapeRoundTrip(t *testing.T) {
	in := CursorShape{ID: 9, W: 2, H: 2, HotX: 1, HotY: 0, BGRA: bytes.Repeat([]byte{1, 2, 3, 4}, 4)}
	b, err := in.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	out, err := UnmarshalCursorShape(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("got %+v", out)
	}
}

func TestCursorShapeLayout(t *testing.T) {
	b, _ := CursorShape{ID: 0x0A0B0C0D, W: 1, H: 1, HotX: 2, HotY: 3, BGRA: []byte{1, 2, 3, 4}}.Marshal()
	want := []byte{0x0A, 0x0B, 0x0C, 0x0D, 0, 1, 0, 1, 0, 2, 0, 3, 1, 2, 3, 4}
	if !bytes.Equal(b, want) {
		t.Fatalf("got %v", b)
	}
}

func TestCursorShapeLengthMismatch(t *testing.T) {
	if _, err := (CursorShape{ID: 1, W: 2, H: 2, BGRA: make([]byte, 15)}).Marshal(); err == nil {
		t.Fatal("marshal: expected error")
	}
	good, _ := CursorShape{ID: 1, W: 2, H: 2, BGRA: make([]byte, 16)}.Marshal()
	if _, err := UnmarshalCursorShape(good[:len(good)-1]); err == nil {
		t.Fatal("unmarshal short: expected error")
	}
	if _, err := UnmarshalCursorShape(append(good, 0)); err == nil {
		t.Fatal("unmarshal long: expected error")
	}
	if _, err := UnmarshalCursorShape(good[:5]); err == nil {
		t.Fatal("unmarshal truncated header: expected error")
	}
}

func TestCursorPosRoundTrip(t *testing.T) {
	for _, in := range []CursorPos{
		{X: -5, Y: 1080, Visible: true, ShapeID: 77},
		{X: 0, Y: 0, Visible: false, ShapeID: 0},
	} {
		out, err := UnmarshalCursorPos(in.Marshal())
		if err != nil || out != in {
			t.Fatalf("got %+v err=%v", out, err)
		}
	}
}

func TestCursorPosLayoutAndLength(t *testing.T) {
	b := CursorPos{X: -1, Y: 2, Visible: true, ShapeID: 3}.Marshal()
	want := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0, 0, 0, 2, 1, 0, 0, 0, 3}
	if !bytes.Equal(b, want) {
		t.Fatalf("got %v", b)
	}
	if _, err := UnmarshalCursorPos(b[:12]); err == nil {
		t.Fatal("expected error")
	}
}
