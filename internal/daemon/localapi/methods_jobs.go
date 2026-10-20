package localapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/jobs"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/wire"
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
	if before.Kind == jobs.KindTransfer && s.d.Transfers != nil {
		return s.cancelTransferJob(ctx, sess, before)
	}
	if before.Kind == jobs.KindQA && s.d.QA != nil {
		return s.cancelQAJob(sess, before)
	}
	if before.Kind == jobs.KindPTY && before.FinishedAt != "" {
		o := outcome{status: model.StatusSucceeded, action: model.Executed()}
		return s.envelope(before.HostID, before.JobID, conn.CallInfo{}, o, nil).Map(map[string]any{"job": before}), nil
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	info := conn.CallInfo{}
	job, err := s.d.Jobs.Cancel(cctx, p.JobID, &info)
	if err != nil && before.Kind == jobs.KindPTY {
		if apierr.CodeOf(err) == wire.CodeNotFound && s.d.PTYs != nil {
			s.d.PTYs.Refresh(cctx, before.HostID)
		}
		if cur, gerr := s.d.Jobs.Get(ctx, p.JobID); gerr == nil && cur.FinishedAt != "" {
			o := outcome{status: model.StatusSucceeded, action: model.Executed()}
			return s.envelope(before.HostID, before.JobID, info, o, nil).Map(map[string]any{"job": cur}), nil
		}
	}
	if err != nil && info.RID == "" {
		return nil, err
	}
	o := outcomeOf(info, err)
	if job.JobID == "" {
		job = before
	}
	if err == nil {
		o.status = job.Status
		if job.Kind == jobs.KindPTY && job.Status != model.StatusCancelled {
			o.status = model.StatusSucceeded
		}
	}
	_, _ = s.d.Bus.Record(sess.Caller().Entry(before.HostID, model.TimelineExec, "job 취소: "+before.Summary, timelineOutcome(o.status), before.JobID))
	return s.envelope(before.HostID, before.JobID, info, o, nil).Map(map[string]any{"job": job}), nil
}

// cancelTransferJob cancels the transfer that the job j mirrors, as
// transfer.cancel does, and returns the envelope with the job.
func (s *Server) cancelTransferJob(ctx context.Context, sess *session, j model.Job) (any, error) {
	var p struct {
		TransferID string `json:"transfer_id"`
	}
	stored, err := s.d.Store.GetJob(j.JobID)
	if err != nil {
		return nil, apierr.Internal(fmt.Sprintf("job %s를 읽지 못했습니다(%v). 데몬 로그를 확인하세요", j.JobID, err))
	}
	if err := json.Unmarshal(stored.Params, &p); err != nil || p.TransferID == "" {
		return nil, apierr.Internal(fmt.Sprintf("job %s에 전송 ID가 없습니다. transfer.list로 전송을 찾아 transfer.cancel을 쓰세요", j.JobID))
	}
	if _, err := s.d.Transfers.Cancel(p.TransferID, sess.Caller()); err != nil {
		return nil, err
	}
	job, err := s.d.Jobs.Get(ctx, j.JobID)
	if err != nil {
		return nil, err
	}
	o := outcome{status: job.Status, action: model.Executed()}
	return s.envelope(job.HostID, job.JobID, conn.CallInfo{}, o, nil).Map(map[string]any{"job": job}), nil
}

// cancelQAJob cancels the run that the job j follows, as qa.cancel does.
func (s *Server) cancelQAJob(sess *session, j model.Job) (any, error) {
	var p struct {
		RunID string `json:"run_id"`
	}
	stored, err := s.d.Store.GetJob(j.JobID)
	if err != nil {
		return nil, apierr.Internal(fmt.Sprintf("job %s를 읽지 못했습니다(%v). 데몬 로그를 확인하세요", j.JobID, err))
	}
	if err := json.Unmarshal(stored.Params, &p); err != nil || p.RunID == "" {
		return nil, apierr.Internal(fmt.Sprintf("job %s에 QA 실행 ID가 없습니다. qa.list로 실행을 찾아 qa.cancel을 쓰세요", j.JobID))
	}
	return s.cancelRun(p.RunID, sess)
}
