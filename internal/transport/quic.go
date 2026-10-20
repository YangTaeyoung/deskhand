package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
	"github.com/quic-go/quic-go"
)

// ErrPeerMismatch reports a peer certificate whose fingerprint is not the pinned one.
var ErrPeerMismatch = errors.New("peer certificate fingerprint does not match the paired one; pair the device again")

func quicConfig() *quic.Config {
	return &quic.Config{
		KeepAlivePeriod:    5 * time.Second,
		MaxIdleTimeout:     20 * time.Second,
		MaxIncomingStreams: 1024,
	}
}

func peerFingerprint(rawCerts [][]byte) (string, error) {
	if len(rawCerts) == 0 {
		return "", errors.New("peer sent no certificate")
	}
	return Fingerprint(rawCerts[0]), nil
}

// Listen accepts QUIC connections from peers whose certificate fingerprint
// allowedPeers approves. Session tickets are off, so allowedPeers runs on
// every handshake.
func Listen(addr string, id Identity, allowedPeers func(fp string) bool) (*quic.Listener, error) {
	if allowedPeers == nil {
		return nil, errors.New("listen: no allowed peers given; pass the paired controller fingerprints")
	}
	cert, err := id.TLSCertificate()
	if err != nil {
		return nil, err
	}
	tlsConf := &tls.Config{
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
	}
	ln, err := quic.ListenAddr(addr, tlsConf, quicConfig())
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	return ln, nil
}

// Dial connects to addr and keeps the connection only if the server certificate fingerprint equals expectedPeer.
func Dial(ctx context.Context, addr string, id Identity, expectedPeer string) (*quic.Conn, error) {
	cert, err := id.TLSCertificate()
	if err != nil {
		return nil, err
	}
	var mismatch error
	tlsConf := &tls.Config{
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
				mismatch = fmt.Errorf("%w (got %s)", ErrPeerMismatch, fp)
				return mismatch
			}
			return nil
		},
	}
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", addr, err)
	}
	udp, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		return nil, fmt.Errorf("open udp socket: %w", err)
	}
	tr := &quic.Transport{Conn: udp}
	conn, err := tr.Dial(ctx, udpAddr, tlsConf, quicConfig())
	if err != nil {
		tr.Close()
		udp.Close()
		if mismatch != nil {
			return nil, mismatch
		}
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	go func() {
		<-conn.Context().Done()
		tr.Close()
		udp.Close()
	}()
	return conn, nil
}
