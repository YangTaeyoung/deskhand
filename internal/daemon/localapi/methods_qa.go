package localapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/artifacts"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/qa"
)

func (s *Server) registerQA() {
	s.methods["artifacts.list"] = s.artifactsList
	s.methods["artifacts.export"] = s.artifactsExport
	if s.d.QA == nil {
		return
	}
	s.methods["qa.run"] = s.qaRun
	s.methods["qa.get"] = s.qaGet
	s.methods["qa.list"] = s.qaList
	s.methods["qa.cancel"] = s.qaCancel
}

type qaParams struct {
	HostID       string                     `json:"host_id"`
	ScenarioPath string                     `json:"scenario_path"`
	Scenario     *string                    `json:"scenario"`
	BaseDir      string                     `json:"base_dir"`
	Vars         map[string]json.RawMessage `json:"vars"`
	TimeoutMS    int64                      `json:"timeout_ms"`
	RunID        string                     `json:"run_id"`
	Limit        int                        `json:"limit"`
	Cursor       string                     `json:"cursor"`
}

// vars returns the vars of qa.run as text: strings, numbers and booleans.
func (p qaParams) vars() (map[string]string, error) {
	out := map[string]string{}
	for k, raw := range p.Vars {
		raw = bytes.TrimSpace(raw)
		var str string
		if json.Unmarshal(raw, &str) == nil {
			out[k] = str
			continue
		}
		var num json.Number
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if dec.Decode(&num) == nil {
			out[k] = num.String()
			continue
		}
		var b bool
		if json.Unmarshal(raw, &b) == nil {
			out[k] = strconv.FormatBool(b)
			continue
		}
		return nil, apierr.Invalid(fmt.Sprintf("vars의 %s는 글자·숫자·불이어야 합니다. 값을 고쳐 다시 보내세요", k))
	}
	return out, nil
}

// qaRun starts a run and returns its envelope with run_id and the run.
func (s *Server) qaRun(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p qaParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	vars, err := p.vars()
	if err != nil {
		return nil, err
	}
	sp := qa.StartParams{HostRef: p.HostID, Path: p.ScenarioPath, BaseDir: p.BaseDir, Vars: vars, TimeoutMS: p.TimeoutMS}
	if p.Scenario != nil {
		if *p.Scenario == "" {
			return nil, apierr.Invalid("scenario가 비어 있습니다. 시나리오 YAML 글을 주세요")
		}
		sp.Text = []byte(*p.Scenario)
	}
	run, err := s.d.QA.Start(ctx, sp, sess.Caller())
	if err != nil {
		return nil, err
	}
	o := outcome{status: model.StatusRunning, action: model.Executed()}
	return s.envelope(run.HostID, run.JobID, conn.CallInfo{}, o, nil).Map(map[string]any{"run_id": run.RunID, "run": run}), nil
}

func (s *Server) qaGet(_ context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p qaParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if p.RunID == "" {
		return nil, required("run_id", "qa.get")
	}
	return s.d.QA.Get(p.RunID)
}

func (s *Server) qaList(_ context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p qaParams
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
	runs, next, err := s.d.QA.List(hostID, p.Limit, p.Cursor)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"runs": runs}
	if next != "" {
		out["next_cursor"] = next
	}
	return out, nil
}

func (s *Server) qaCancel(_ context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p qaParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if p.RunID == "" {
		return nil, required("run_id", "qa.cancel")
	}
	return s.cancelRun(p.RunID, sess)
}

// cancelRun cancels a run and returns the envelope with the run: running
// while its teardown goes on, cancelled once it has ended.
func (s *Server) cancelRun(runID string, sess *session) (any, error) {
	run, err := s.d.QA.Cancel(runID, sess.Caller())
	if err != nil {
		return nil, err
	}
	status := model.StatusRunning
	if run.FinishedAt != "" {
		status = model.StatusCancelled
	}
	o := outcome{status: status, action: model.Executed()}
	fields := map[string]any{"run_id": run.RunID, "run": run}
	if job, err := s.d.Jobs.Get(context.Background(), run.JobID); err == nil {
		fields["job"] = job
	}
	return s.envelope(run.HostID, run.JobID, conn.CallInfo{}, o, nil).Map(fields), nil
}

type artifactsParams struct {
	JobID string `json:"job_id"`
	Dir   string `json:"dir"`
}

func (s *Server) artifactJob(ctx context.Context, raw json.RawMessage, method string) (model.Job, artifactsParams, error) {
	var p artifactsParams
	if err := decode(raw, &p); err != nil {
		return model.Job{}, p, err
	}
	if p.JobID == "" {
		return model.Job{}, p, required("job_id", method)
	}
	job, err := s.d.Jobs.Get(ctx, p.JobID)
	return job, p, err
}

func (s *Server) artifactsList(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	job, _, err := s.artifactJob(ctx, raw, "artifacts.list")
	if err != nil {
		return nil, err
	}
	dir, list, err := artifacts.List(job)
	if err != nil {
		return nil, err
	}
	return map[string]any{"job_id": job.JobID, "dir": dir, "artifacts": list}, nil
}

func (s *Server) artifactsExport(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	job, p, err := s.artifactJob(ctx, raw, "artifacts.export")
	if err != nil {
		return nil, err
	}
	return artifacts.Export(job, p.Dir)
}
