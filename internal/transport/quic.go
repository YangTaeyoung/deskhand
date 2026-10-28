package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
	"github.com/quic-go/quic-go"
)

// ErrPeerMismatch reports a peer certificate whose fingerprint is not the pinned one.
var ErrPeerMismatch = errors.New("peer certificate fingerprint does not match the paired one; pair the device again")

func quicConfig() *quic.Config {
	return &quic.Config{
		KeepAlivePeriod:    2 * time.Second,
		MaxIdleTimeout:     8 * time.Second,
		MaxIncomingStreams: 1024,
	}
}

func peerFingerprint(rawCerts [][]byte) (string, error) {
	if len(rawCerts) == 0 {
		return "", errors.New("peer sent no certificate")
	}
	return Fingerprint(rawCerts[0]), nil
}

// serverTLS returns the TLS configuration of a listener that keeps the
// peers whose certificate fingerprint allowedPeers approves. Session tickets
// are off, so allowedPeers runs on every handshake.
func serverTLS(id Identity, allowedPeers func(fp string) bool) (*tls.Config, error) {
	if allowedPeers == nil {
		return nil, errors.New("listen: no allowed peers given; pass the paired controller fingerprints")
	}
	cert, err := id.TLSCertificate()
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates:           []tls.Certificate{cert},
		ClientAuth:             tls.RequireAnyClientCert,
		InsecureSkipVerify:     true,
		NextProtos:             []string{wire.ALPN},
		MinVersion:             tls.VersionTLS13,
		SessionTicketsDisabled: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			fp, err := peerFingerprint(rawCerts)
			if err != nil {
				return err
			}
			if !allowedPeers(fp) {
				return ErrPeerMismatch
			}
			return nil
		},
	}, nil
}

// Listen accepts QUIC connections on the UDP address addr from peers whose
// certificate fingerprint allowedPeers approves. Session tickets are off,
// so allowedPeers runs on every handshake.
func Listen(addr string, id Identity, allowedPeers func(fp string) bool) (*quic.Listener, error) {
	tlsConf, err := serverTLS(id, allowedPeers)
	if err != nil {
		return nil, err
	}
	ln, err := quic.ListenAddr(addr, tlsConf, quicConfig())
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	return ln, nil
}

// ListenPacket is Listen on the packet connection conn, which the caller
// closes after the listener.
func ListenPacket(conn net.PacketConn, id Identity, allowedPeers func(fp string) bool) (*quic.Listener, error) {
	tlsConf, err := serverTLS(id, allowedPeers)
	if err != nil {
		return nil, err
	}
	return quic.Listen(conn, tlsConf, quicConfig())
}

// clientTLS returns the TLS configuration of a connection that keeps the
// server only if its certificate fingerprint equals expectedPeer; a server
// with another one stores its error in mismatch.
func clientTLS(id Identity, expectedPeer string, mismatch *error) (*tls.Config, error) {
	cert, err := id.TLSCertificate()
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
		NextProtos:         []string{wire.ALPN},
		MinVersion:         tls.VersionTLS13,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			fp, err := peerFingerprint(rawCerts)
			if err != nil {
				return err
			}
			if fp != expectedPeer {
				*mismatch = fmt.Errorf("%w (got %s)", ErrPeerMismatch, fp)
				return *mismatch
			}
			return nil
		},
	}, nil
}

// recordingUDP preserves a local routing error that quic-go otherwise converts
// into an unwrappable INTERNAL_ERROR string. Embedding UDPConn retains its
// out-of-band packet and syscall capabilities.
type recordingUDP struct {
	*net.UDPConn
	mu          sync.Mutex
	unreachable error
}

func (c *recordingUDP) record(err error) {
	if errors.Is(err, syscall.EHOSTUNREACH) {
		c.mu.Lock()
		c.unreachable = err
		c.mu.Unlock()
	}
}
func (c *recordingUDP) WriteTo(b []byte, addr net.Addr) (int, error) {
	n, err := c.UDPConn.WriteTo(b, addr)
	c.record(err)
	return n, err
}
func (c *recordingUDP) WriteMsgUDP(b, oob []byte, addr *net.UDPAddr) (int, int, error) {
	n, on, err := c.UDPConn.WriteMsgUDP(b, oob, addr)
	c.record(err)
	return n, on, err
}
func (c *recordingUDP) routeError() error { c.mu.Lock(); defer c.mu.Unlock(); return c.unreachable }

func restoreLocalRouteError(err, routeErr error) error {
	var te *quic.TransportError
	if routeErr != nil && errors.As(err, &te) && !te.Remote && te.ErrorCode == quic.InternalError {
		return errors.Join(err, routeErr)
	}
	return err
}

// Dial connects to addr and keeps the connection only if the server certificate fingerprint equals expectedPeer.
func Dial(ctx context.Context, addr string, id Identity, expectedPeer string) (*quic.Conn, error) {
	var mismatch error
	tlsConf, err := clientTLS(id, expectedPeer, &mismatch)
	if err != nil {
		return nil, err
	}
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", addr, err)
	}
	udp, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		return nil, fmt.Errorf("open udp socket: %w", err)
	}
	recorded := &recordingUDP{UDPConn: udp}
	tr := &quic.Transport{Conn: recorded}
	conn, err := tr.Dial(ctx, udpAddr, tlsConf, quicConfig())
	if err != nil {
		tr.Close()
		udp.Close()
		if mismatch != nil {
			return nil, mismatch
		}
		err = restoreLocalRouteError(err, recorded.routeError())
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	go func() {
		<-conn.Context().Done()
		tr.Close()
		udp.Close()
	}()
	return conn, nil
}

// DialPacket is Dial over the packet connection conn, which the caller
// closes after the connection.
func DialPacket(ctx context.Context, conn net.PacketConn, addr string, id Identity, expectedPeer string) (*quic.Conn, error) {
	var mismatch error
	tlsConf, err := clientTLS(id, expectedPeer, &mismatch)
	if err != nil {
		return nil, err
	}
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", addr, err)
	}
	c, err := quic.Dial(ctx, conn, udpAddr, tlsConf, quicConfig())
	if err != nil {
		if mismatch != nil {
			return nil, mismatch
		}
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return c, nil
}
