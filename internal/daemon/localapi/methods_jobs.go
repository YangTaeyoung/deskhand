package localapi

import (
	"context"
	"encoding/json"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
)

func (s *Server) registerJobs() {
	s.methods["jobs.list"] = s.jobsList
	s.methods["jobs.get"] = s.jobsGet
	s.methods["jobs.output"] = s.jobsOutput
	s.methods["jobs.cancel"] = s.jobsCancel
}

type jobsListParams struct {
	HostID string `json:"host_id"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Limit  int    `json:"limit"`
}

func (s *Server) jobsList(_ context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p jobsListParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	hostID := p.HostID
	if hostID != "" {
		if row, err := s.d.Hosts.Resolve(hostID); err == nil {
			hostID = row.HostID
		}
	}
	list, err := s.d.Jobs.List(store.JobFilter{HostID: hostID, Kind: p.Kind, Status: p.Status, Limit: p.Limit})
	if err != nil {
		return nil, apierr.Internal("job 목록을 읽지 못했습니다. 데몬 로그를 확인하세요")
	}
	return map[string]any{"jobs": list}, nil
}

type jobRef struct {
	JobID  string `json:"job_id"`
	Cursor string `json:"cursor"`
	Max    int    `json:"max"`
}

func (p jobRef) check(method string) error {
	if p.JobID == "" {
		return required("job_id", method)
	}
	return nil
}

func (s *Server) jobsGet(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p jobRef
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.check("jobs.get"); err != nil {
		return nil, err
	}
	return s.d.Jobs.Get(ctx, p.JobID)
}

func (s *Server) jobsOutput(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p jobRef
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.check("jobs.output"); err != nil {
		return nil, err
	}
	return s.d.Jobs.Output(ctx, p.JobID, p.Cursor, p.Max)
}

func (s *Server) jobsCancel(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p jobRef
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.check("jobs.cancel"); err != nil {
		return nil, err
	}
	before, err := s.d.Jobs.Get(ctx, p.JobID)
	if err != nil {
		return nil, err
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	info := conn.CallInfo{}
	job, err := s.d.Jobs.Cancel(cctx, p.JobID, &info)
	if err != nil && info.RID == "" {
		return nil, err
	}
	o := outcomeOf(info, err)
	if job.JobID == "" {
		job = before
	}
	if err == nil {
		o.status = job.Status
	}
	_, _ = s.d.Bus.Record(sess.Caller().Entry(before.HostID, model.TimelineExec, "job 취소: "+before.Summary, timelineOutcome(o.status), before.JobID))
	return s.envelope(before.HostID, before.JobID, info, o, nil).Map(map[string]any{"job": job}), nil
}
