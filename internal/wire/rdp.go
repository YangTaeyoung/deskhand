package wire

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// RDPListener describes the configured RDP-Tcp listener and its bound
// certificate. CertificateSHA256 hashes the certificate's DER bytes.
type RDPListener struct {
	Enabled           bool   `json:"enabled"`
	Port              int    `json:"port"`
	CertificateSHA256 string `json:"certificate_sha256"`
}

func (r RDPListener) Validate() error {
	if r.Port < 1 || r.Port > 65535 {
		return fmt.Errorf("RDP listener port must be between 1 and 65535")
	}
	b, err := hex.DecodeString(r.CertificateSHA256)
	if err != nil || len(b) != 32 {
		return fmt.Errorf("RDP certificate_sha256 must be 64 hexadecimal characters without separators")
	}
	return nil
}

// RDPSession is the exact session.rdp result on both RPC boundaries.
type RDPSession struct {
	RDPListener
	SessionID    uint32 `json:"session_id"`
	User         string `json:"user"`
	SID          string `json:"sid"`
	Protocol     string `json:"protocol"`
	ConnectState string `json:"connect_state"`
	Locked       bool   `json:"locked"`
}

func (r RDPSession) Validate() error {
	if err := r.RDPListener.Validate(); err != nil {
		return err
	}
	if r.SessionID == 0 || strings.TrimSpace(r.User) == "" || !strings.HasPrefix(r.SID, "S-1-") {
		return fmt.Errorf("RDP metadata requires the current interactive agent session, user and SID")
	}
	if r.Protocol != "console" && r.Protocol != "rdp" {
		return fmt.Errorf("RDP metadata has an unknown session protocol")
	}
	switch r.ConnectState {
	case "active", "connected", "disconnected":
	default:
		return fmt.Errorf("RDP metadata has an unknown connection state")
	}
	return nil
}

// ParseRDPSession rejects incomplete metadata instead of treating absent
// booleans or identities as safe defaults. RPC envelope fields are ignored.
func ParseRDPSession(b []byte) (RDPSession, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return RDPSession{}, err
	}
	for _, k := range []string{"enabled", "port", "certificate_sha256", "session_id", "user", "sid", "protocol", "connect_state", "locked"} {
		if v, ok := fields[k]; !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return RDPSession{}, fmt.Errorf("RDP metadata is missing %s", k)
		}
	}
	var r RDPSession
	if err := json.Unmarshal(b, &r); err != nil {
		return RDPSession{}, err
	}
	if err := r.Validate(); err != nil {
		return RDPSession{}, err
	}
	r.CertificateSHA256 = strings.ToUpper(r.CertificateSHA256)
	return r, nil
}
