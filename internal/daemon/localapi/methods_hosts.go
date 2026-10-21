package localapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/hosts"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// installTimeout bounds host.add and host.repair.
const installTimeout = 10 * time.Minute

func (s *Server) registerHosts() {
	s.methods["host.list"] = s.hostList
	s.methods["host.add"] = s.hostAdd
	s.methods["host.update"] = s.hostUpdate
	s.methods["host.remove"] = s.hostRemove
	s.methods["host.connect"] = s.hostConnect
	s.methods["host.disconnect"] = s.hostDisconnect
	s.methods["host.doctor"] = s.hostDoctor
	s.methods["host.repair"] = s.hostRepair
	s.methods["session.inspect"] = s.sessionInspect
}

type hostRef struct {
	HostID string `json:"host_id"`
}

func (p hostRef) check() error { return hostRequired(p.HostID) }

func (s *Server) hostList(context.Context, *session, json.RawMessage) (any, error) {
	list, err := s.d.Hosts.List()
	if err != nil {
		return nil, err
	}
	return map[string]any{"hosts": list}, nil
}

type addParams struct {
	Name    string `json:"name"`
	SSH     string `json:"ssh"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	Force   bool   `json:"force"`
}

// installEnvelope is the result of host.add and host.repair: an envelope
// with the host and the install job, or the error of a request that never
// started a job.
func (s *Server) installEnvelope(h model.Host, job model.Job, hostID string, err error) (any, error) {
	if err != nil && job.JobID == "" {
		return nil, err
	}
	o := outcomeOf(conn.CallInfo{}, err)
	if err != nil {
		o = outcome{status: model.StatusFailed, action: model.NotStarted(), err: o.err}
	}
	if h.HostID != "" {
		hostID = h.HostID
	}
	env := s.envelope(hostID, job.JobID, conn.CallInfo{}, o, nil)
	fields := map[string]any{"job": job}
	if h.HostID != "" {
		fields["host"] = h
	}
	return env.Map(fields), nil
}

func (s *Server) hostAdd(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p addParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	actx, cancel := s.detached(installTimeout)
	defer cancel()
	h, job, err := s.d.Hosts.Add(actx, hosts.AddParams{Name: p.Name, SSH: p.SSH, Address: p.Address, Port: p.Port, Force: p.Force}, sess.Caller())
	return s.installEnvelope(h, job, job.HostID, err)
}

func (s *Server) hostRepair(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p hostRef
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.check(); err != nil {
		return nil, err
	}
	actx, cancel := s.detached(installTimeout)
	defer cancel()
	h, job, err := s.d.Hosts.Repair(actx, p.HostID, sess.Caller())
	return s.installEnvelope(h, job, job.HostID, err)
}

type removeParams struct {
	HostID    string `json:"host_id"`
	Uninstall bool   `json:"uninstall"`
}

func (s *Server) hostRemove(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p removeParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := hostRequired(p.HostID); err != nil {
		return nil, err
	}
	row, err := s.d.Hosts.Resolve(p.HostID)
	if err != nil {
		return nil, err
	}
	actx, cancel := s.detached(remoteTimeout)
	defer cancel()
	if err := s.d.Hosts.Remove(actx, row.HostID, p.Uninstall, sess.Caller()); err != nil {
		return nil, err
	}
	if s.d.Tunnels != nil {
		s.d.Tunnels.CloseHost(row.HostID)
	}
	return map[string]any{"host_id": row.HostID, "removed": true, "uninstalled": p.Uninstall}, nil
}

func (s *Server) hostConnect(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p hostRef
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.check(); err != nil {
		return nil, err
	}
	return s.d.Hosts.Connect(ctx, p.HostID)
}

func (s *Server) hostDisconnect(_ context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p hostRef
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.check(); err != nil {
		return nil, err
	}
	return s.d.Hosts.Disconnect(p.HostID)
}

func (s *Server) hostDoctor(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p hostRef
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.check(); err != nil {
		return nil, err
	}
	row, err := s.d.Hosts.Resolve(p.HostID)
	if err != nil {
		return nil, err
	}
	checks, err := s.d.Hosts.Doctor(ctx, row.HostID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"host_id": row.HostID, "checks": checks}, nil
}

func (s *Server) sessionInspect(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p hostRef
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.check(); err != nil {
		return nil, err
	}
	return s.d.Hosts.Inspect(ctx, p.HostID)
}

type updateParams struct {
	HostID   string               `json:"host_id"`
	Name     *string              `json:"name"`
	Address  *string              `json:"address"`
	Port     *int                 `json:"port"`
	SSHAlias *string              `json:"ssh_alias"`
	SSH      *string              `json:"ssh"`
	AIScope  *model.AIScopeUpdate `json:"ai_scope"`
}

func (s *Server) hostUpdate(_ context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p updateParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := hostRequired(p.HostID); err != nil {
		return nil, &paramError{e: apierr.As(err)}
	}
	if sess.Caller().Actor == model.ActorAI {
		return nil, apierr.New(wire.CodePermissionDenied,
			"AI 클라이언트는 PC 설정과 AI 범위를 바꿀 수 없습니다. Deskhand 앱이나 deskhand CLI에서 사람이 바꿔야 합니다", false, "")
	}
	if p.SSH != nil && (p.SSHAlias != nil && *p.SSHAlias != *p.SSH) {
		return nil, &paramError{e: apierr.Invalid("ssh와 ssh_alias가 서로 다릅니다. 하나만 주세요")}
	}
	if p.SSHAlias == nil {
		p.SSHAlias = p.SSH
	}
	h, err := s.d.Hosts.Update(p.HostID, hosts.UpdateParams{Name: p.Name, Address: p.Address, Port: p.Port,
		SSHAlias: p.SSHAlias, AIScope: p.AIScope}, sess.Caller())
	if err != nil {
		if apierr.CodeOf(err) == wire.CodeInvalidArgument {
			return nil, &paramError{e: apierr.As(err)}
		}
		return nil, err
	}
	if p.AIScope != nil && !h.AIScope.Screen && s.d.Screens != nil {
		s.d.Screens.CloseAIViewers(h.HostID)
	}
	return h, nil
}
