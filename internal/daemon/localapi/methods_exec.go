package localapi

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/jobs"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// defaultExecTimeout is the timeout of exec.run without timeout_ms.
	defaultExecTimeout = 120 * time.Second
	// execMargin is added to the command timeout for the round trip.
	execMargin = 30 * time.Second
	// summaryChars bounds a job summary.
	summaryChars = 200
)

func (s *Server) registerExec() {
	s.methods["exec.run"] = s.execRun
	s.methods["exec.start"] = s.execStart
}

type execParams struct {
	HostID    string            `json:"host_id"`
	Argv      []string          `json:"argv"`
	Cmd       string            `json:"cmd"`
	Shell     string            `json:"shell"`
	Cwd       string            `json:"cwd"`
	Env       map[string]string `json:"env"`
	Stdin     *string           `json:"stdin"`
	TimeoutMS int               `json:"timeout_ms"`
}

func (p execParams) check(method string) error {
	if err := hostRequired(p.HostID); err != nil {
		return err
	}
	if len(p.Argv) == 0 && p.Cmd == "" {
		return apierr.Invalid(fmt.Sprintf("%s에는 argv나 cmd가 필요합니다. 실행할 명령을 주세요", method))
	}
	if len(p.Argv) > 0 && p.Cmd != "" {
		return apierr.Invalid(fmt.Sprintf("%s에는 argv와 cmd 중 하나만 주세요", method))
	}
	if p.TimeoutMS < 0 {
		return apierr.Invalid("timeout_ms는 0 이상이어야 합니다. 값을 고쳐 다시 보내세요")
	}
	return nil
}

// agentParams returns the fields of the agent's exec request.
func (p execParams) agentParams() proto.Message {
	m := proto.Message{}
	if len(p.Argv) > 0 {
		m["argv"] = p.Argv
	} else {
		m["cmd"] = p.Cmd
	}
	if p.Shell != "" {
		m["shell"] = p.Shell
	}
	if p.Cwd != "" {
		m["cwd"] = p.Cwd
	}
	if len(p.Env) > 0 {
		m["env"] = p.Env
	}
	if p.Stdin != nil {
		m["stdin"] = *p.Stdin
	}
	if p.TimeoutMS > 0 {
		m["timeout_ms"] = p.TimeoutMS
	}
	return m
}

// jobParams returns the params kept with the job: the environment's names
// and the length of stdin, not their values.
func (p execParams) jobParams() map[string]any {
	m := map[string]any{}
	if len(p.Argv) > 0 {
		m["argv"] = p.Argv
	} else {
		m["cmd"] = p.Cmd
	}
	if p.Shell != "" {
		m["shell"] = p.Shell
	}
	if p.Cwd != "" {
		m["cwd"] = p.Cwd
	}
	if len(p.Env) > 0 {
		keys := make([]string, 0, len(p.Env))
		for k := range p.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		m["env_keys"] = keys
	}
	if p.Stdin != nil {
		m["stdin_bytes"] = len(*p.Stdin)
	}
	if p.TimeoutMS > 0 {
		m["timeout_ms"] = p.TimeoutMS
	}
	return m
}

func (p execParams) summary() string {
	s := p.Cmd
	if len(p.Argv) > 0 {
		s = strings.Join(p.Argv, " ")
	}
	if r := []rune(s); len(r) > summaryChars {
		s = string(r[:summaryChars]) + "…"
	}
	return s
}

func (s *Server) execRun(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p execParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.check("exec.run"); err != nil {
		return nil, err
	}
	h, row, err := s.d.Hosts.Conn(p.HostID)
	if err != nil {
		return nil, err
	}
	timeout := defaultExecTimeout
	if p.TimeoutMS > 0 {
		timeout = time.Duration(p.TimeoutMS) * time.Millisecond
	}
	rid := conn.NewRID()
	if pe := requestProblem(conn.CheckRequest("exec.run", p.agentParams(), rid)); pe != nil {
		return nil, pe
	}
	summary := p.summary()
	job, err := s.d.Jobs.Create(row.HostID, jobs.KindExec, rid, summary, p.jobParams())
	if err != nil {
		return nil, err
	}
	cctx, cancel := s.detached(timeout + execMargin)
	defer cancel()
	info := conn.CallInfo{}
	res, err := h.Call(cctx, "exec.run", p.agentParams(), conn.CallOpts{RID: rid, Info: &info})
	o := outcomeOf(info, err)
	fields := map[string]any{}
	var arts []model.Artifact
	if err == nil {
		code := res.Int("code")
		stdout, stderr := res.String("stdout"), res.String("stderr")
		timedOut := res.Bool("timed_out")
		fields = map[string]any{"code": code, "stdout": stdout, "stderr": stderr, "timed_out": timedOut, "truncated": res.Bool("truncated")}
		o.status = jobs.ExitStatus(code)
		if timedOut {
			o.status = model.StatusFailed
			o.err = apierr.New(wire.CodeTimeout, fmt.Sprintf("명령이 %s 안에 끝나지 않아 종료했습니다. timeout_ms를 늘리거나 exec.start로 실행하세요", timeout), true, apierr.NextRetry)
		}
		for _, st := range []struct{ name, data string }{{"stdout.txt", stdout}, {"stderr.txt", stderr}} {
			if len(st.data) >= artifactThreshold {
				if a, err := saveArtifact(job.JobID, st.name, "log", "text/plain; charset=utf-8", []byte(st.data)); err == nil {
					arts = append(arts, a)
				}
			}
		}
	}
	result := map[string]any{}
	for k, v := range fields {
		result[k] = v
	}
	if o.err != nil {
		result["error"] = o.err
	}
	if len(arts) > 0 {
		result["artifacts"] = arts
	}
	_, _ = s.d.Jobs.Finish(job.JobID, o.status, result)
	tl := "명령 실행: " + summary
	if err == nil {
		tl += fmt.Sprintf(" (종료 코드 %d)", res.Int("code"))
	}
	_, _ = s.d.Bus.Record(sess.Caller().Entry(row.HostID, model.TimelineExec, tl, timelineOutcome(o.status), job.JobID))
	return s.envelope(row.HostID, job.JobID, info, o, arts).Map(fields), nil
}

func (s *Server) execStart(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p execParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := p.check("exec.start"); err != nil {
		return nil, err
	}
	h, row, err := s.d.Hosts.Conn(p.HostID)
	if err != nil {
		return nil, err
	}
	rid := conn.NewRID()
	params := p.agentParams()
	delete(params, "timeout_ms")
	if pe := requestProblem(conn.CheckRequest("exec.start", params, rid)); pe != nil {
		return nil, pe
	}
	summary := p.summary()
	job, err := s.d.Jobs.Create(row.HostID, jobs.KindExec, rid, summary, p.jobParams())
	if err != nil {
		return nil, err
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	info := conn.CallInfo{}
	res, err := h.Call(cctx, "exec.start", params, conn.CallOpts{RID: rid, Info: &info})
	o := outcomeOf(info, err)
	fields := map[string]any{}
	if err == nil {
		remote := res.String("job")
		if remote == "" {
			remote = fmt.Sprint(res["job"])
		}
		fields = map[string]any{"pid": res.Int("pid"), "remote_job": remote}
		if j, err := s.d.Jobs.SetRemote(job.JobID, remote, map[string]any{"job": remote, "pid": res.Int("pid")}); err == nil {
			job = j
		}
		o.status = job.Status
	} else {
		result := map[string]any{"error": o.err}
		_, _ = s.d.Jobs.Finish(job.JobID, o.status, result)
	}
	_, _ = s.d.Bus.Record(sess.Caller().Entry(row.HostID, model.TimelineExec, "명령 시작: "+summary, timelineOutcome(o.status), job.JobID))
	return s.envelope(row.HostID, job.JobID, info, o, nil).Map(fields), nil
}
