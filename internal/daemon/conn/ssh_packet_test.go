package conn

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

func TestSSHPacketConnFramesDatagramsAndReadsReplies(t *testing.T) {
	readInput, writeInput := io.Pipe()
	readOutput, writeOutput := io.Pipe()
	peer := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 47600}
	c := newSSHPacketConn(readOutput, writeInput, peer, nil)
	defer c.Close()
	defer readInput.Close()
	defer writeOutput.Close()
	if n, err := c.WriteTo([]byte("abc"), peer); err != nil || n != 3 {
		t.Fatalf("write: %d %v", n, err)
	}
	frame := make([]byte, 9)
	if _, err := io.ReadFull(readInput, frame); err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint32(frame[:4]) != 1 || binary.BigEndian.Uint16(frame[4:6]) != 3 || !bytes.Equal(frame[6:], []byte("abc")) {
		t.Fatalf("bad frame %v", frame)
	}
	go func() { _, _ = writeOutput.Write([]byte{0, 0, 0, 1, 0, 3, 'x', 'y', 'z'}) }()
	buf := make([]byte, 10)
	n, addr, err := c.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "xyz" || addr.String() != peer.String() {
		t.Fatalf("read %q %v %v", buf[:n], addr, err)
	}
}

func TestSSHPacketConnDeadlineAndClose(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	outR, outW := io.Pipe()
	defer outR.Close()
	c := newSSHPacketConn(r, outW, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}, nil)
	if err := c.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, _, err := c.ReadFrom(make([]byte, 10))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	_ = c.Close()
	_, _, err = c.ReadFrom(make([]byte, 10))
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed: %v", err)
	}
	if _, err = c.WriteTo([]byte("x"), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
}

type shortRelayWriter struct{ b bytes.Buffer }

func (w *shortRelayWriter) Write(p []byte) (int, error) {
	if len(p) > 2 {
		p = p[:2]
	}
	return w.b.Write(p)
}
func TestRelayWritesEntireFrameWithShortWrites(t *testing.T) {
	var w shortRelayWriter
	if err := writeRelayBytes(&w, []byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if w.b.String() != "abcdef" {
		t.Fatalf("wrote %q", w.b.String())
	}
}
