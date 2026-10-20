// Package transport provides self-signed Ed25519 identities and QUIC mutual
// TLS that accepts only pinned peer certificate fingerprints.
package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"
)

// Identity is a certificate and private key in PEM form plus the certificate fingerprint.
type Identity struct {
	CertPEM     []byte
	KeyPEM      []byte
	Fingerprint string
}

// Fingerprint returns the lowercase hex SHA-256 of a DER certificate.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// NewIdentity creates an Ed25519 self-signed certificate valid for 20 years.
func NewIdentity(commonName string) (Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, fmt.Errorf("generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return Identity{}, fmt.Errorf("serial number: %w", err)
	}
	start := time.Now().Add(-time.Hour)
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    start,
		NotAfter:     start.AddDate(20, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return Identity{}, fmt.Errorf("create certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return Identity{}, fmt.Errorf("marshal key: %w", err)
	}
	return Identity{
		CertPEM:     pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:      pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
		Fingerprint: Fingerprint(der),
	}, nil
}

// LoadIdentity reads a certificate and key written by Save.
func LoadIdentity(certPath, keyPath string) (Identity, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return Identity{}, fmt.Errorf("read certificate: %w", err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return Identity{}, fmt.Errorf("read key: %w", err)
	}
	id := Identity{CertPEM: certPEM, KeyPEM: keyPEM}
	cert, err := id.TLSCertificate()
	if err != nil {
		return Identity{}, err
	}
	id.Fingerprint = Fingerprint(cert.Certificate[0])
	return id, nil
}

// TLSCertificate parses the PEM pair into a tls.Certificate.
func (id Identity) TLSCertificate() (tls.Certificate, error) {
	cert, err := tls.X509KeyPair(id.CertPEM, id.KeyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("invalid identity: %w", err)
	}
	if len(cert.Certificate) == 0 {
		return tls.Certificate{}, errors.New("invalid identity: no certificate")
	}
	return cert, nil
}

// Save writes the certificate (0644) and key (0600).
func (id Identity) Save(certPath, keyPath string) error {
	if err := writeFile(keyPath, id.KeyPEM, 0o600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	if err := writeFile(certPath, id.CertPEM, 0o644); err != nil {
		return fmt.Errorf("write certificate: %w", err)
	}
	return nil
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, data, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}
