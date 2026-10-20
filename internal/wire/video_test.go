package wire

import (
	"bytes"
	"reflect"
	"testing"
)

func TestAURoundTrip(t *testing.T) {
	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = byte(i * 7)
	}
	in := AU{Key: true, HasConfig: true, Width: 3840, Height: 2160, Seq: 42,
		CaptureMonoUS: 1_000_000, EncodeDoneMonoUS: 1_004_000, Data: data}
	out, err := UnmarshalAU(in.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatal("AU mismatch")
	}
}

func TestAUMarshalLayout(t *testing.T) {
	p := AU{Key: true, Width: 0x01020304, Height: 5, Seq: 6, CaptureMonoUS: 7, EncodeDoneMonoUS: 8, Data: []byte{9}}.Marshal()
	want := []byte{
		0x01,
		1, 2, 3, 4,
		0, 0, 0, 5,
		0, 0, 0, 0, 0, 0, 0, 6,
		0, 0, 0, 0, 0, 0, 0, 7,
		0, 0, 0, 0, 0, 0, 0, 8,
		9,
	}
	if !bytes.Equal(p, want) {
		t.Fatalf("got %v", p)
	}
	p = AU{HasConfig: true}.Marshal()
	if p[0] != 0x02 {
		t.Fatalf("flags = %#x", p[0])
	}
}

func TestUnmarshalAUShort(t *testing.T) {
	if _, err := UnmarshalAU(make([]byte, 32)); err == nil {
		t.Fatal("expected error")
	}
}
