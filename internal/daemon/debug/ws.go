package debug

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// wsGUID is the key suffix of the RFC 6455 opening handshake.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// maxMessage bounds one received WebSocket message.
const maxMessage = 256 << 20

// WebSocket opcodes.
const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

// wsConn is the client end of a WebSocket connection (RFC 6455): it sends
// masked text messages and reads whole messages, answering pings.
type wsConn struct {
	c  net.Conn
	br *bufio.Reader

	wmu    sync.Mutex
	closed bool
}

// AcceptKey returns the Sec-WebSocket-Accept value of key.
func AcceptKey(key string) string {
	sum := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// dialWS opens a WebSocket to path on the TCP address addr.
func dialWS(ctx context.Context, addr, path string) (*wsConn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		c.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(raw[:])
	if path == "" || path[0] != '/' {
		path = "/" + path
	}
	req := "GET " + path + " HTTP/1.1\r\nHost: " + addr + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := io.WriteString(c, req); err != nil {
		c.Close()
		return nil, err
	}
	br := bufio.NewReaderSize(c, 64<<10)
	res, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		c.Close()
		return nil, err
	}
	if res.StatusCode != http.StatusSwitchingProtocols {
		c.Close()
		return nil, fmt.Errorf("the WebSocket handshake answered %s", res.Status)
	}
	if !strings.EqualFold(res.Header.Get("Upgrade"), "websocket") || res.Header.Get("Sec-WebSocket-Accept") != AcceptKey(key) {
		c.Close()
		return nil, errors.New("the WebSocket handshake answered without a valid upgrade")
	}
	_ = c.SetDeadline(time.Time{})
	return &wsConn{c: c, br: br}, nil
}

// writeFrame writes one masked frame.
func (w *wsConn) writeFrame(op byte, payload []byte) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	if w.closed {
		return net.ErrClosed
	}
	return writeFrame(w.c, op, payload, true)
}

// writeFrame writes one final frame of op with payload, masked when mask is
// set.
func writeFrame(wr io.Writer, op byte, payload []byte, mask bool) error {
	hdr := make([]byte, 0, 14)
	hdr = append(hdr, 0x80|op)
	mbit := byte(0)
	if mask {
		mbit = 0x80
	}
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, mbit|byte(n))
	case n <= 0xFFFF:
		hdr = append(hdr, mbit|126, byte(n>>8), byte(n))
	default:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		hdr = append(hdr, mbit|127)
		hdr = append(hdr, b[:]...)
	}
	body := payload
	if mask {
		var key [4]byte
		if _, err := rand.Read(key[:]); err != nil {
			return err
		}
		hdr = append(hdr, key[:]...)
		body = make([]byte, n)
		for i := range payload {
			body[i] = payload[i] ^ key[i&3]
		}
	}
	if _, err := wr.Write(append(hdr, body...)); err != nil {
		return err
	}
	return nil
}

// WriteText sends one text message.
func (w *wsConn) WriteText(p []byte) error { return w.writeFrame(opText, p) }

// ReadMessage returns the next text or binary message, answering pings
// and ending with io.EOF on a close frame.
func (w *wsConn) ReadMessage() ([]byte, error) {
	return readMessage(w.br, func(op byte, p []byte) error { return w.writeFrame(op, p) })
}

// readMessage reads frames from r until a whole data message; control
// frames are answered through reply.
func readMessage(r io.Reader, reply func(op byte, p []byte) error) ([]byte, error) {
	var msg []byte
	started := false
	for {
		fin, op, payload, err := readFrame(r)
		if err != nil {
			return nil, err
		}
		switch op {
		case opPing:
			_ = reply(opPong, payload)
			continue
		case opPong:
			continue
		case opClose:
			_ = reply(opClose, nil)
			return nil, io.EOF
		case opText, opBinary:
			if started {
				return nil, errors.New("a WebSocket data frame interrupted a fragmented message")
			}
			started = true
			msg = append(msg[:0], payload...)
		case opContinuation:
			if !started {
				return nil, errors.New("a WebSocket continuation frame came without a message")
			}
			msg = append(msg, payload...)
		default:
			return nil, fmt.Errorf("unknown WebSocket opcode %d", op)
		}
		if len(msg) > maxMessage {
			return nil, fmt.Errorf("a WebSocket message is larger than %d bytes", maxMessage)
		}
		if fin {
			return msg, nil
		}
	}
}

// readFrame reads one frame and unmasks its payload.
func readFrame(r io.Reader) (fin bool, op byte, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(r, h[:]); err != nil {
		return
	}
	fin = h[0]&0x80 != 0
	op = h[0] & 0x0F
	masked := h[1]&0x80 != 0
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(r, b[:]); err != nil {
			return
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(r, b[:]); err != nil {
			return
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > maxMessage {
		err = fmt.Errorf("a WebSocket frame is larger than %d bytes", maxMessage)
		return
	}
	var key [4]byte
	if masked {
		if _, err = io.ReadFull(r, key[:]); err != nil {
			return
		}
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(r, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= key[i&3]
		}
	}
	return
}

// Close sends a close frame and closes the connection.
func (w *wsConn) Close() error {
	w.wmu.Lock()
	if w.closed {
		w.wmu.Unlock()
		return nil
	}
	_ = w.c.SetWriteDeadline(time.Now().Add(time.Second))
	_ = writeFrame(w.c, opClose, []byte{0x03, 0xE8}, true)
	w.closed = true
	w.wmu.Unlock()
	return w.c.Close()
}

// ServerConn is the server end of a WebSocket, for test servers that speak
// CDP: it reads masked client frames and writes unmasked ones.
type ServerConn struct {
	c   net.Conn
	br  *bufio.Reader
	wmu sync.Mutex
}

// Upgrade answers a WebSocket opening handshake on w and returns the
// connection.
func Upgrade(w http.ResponseWriter, r *http.Request) (*ServerConn, error) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "not a WebSocket request", http.StatusBadRequest)
		return nil, errors.New("not a WebSocket request")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("the response cannot be hijacked")
	}
	c, brw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	res := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + AcceptKey(key) + "\r\n\r\n"
	if _, err := io.WriteString(c, res); err != nil {
		c.Close()
		return nil, err
	}
	return &ServerConn{c: c, br: brw.Reader}, nil
}

// WriteText sends one text message.
func (s *ServerConn) WriteText(p []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return writeFrame(s.c, opText, p, false)
}

// ReadMessage returns the next data message.
func (s *ServerConn) ReadMessage() ([]byte, error) {
	return readMessage(s.br, func(op byte, p []byte) error {
		s.wmu.Lock()
		defer s.wmu.Unlock()
		return writeFrame(s.c, op, p, false)
	})
}

// Close closes the connection.
func (s *ServerConn) Close() error { return s.c.Close() }
