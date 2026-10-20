package localapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/tunnel"
)

func (s *Server) registerTunnel() {
	if s.d.Tunnels == nil {
		return
	}
	s.methods["tunnel.open"] = s.tunnelOpen
	s.methods["tunnel.close"] = s.tunnelClose
	s.methods["tunnel.list"] = s.tunnelList
}

type tunnelOpenParams struct {
	HostID     string `json:"host_id"`
	RemotePort int    `json:"remote_port"`
	LocalPort  int    `json:"local_port"`
}

// tunnelOpen opens a tunnel owned by "user" from 127.0.0.1 of this Mac to a
// loopback port of the host and returns it.
func (s *Server) tunnelOpen(_ context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p tunnelOpenParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := hostRequired(p.HostID); err != nil {
		return nil, &paramError{e: apierr.As(err)}
	}
	h, row, err := s.d.Hosts.Conn(p.HostID)
	if err != nil {
		return nil, err
	}
	info, err := s.d.Tunnels.Open(h, tunnel.OpenParams{RemotePort: p.RemotePort, LocalPort: p.LocalPort, Owner: tunnel.OwnerUser})
	if err != nil {
		if apierr.CodeOf(err) == "INVALID_ARGUMENT" {
			return nil, &paramError{e: apierr.As(err)}
		}
		return nil, err
	}
	_, _ = s.d.Bus.Record(sess.Caller().Entry(row.HostID, model.TimelineExec,
		fmt.Sprintf("터널 열기: 127.0.0.1:%d -> PC 포트 %d", info.LocalPort, info.RemotePort), model.TimelineOK, ""))
	return info, nil
}

type tunnelCloseParams struct {
	TunnelID  string `json:"tunnel_id"`
	LocalPort int    `json:"local_port"`
}

// tunnelClose closes the tunnel that tunnel_id or local_port names. A tunnel
// of a debug session is closed by its session, not here.
func (s *Server) tunnelClose(_ context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p tunnelCloseParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if (p.TunnelID == "") == (p.LocalPort == 0) {
		return nil, &paramError{e: apierr.Invalid("tunnel.close에는 tunnel_id나 local_port 중 하나만 주세요")}
	}
	var info tunnel.Info
	var ok bool
	if p.TunnelID != "" {
		info, ok = s.d.Tunnels.Get(p.TunnelID)
	} else {
		info, ok = s.d.Tunnels.FindLocal(p.LocalPort)
	}
	if !ok {
		return nil, apierr.NotFound("그런 터널이 없습니다. tunnel.list로 열린 터널을 확인하세요")
	}
	if info.Owner != tunnel.OwnerUser {
		return nil, &paramError{e: apierr.Invalid(fmt.Sprintf("터널 %s는 디버그 세션 %s의 것입니다. debug.close로 세션을 닫으면 함께 닫힙니다",
			info.TunnelID, strings.TrimPrefix(info.Owner, tunnel.OwnerDebugPrefix)))}
	}
	closed, err := s.d.Tunnels.Close(info.TunnelID)
	if err != nil {
		return nil, err
	}
	_, _ = s.d.Bus.Record(sess.Caller().Entry(closed.HostID, model.TimelineExec,
		fmt.Sprintf("터널 닫기: 127.0.0.1:%d -> PC 포트 %d", closed.LocalPort, closed.RemotePort), model.TimelineOK, ""))
	return map[string]any{"closed": true, "tunnel_id": closed.TunnelID, "local_port": closed.LocalPort, "remote_port": closed.RemotePort, "host_id": closed.HostID}, nil
}

type tunnelListParams struct {
	HostID string `json:"host_id"`
}

// tunnelList lists the open tunnels, of one host when host_id is given.
func (s *Server) tunnelList(_ context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p tunnelListParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	hostID := ""
	if p.HostID != "" {
		row, err := s.d.Hosts.Resolve(p.HostID)
		if err != nil {
			return nil, err
		}
		hostID = row.HostID
	}
	return map[string]any{"tunnels": s.d.Tunnels.List(hostID)}, nil
}
