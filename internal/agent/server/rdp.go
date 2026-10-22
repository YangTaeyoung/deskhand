package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const rdpQueryTimeout = 8 * time.Second
const maxRDPOutput = 64 << 10

func (s *Server) sessionRDPOp(ctx context.Context, _ proto.Message) (proto.Message, error) {
	ctx, cancel := context.WithTimeout(ctx, rdpQueryTimeout)
	defer cancel()
	listener, err := s.opt.RDPListener(ctx)
	if errors.Is(err, session.ErrUnsupported) {
		return nil, coded(wire.CodeCapabilityUnsupported, "session.rdp needs a Windows agent", false)
	}
	if err != nil {
		return nil, rdpError(err)
	}
	info, err := s.opt.SessionInfo()
	if err != nil {
		return nil, rdpError(err)
	}
	if info.Locked == nil {
		return nil, rdpError(fmt.Errorf("the agent session's lock state is unknown"))
	}
	r := wire.RDPSession{RDPListener: listener, SessionID: info.SessionID, User: info.User, SID: info.SID,
		Protocol: info.Protocol, ConnectState: info.ConnectState, Locked: *info.Locked}
	if err := r.Validate(); err != nil {
		return nil, rdpError(err)
	}
	r.CertificateSHA256 = strings.ToUpper(r.CertificateSHA256)
	b, err := json.Marshal(r)
	if err != nil {
		return nil, rdpError(err)
	}
	var out proto.Message
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, rdpError(err)
	}
	return out, nil
}

func rdpError(err error) error {
	return coded(wire.CodeInternal, fmt.Sprintf("RDP listener or current agent session could not be read (%v); check Remote Desktop configuration and query permissions", err), true)
}

func parseRDPListener(b []byte) (wire.RDPListener, error) {
	if len(b) > maxRDPOutput {
		return wire.RDPListener{}, fmt.Errorf("RDP listener response exceeds 64 KiB")
	}
	var fields struct {
		Enabled           *bool  `json:"enabled"`
		Port              int    `json:"port"`
		CertificateSHA256 string `json:"certificate_sha256"`
	}
	if err := json.Unmarshal(b, &fields); err != nil {
		return wire.RDPListener{}, err
	}
	if fields.Enabled == nil {
		return wire.RDPListener{}, fmt.Errorf("RDP listener response is missing enabled")
	}
	r := wire.RDPListener{Enabled: *fields.Enabled, Port: fields.Port, CertificateSHA256: fields.CertificateSHA256}
	return r, r.Validate()
}

// rdpOutput bounds stdout/stderr independently while continuing to drain
// the process. PowerShell never receives RPC arguments or credentials.
type rdpOutput struct {
	data     []byte
	overflow bool
}

func (b *rdpOutput) Write(p []byte) (int, error) {
	n := len(p)
	keep := min(n, maxRDPOutput-len(b.data))
	b.data = append(b.data, p[:keep]...)
	b.overflow = b.overflow || keep < n
	return n, nil
}
