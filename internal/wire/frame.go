// Package wire defines the binary framing and message types shared by the
// agent and the daemon.
package wire

import (
	"encoding/binary"
	"errors"
	"io"
)

const (
	ALPN      = "deskhand/1"
	MaxFrame  = 32 << 20
	MaxHeader = 64 << 10
)

// Frame kinds: the first byte of every frame body.
const (
	KindAU            byte = 0x01
	KindVideoInfo     byte = 0x02
	KindVideoStats    byte = 0x03
	KindVideoPaused   byte = 0x04
	KindCursorShape   byte = 0x10
	KindCursorPos     byte = 0x11
	KindPTYData       byte = 0x20
	KindPTYResize     byte = 0x21
	KindPTYExit       byte = 0x22
	KindPTYAttached   byte = 0x23
	KindNotice        byte = 0x30
	KindClientControl byte = 0x40
	KindClientInput   byte = 0x41
)

var (
	ErrFrameTooLarge  = errors.New("wire: frame exceeds the size limit; split the payload into smaller frames")
	ErrEmptyFrame     = errors.New("wire: frame has zero length; the peer sent a malformed stream")
	ErrHeaderTooLarge = errors.New("wire: stream header exceeds 64 KiB; shorten the header fields")
)

// WriteFrame writes u32 BE (1+len(payload)), kind, payload in a single Write.
func WriteFrame(w io.Writer, kind byte, payload []byte) error {
	n := 1 + len(payload)
	if n > MaxFrame {
		return ErrFrameTooLarge
	}
	buf := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(buf, uint32(n))
	buf[4] = kind
	copy(buf[5:], payload)
	_, err := w.Write(buf)
	return err
}

// ReadFrame reads one frame. The length prefix (kind byte plus payload) must
// be between 1 and max.
func ReadFrame(r io.Reader, max int) (kind byte, payload []byte, err error) {
	var hdr [4]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 {
		return 0, nil, ErrEmptyFrame
	}
	if uint64(n) > uint64(max) {
		return 0, nil, ErrFrameTooLarge
	}
	body := make([]byte, n)
	if _, err = io.ReadFull(r, body); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return 0, nil, err
	}
	return body[0], body[1:], nil
}
