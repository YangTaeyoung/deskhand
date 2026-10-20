package conn

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"syscall"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/transport"
	"github.com/quic-go/quic-go"
)

// Target is where a host's agent listens and the certificate fingerprint it
// must present.
type Target struct {
	HostID      string
	Name        string
	Address     string
	Port        int
	Fingerprint string
}

// Addr returns address:port.
func (t Target) Addr() string { return net.JoinHostPort(t.Address, strconv.Itoa(t.Port)) }

// Stream is a bidirectional byte stream of a Session.
type Stream interface {
	io.ReadWriteCloser
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
}

// Session is one connection to an agent.
type Session interface {
	// OpenStream opens a bidirectional stream; the peer sees it once data
	// is written.
	OpenStream(ctx context.Context) (Stream, error)
	// Close closes the connection with reason.
	Close(reason string) error
	// Err returns why the connection ended, or nil while it is open.
	Err() error
}

// Dialer connects to the agent of a target with the controller identity id.
type Dialer func(ctx context.Context, t Target, id transport.Identity) (Session, error)

// DialQUIC connects with transport.Dial and pins t.Fingerprint.
func DialQUIC(ctx context.Context, t Target, id transport.Identity) (Session, error) {
	c, err := transport.Dial(ctx, t.Addr(), id, t.Fingerprint)
	if err != nil {
		return nil, err
	}
	return quicSession{c}, nil
}

type quicSession struct{ c *quic.Conn }

func (s quicSession) OpenStream(ctx context.Context) (Stream, error) {
	st, err := s.c.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	return quicStream{st}, nil
}

func (s quicSession) Close(reason string) error { return s.c.CloseWithError(0, reason) }

func (s quicSession) Err() error {
	select {
	case <-s.c.Context().Done():
		return context.Cause(s.c.Context())
	default:
		return nil
	}
}

// quicStream closes both directions of a stream.
type quicStream struct{ *quic.Stream }

func (s quicStream) Close() error {
	s.CancelRead(0)
	return s.Stream.Close()
}

// rejected reports whether err, or the end of sess, is a TLS alert the agent
// sent: the agent does not accept this controller's certificate.
func rejected(err error, sess Session) bool {
	for _, e := range []error{err, sessErr(sess)} {
		var te *quic.TransportError
		if errors.As(e, &te) && te.Remote && te.ErrorCode.IsCryptoError() {
			return true
		}
	}
	return false
}

// noAnswer reports whether a dial error means that nothing answered: the
// QUIC handshake or the dial ran out of time, or the port refused.
func noAnswer(err error) bool {
	var idle *quic.IdleTimeoutError
	var handshake *quic.HandshakeTimeoutError
	return errors.As(err, &idle) || errors.As(err, &handshake) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, syscall.ECONNREFUSED)
}

func sessErr(sess Session) error {
	if sess == nil {
		return nil
	}
	return sess.Err()
}
