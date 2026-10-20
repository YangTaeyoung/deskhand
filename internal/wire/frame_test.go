package wire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte("hello")
	if err := WriteFrame(&buf, KindPTYData, payload); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	if got := binary.BigEndian.Uint32(raw[:4]); got != uint32(1+len(payload)) {
		t.Fatalf("length prefix = %d", got)
	}
	kind, got, err := ReadFrame(&buf, MaxFrame)
	if err != nil {
		t.Fatal(err)
	}
	if kind != KindPTYData || !bytes.Equal(got, payload) {
		t.Fatalf("kind=%#x payload=%q", kind, got)
	}
}

func TestFrameEmptyPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, KindNotice, nil); err != nil {
		t.Fatal(err)
	}
	kind, got, err := ReadFrame(&buf, MaxFrame)
	if err != nil || kind != KindNotice || len(got) != 0 {
		t.Fatalf("kind=%#x len=%d err=%v", kind, len(got), err)
	}
}

func TestReadFrameRejectsOversize(t *testing.T) {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 101)
	_, _, err := ReadFrame(bytes.NewReader(hdr[:]), 100)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestReadFrameRejectsZeroLength(t *testing.T) {
	_, _, err := ReadFrame(bytes.NewReader(make([]byte, 4)), MaxFrame)
	if !errors.Is(err, ErrEmptyFrame) {
		t.Fatalf("err = %v", err)
	}
}

func TestWriteFrameRejectsOversize(t *testing.T) {
	err := WriteFrame(&bytes.Buffer{}, KindAU, make([]byte, MaxFrame))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestReadFrameTruncated(t *testing.T) {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 10)
	_, _, err := ReadFrame(bytes.NewReader(append(hdr[:], 1, 2)), MaxFrame)
	if err == nil {
		t.Fatal("expected error")
	}
}
