package localapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// sessionRDP forwards a read to the selected paired agent. Neither address,
// session identity nor certificate can be supplied by the local caller.
func (s *Server) sessionRDP(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p hostRef
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.check(); err != nil {
		return nil, &paramError{e: apierr.As(err)}
	}
	h, _, err := s.d.Hosts.Conn(p.HostID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	info := conn.CallInfo{}
	res, err := h.Call(ctx, "session.rdp", proto.Message{}, conn.CallOpts{Info: &info})
	if err != nil {
		return nil, err
	}
	if info.Generation != h.Generation() {
		return nil, apierr.New(wire.CodeStaleTarget, "에이전트 연결이 바뀌었습니다. session.rdp를 다시 조회하세요", true, apierr.NextRetry)
	}
	b, err := json.Marshal(res)
	if err != nil {
		return nil, apierr.Internal("에이전트 RDP 응답을 읽지 못했습니다")
	}
	r, err := wire.ParseRDPSession(b)
	if err != nil {
		return nil, apierr.Internal("에이전트 RDP 응답이 올바르지 않습니다: " + err.Error())
	}
	return r, nil
}
