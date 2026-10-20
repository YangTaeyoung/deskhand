package localapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/jobs"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func (s *Server) registerDiag() {
	s.methods["diag.system"] = s.diagSystem
	s.methods["diag.services"] = s.diagServices
	s.methods["diag.events"] = s.diagEvents
	s.methods["diag.crashes"] = s.diagCrashes
}

// diagParams are the parameters of the diag methods.
type diagParams struct {
	HostID   string   `json:"host_id"`
	Save     bool     `json:"save"`
	Name     string   `json:"name"`
	Log      string   `json:"log"`
	Since    string   `json:"since"`
	Until    string   `json:"until"`
	Levels   []string `json:"levels"`
	Provider string   `json:"provider"`
	Max      *int     `json:"max"`
}

// diagMethod describes one diag method: its agent op, the result fields it
// copies, the artifact name and job summary of a saved result.
type diagMethod struct {
	op      string
	fields  []string
	name    string
	summary string
}

var (
	diagSystemMethod   = diagMethod{"diag.system", []string{"os", "computer_name", "cpu", "memory", "disks", "gpus", "boot_time", "uptime_s"}, "diag-system.json", "시스템 정보 저장"}
	diagServicesMethod = diagMethod{"diag.services", []string{"services"}, "diag-services.json", "서비스 목록 저장"}
	diagEventsMethod   = diagMethod{"diag.events", []string{"events", "truncated"}, "diag-events.json", "이벤트 로그 저장"}
	diagCrashesMethod  = diagMethod{"diag.crashes", []string{"dumps"}, "diag-crashes.json", "크래시 목록 저장"}
)

func (s *Server) diagSystem(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	p, err := diagDecode(raw)
	if err != nil {
		return nil, err
	}
	return s.diagCall(ctx, p, diagSystemMethod, proto.Message{})
}

func (s *Server) diagServices(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	p, err := diagDecode(raw)
	if err != nil {
		return nil, err
	}
	params := proto.Message{}
	if p.Name != "" {
		params["name"] = p.Name
	}
	return s.diagCall(ctx, p, diagServicesMethod, params)
}

func (s *Server) diagEvents(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	p, err := diagDecode(raw)
	if err != nil {
		return nil, err
	}
	q, fault := wire.DiagEventsParams{Log: p.Log, Since: p.Since, Until: p.Until, Levels: p.Levels, Provider: p.Provider, Max: p.Max}.Check(time.Now())
	if fault != nil {
		return nil, diagEventsFault(fault)
	}
	params := proto.Message{"log": q.Log, "max": q.Max}
	for k, v := range map[string]string{"since": p.Since, "until": p.Until, "provider": p.Provider} {
		if v != "" {
			params[k] = v
		}
	}
	if len(q.Levels) > 0 {
		params["levels"] = q.Levels
	}
	m := diagEventsMethod
	m.summary += " (" + q.Log + ")"
	return s.diagCall(ctx, p, m, params)
}

func (s *Server) diagCrashes(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	p, err := diagDecode(raw)
	if err != nil {
		return nil, err
	}
	params := proto.Message{}
	if p.Since != "" {
		if _, err := wire.ParseDiagTime(p.Since); err != nil {
			return nil, apierr.Invalid(fmt.Sprintf("since %q는 RFC 3339 시각이 아닙니다. 2026-10-20T00:00:00Z처럼 주세요", p.Since))
		}
		params["since"] = p.Since
	}
	return s.diagCall(ctx, p, diagCrashesMethod, params)
}

// diagDecode reads the params of a diag method, which need host_id.
func diagDecode(raw json.RawMessage) (diagParams, error) {
	var p diagParams
	if err := decode(raw, &p); err != nil {
		return p, err
	}
	return p, hostRequired(p.HostID)
}

// diagEventsFault is the params error of a diag.events fault.
func diagEventsFault(f *wire.DiagEventsFault) error {
	switch f.Field {
	case "log":
		return apierr.Invalid(fmt.Sprintf("log %q는 읽을 수 없는 로그입니다. %s 중 하나를 주세요", f.Value, strings.Join(wire.DiagEventLogs, ", ")))
	case "since":
		return apierr.Invalid(fmt.Sprintf("since %q는 RFC 3339 시각이 아닙니다. 2026-10-20T00:00:00Z처럼 주세요", f.Value))
	case "until":
		return apierr.Invalid(fmt.Sprintf("until %q는 since 이후의 RFC 3339 시각이어야 합니다. 2026-10-21T00:00:00Z처럼 주세요", f.Value))
	case "levels":
		return apierr.Invalid(fmt.Sprintf("levels의 %q는 알 수 없는 수준입니다. %s 중에서 주세요", f.Value, strings.Join(wire.DiagEventLevels, ", ")))
	}
	return apierr.Invalid(fmt.Sprintf("max는 1부터 %d까지의 정수여야 합니다. 값을 고쳐 다시 보내세요", wire.DiagEventsMaxLimit))
}

// diagCall sends the agent op of m and returns the envelope with m's result
// fields. With save, a successful result is kept as the JSON artifact of a
// new diag job, named in artifact_id.
func (s *Server) diagCall(ctx context.Context, p diagParams, m diagMethod, params proto.Message) (any, error) {
	h, row, err := s.d.Hosts.Conn(p.HostID)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	info := conn.CallInfo{}
	res, err := h.Call(cctx, m.op, params, conn.CallOpts{Info: &info})
	if pe := requestProblem(err); pe != nil {
		return nil, pe
	}
	o := outcome{status: model.StatusSucceeded, action: model.Executed()}
	if err != nil {
		o = outcome{status: model.StatusFailed, action: model.NotStarted(), err: apierr.As(err)}
	}
	fields := map[string]any{}
	if err == nil {
		for _, k := range m.fields {
			if v, ok := res[k]; ok {
				fields[k] = v
			}
		}
	}
	jobID := ""
	var arts []model.Artifact
	if err == nil && p.Save {
		job, a, serr := s.saveDiag(row.HostID, m, params, fields)
		jobID = job
		if serr != nil {
			o.status, o.err = model.StatusFailed, serr
		} else {
			arts = []model.Artifact{a}
			fields["artifact_id"] = a.ArtifactID
		}
	}
	return s.envelope(row.HostID, jobID, info, o, arts).Map(fields), nil
}

// saveDiag keeps a diag result as the artifact m.name of a new diag job:
// the host, the method, the time and the result fields. It returns the job
// id ("" when the job could not be created) and the artifact.
func (s *Server) saveDiag(hostID string, m diagMethod, params proto.Message, fields map[string]any) (string, model.Artifact, *apierr.Error) {
	job, err := s.d.Jobs.Create(hostID, jobs.KindDiag, "", m.summary, params)
	if err != nil {
		return "", model.Artifact{}, apierr.As(err)
	}
	data, err := json.MarshalIndent(map[string]any{"schema_version": model.SchemaVersion, "host_id": hostID, "method": m.op,
		"saved_at": model.Time(time.Now()), "result": fields}, "", "  ")
	var a model.Artifact
	if err == nil {
		a, err = saveArtifact(job.JobID, m.name, "report", "application/json", data)
	}
	if err != nil {
		e := apierr.Internal(fmt.Sprintf("결과를 산출물로 저장하지 못했습니다(%v). 디스크 공간을 확인한 뒤 save 없이 다시 읽거나 다시 저장하세요", err))
		_, _ = s.d.Jobs.Finish(job.JobID, model.StatusFailed, map[string]any{"error": e})
		return job.JobID, model.Artifact{}, e
	}
	_, _ = s.d.Jobs.Finish(job.JobID, model.StatusSucceeded, map[string]any{"artifact_id": a.ArtifactID, "artifacts": []model.Artifact{a}})
	return job.JobID, a, nil
}
