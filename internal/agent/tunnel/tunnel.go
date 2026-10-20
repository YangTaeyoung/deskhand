// Package tunnel serves the tunnel streams of the agent: each stream is a
// TCP connection to a loopback port of this PC, carried as raw bytes
// (docs/protocol.md §2).
package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// dialTimeout bounds the connect to one loopback address.
	dialTimeout = 5 * time.Second
	// lineTimeout bounds the write of the acceptance or refusal line.
	lineTimeout = 10 * time.Second
	// copyBuffer is the size of the buffer of one copy direction.
	copyBuffer = 64 << 10
)

// loopbacks are the addresses a tunnel connects to, in the order tried.
var loopbacks = []string{"127.0.0.1", "::1"}

// Stream is a tunnel stream.
type Stream interface {
	io.Reader
	io.Writer
	SetWriteDeadline(t time.Time) error
	// Close ends the agent's direction of the stream.
	Close() error
	// CancelRead asks the daemon to stop writing to the stream.
	CancelRead()
	// CancelWrite resets the agent's direction of the stream.
	CancelWrite()
}

// refusalError is a refusal line's code, text and retryable.
type refusalError struct {
	code      string
	text      string
	retryable bool
}

func (e *refusalError) Error() string { return e.text }

// Serve serves the tunnel stream st with header h: it connects to the
// loopback port h.Port, writes {"ok":true} and carries the bytes of both
// directions until both end. A header that names a host other than the
// loopback literals 127.0.0.1 and ::1, a port outside 1-65535 and a port
// nothing listens on get a refusal line instead. Serve owns st and closes it.
func Serve(ctx context.Context, st Stream, h wire.StreamHeader) {
	defer st.Close()
	tc, err := dial(ctx, h)
	if err != nil {
		refuse(st, err)
		return
	}
	defer tc.Close()
	if err := writeLine(st, map[string]any{"ok": true}); err != nil {
		st.CancelWrite()
		st.CancelRead()
		return
	}
	stop := context.AfterFunc(ctx, func() {
		tc.Close()
		st.CancelRead()
		st.CancelWrite()
	})
	defer stop()
	relay(st, tc)
}

// relay copies between the stream and the connection until both directions
// end; a failed direction resets both.
func relay(st Stream, tc *net.TCPConn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := io.CopyBuffer(tc, readerOnly{st}, make([]byte, copyBuffer))
		if err != nil {
			tc.Close()
			st.CancelRead()
			return
		}
		_ = tc.CloseWrite()
	}()
	go func() {
		defer wg.Done()
		_, err := io.CopyBuffer(streamWriter{st}, readerOnly{tc}, make([]byte, copyBuffer))
		if err != nil {
			st.CancelWrite()
			tc.Close()
			return
		}
		_ = st.Close()
	}()
	wg.Wait()
}

// readerOnly exposes only the Read method of r.
type readerOnly struct{ r io.Reader }

func (r readerOnly) Read(p []byte) (int, error) { return r.r.Read(p) }

// streamWriter hides every method of the stream but Write.
type streamWriter struct{ st Stream }

func (w streamWriter) Write(p []byte) (int, error) { return w.st.Write(p) }

// dial checks h and connects to its loopback port.
func dial(ctx context.Context, h wire.StreamHeader) (*net.TCPConn, error) {
	addrs, err := loopbackAddrs(h)
	if err != nil {
		return nil, err
	}
	var last error
	for _, addr := range addrs {
		d := net.Dialer{Timeout: dialTimeout}
		c, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			return c.(*net.TCPConn), nil
		}
		last = err
	}
	if errors.Is(last, syscall.ECONNREFUSED) || errors.Is(last, context.Canceled) {
		return nil, &refusalError{wire.CodeNotFound, fmt.Sprintf("nothing listens on port %d of this PC's loopback; start the program there and retry", h.Port), true}
	}
	var ne net.Error
	if errors.As(last, &ne) && ne.Timeout() {
		return nil, &refusalError{wire.CodeTimeout, fmt.Sprintf("port %d of this PC's loopback did not accept the connection within %s; check the program that owns it", h.Port, dialTimeout), true}
	}
	return nil, &refusalError{wire.CodeNotFound, fmt.Sprintf("cannot connect to port %d of this PC's loopback (%v); start the program there and retry", h.Port, last), true}
}

// loopbackAddrs returns the addresses to connect to for h, or the refusal
// of a header that does not name a loopback port.
func loopbackAddrs(h wire.StreamHeader) ([]string, error) {
	if h.Port < 1 || h.Port > 65535 {
		return nil, &refusalError{wire.CodeInvalidArgument, fmt.Sprintf("tunnel port %d is not between 1 and 65535; pass the port of a program on this PC", h.Port), false}
	}
	port := strconv.Itoa(h.Port)
	switch h.Host {
	case "":
		out := make([]string, len(loopbacks))
		for i, ip := range loopbacks {
			out[i] = net.JoinHostPort(ip, port)
		}
		return out, nil
	case "127.0.0.1", "::1":
		return []string{net.JoinHostPort(h.Host, port)}, nil
	}
	text := h.Host
	if len(text) > 64 {
		text = text[:64]
	}
	return nil, &refusalError{wire.CodePermissionDenied, fmt.Sprintf("tunnels reach only this PC's loopback (127.0.0.1 or ::1), not '%s'; leave host out or use a loopback address", text), false}
}

// refuse writes the refusal line of err and ends the stream.
func refuse(st Stream, err error) {
	code, retryable := wire.CodeInternal, false
	var re *refusalError
	if errors.As(err, &re) {
		code, retryable = re.code, re.retryable
	}
	_ = writeLine(st, map[string]any{"ok": false, "code": code, "error": err.Error(), "retryable": retryable})
	st.CancelRead()
}

// writeLine writes m as one JSON line within lineTimeout.
func writeLine(st Stream, m map[string]any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_ = st.SetWriteDeadline(time.Now().Add(lineTimeout))
	_, err = st.Write(append(b, '\n'))
	_ = st.SetWriteDeadline(time.Time{})
	return err
}

// FreePorts returns n distinct loopback TCP ports that are free at the
// time of the call: it binds them all, then releases them.
func FreePorts(n int) ([]int, error) {
	var held []net.Listener
	defer func() {
		for _, l := range held {
			l.Close()
		}
	}()
	ports := make([]int, 0, n)
	for len(ports) < n {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		held = append(held, l)
		ports = append(ports, l.Addr().(*net.TCPAddr).Port)
	}
	return ports, nil
}
