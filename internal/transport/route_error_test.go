package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

type unreachablePacket struct {
	once sync.Once
	done chan struct{}
}

func (c *unreachablePacket) ReadFrom([]byte) (int, net.Addr, error) {
	<-c.done
	return 0, nil, net.ErrClosed
}
func (c *unreachablePacket) WriteTo([]byte, net.Addr) (int, error) {
	time.AfterFunc(100*time.Millisecond, func() { _ = c.Close() })
	return 0, syscall.EHOSTUNREACH
}
func (c *unreachablePacket) Close() error                     { c.once.Do(func() { close(c.done) }); return nil }
func (c *unreachablePacket) LocalAddr() net.Addr              { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (c *unreachablePacket) SetDeadline(time.Time) error      { return nil }
func (c *unreachablePacket) SetReadDeadline(time.Time) error  { return nil }
func (c *unreachablePacket) SetWriteDeadline(time.Time) error { return nil }

func TestRestoreQUICLocalRoutingError(t *testing.T) {
	p := &unreachablePacket{done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := quic.Dial(ctx, p, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}, &tls.Config{InsecureSkipVerify: true}, nil)
	_ = p.Close()
	if err == nil {
		t.Fatal("expected failure")
	}
	var te *quic.TransportError
	if !errors.As(err, &te) || te.Remote || te.ErrorCode != quic.InternalError {
		t.Fatalf("unexpected error: %v", err)
	}
	if errors.Is(err, syscall.EHOSTUNREACH) {
		t.Fatal("expected quic-go to lose errno")
	}
	if !errors.Is(restoreLocalRouteError(err, syscall.EHOSTUNREACH), syscall.EHOSTUNREACH) {
		t.Fatal("routing errno was not restored")
	}
	remote := &quic.TransportError{Remote: true, ErrorCode: quic.InternalError}
	if errors.Is(restoreLocalRouteError(remote, syscall.EHOSTUNREACH), syscall.EHOSTUNREACH) {
		t.Fatal("remote errors must not trigger local fallback")
	}
}
