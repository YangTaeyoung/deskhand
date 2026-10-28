package conn

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// sshPacketConn carries one QUIC flow over the relay's framed SSH streams.
// Packets may be dropped under backpressure, just as on UDP; QUIC retransmits.
type sshPacketConn struct {
	in              io.ReadCloser
	out             io.WriteCloser
	peer            *net.UDPAddr
	stop            func()
	done            chan struct{}
	received        chan []byte
	outgoing        chan []byte
	closeOnce       sync.Once
	mu              sync.Mutex
	readDeadline    time.Time
	writeDeadline   time.Time
	deadlineChanged chan struct{}
}

func newSSHPacketConn(in io.ReadCloser, out io.WriteCloser, peer *net.UDPAddr, stop func()) *sshPacketConn {
	c := &sshPacketConn{in: in, out: out, peer: peer, stop: stop, done: make(chan struct{}), received: make(chan []byte, 256), outgoing: make(chan []byte, 256), deadlineChanged: make(chan struct{})}
	go c.readLoop()
	go c.writeLoop()
	return c
}

func (c *sshPacketConn) readLoop() {
	defer c.Close()
	r := bufio.NewReaderSize(c.in, 64<<10)
	for {
		var hdr [6]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(hdr[4:]))
		id := binary.BigEndian.Uint32(hdr[:4])
		b := make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			return
		}
		if id != 1 || n == 0 {
			continue
		}
		select {
		case c.received <- b:
		case <-c.done:
			return
		default:
		}
	}
}
func (c *sshPacketConn) writeLoop() {
	defer c.Close()
	for {
		select {
		case <-c.done:
			return
		case b := <-c.outgoing:
			var hdr [6]byte
			binary.BigEndian.PutUint32(hdr[:4], 1)
			binary.BigEndian.PutUint16(hdr[4:], uint16(len(b)))
			if err := writeRelayBytes(c.out, hdr[:]); err != nil {
				return
			}
			if err := writeRelayBytes(c.out, b); err != nil {
				return
			}
		}
	}
}
func writeRelayBytes(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(p) {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}

func (c *sshPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		select {
		case <-c.done:
			return 0, nil, net.ErrClosed
		default:
		}
		c.mu.Lock()
		deadline, changed := c.readDeadline, c.deadlineChanged
		c.mu.Unlock()
		var timer *time.Timer
		var timeout <-chan time.Time
		if !deadline.IsZero() {
			d := time.Until(deadline)
			if d <= 0 {
				return 0, nil, os.ErrDeadlineExceeded
			}
			timer = time.NewTimer(d)
			timeout = timer.C
		}
		select {
		case <-c.done:
			if timer != nil {
				timer.Stop()
			}
			return 0, nil, net.ErrClosed
		case <-changed:
			if timer != nil {
				timer.Stop()
			}
			continue
		case <-timeout:
			return 0, nil, os.ErrDeadlineExceeded
		case p := <-c.received:
			if timer != nil {
				timer.Stop()
			}
			return copy(b, p), c.peer, nil
		}
	}
}
func (c *sshPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	select {
	case <-c.done:
		return 0, net.ErrClosed
	default:
	}
	if addr == nil || addr.String() != c.peer.String() {
		return 0, errors.New("unexpected SSH relay peer")
	}
	if len(b) > 65535 {
		return 0, errors.New("datagram exceeds relay limit")
	}
	c.mu.Lock()
	deadline := c.writeDeadline
	c.mu.Unlock()
	if !deadline.IsZero() && !time.Now().Before(deadline) {
		return 0, os.ErrDeadlineExceeded
	}
	p := append([]byte(nil), b...)
	select {
	case <-c.done:
		return 0, net.ErrClosed
	case c.outgoing <- p:
	default:
	}
	return len(b), nil
}
func (c *sshPacketConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.in.Close()
		_ = c.out.Close()
		if c.stop != nil {
			c.stop()
		}
	})
	return nil
}
func (c *sshPacketConn) LocalAddr() net.Addr { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (c *sshPacketConn) SetDeadline(t time.Time) error {
	_ = c.SetWriteDeadline(t)
	return c.SetReadDeadline(t)
}
func (c *sshPacketConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	close(c.deadlineChanged)
	c.deadlineChanged = make(chan struct{})
	c.mu.Unlock()
	return nil
}
func (c *sshPacketConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.writeDeadline = t
	c.mu.Unlock()
	return nil
}
