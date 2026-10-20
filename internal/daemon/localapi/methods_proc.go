package localapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/proto"
)

func (s *Server) registerProc() {
	s.methods["proc.list"] = s.procList
	s.methods["proc.kill"] = s.procKill
	s.methods["app.launch"] = s.appLaunch
}

type procParams struct {
	HostID string `json:"host_id"`
	Name   string `json:"name"`
	PID    int    `json:"pid"`
	Tree   bool   `json:"tree"`
}

func (s *Server) procList(ctx context.Context, _ *session, raw json.RawMessage) (any, error) {
	var p procParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := hostRequired(p.HostID); err != nil {
		return nil, err
	}
	h, row, err := s.d.Hosts.Conn(p.HostID)
	if err != nil {
		return nil, err
	}
	params := proto.Message{}
	if p.Name != "" {
		params["name"] = p.Name
	}
	res, err := h.Call(ctx, "proc.list", params, conn.CallOpts{})
	if err != nil {
		return nil, err
	}
	raws, _ := res["procs"].([]any)
	procs := make([]model.ProcessInfo, 0, len(raws))
	byPID := map[int]int{}
	for _, r := range raws {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		e := proto.Message(m)
		pi := model.ProcessInfo{PID: e.Int("pid"), PPID: e.Int("ppid"), Name: e.String("name"), Path: e.String("path"),
			Cmd: e.String("cmd"), MemKB: e.Int("mem_kb")}
		byPID[pi.PID] = len(procs)
		procs = append(procs, pi)
	}
	started := s.d.Jobs.StartedPIDs(row.HostID)
	for i := range procs {
		seen := map[int]bool{}
		for pid := procs[i].PID; pid > 0 && !seen[pid]; {
			seen[pid] = true
			if started[pid] {
				procs[i].StartedByDeskhand = true
				break
			}
			j, ok := byPID[pid]
			if !ok {
				break
			}
			pid = procs[j].PPID
		}
	}
	return map[string]any{"host_id": row.HostID, "procs": procs}, nil
}

func (s *Server) procKill(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p procParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := hostRequired(p.HostID); err != nil {
		return nil, err
	}
	if p.PID <= 0 && p.Name == "" {
		return nil, apierr.Invalid("proc.kill에는 pid나 name이 필요합니다. 끝낼 프로세스를 주세요")
	}
	params := proto.Message{}
	target := p.Name
	if p.PID > 0 {
		params["pid"] = p.PID
		target = fmt.Sprintf("pid %d", p.PID)
	} else {
		params["name"] = p.Name
	}
	if p.Tree {
		params["tree"] = true
	}
	h, row, err := s.d.Hosts.Conn(p.HostID)
	if err != nil {
		return nil, err
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	info := conn.CallInfo{}
	res, err := h.Call(cctx, "proc.kill", params, conn.CallOpts{Info: &info})
	o := outcomeOf(info, err)
	fields := map[string]any{}
	if err == nil {
		fields["killed"] = res.Int("killed")
	}
	_, _ = s.d.Bus.Record(sess.Caller().Entry(row.HostID, model.TimelineExec, "프로세스 종료: "+target, timelineOutcome(o.status), ""))
	return s.envelope(row.HostID, "", info, o, nil).Map(fields), nil
}

type launchParams struct {
	HostID       string   `json:"host_id"`
	Path         string   `json:"path"`
	Args         []string `json:"args"`
	Cwd          string   `json:"cwd"`
	WaitWindowMS *int     `json:"wait_window_ms"`
}

func (s *Server) appLaunch(ctx context.Context, sess *session, raw json.RawMessage) (any, error) {
	var p launchParams
	if err := decode(raw, &p); err != nil {
		return nil, err
	}
	if err := hostRequired(p.HostID); err != nil {
		return nil, err
	}
	if p.Path == "" {
		return nil, required("path", "app.launch")
	}
	params := proto.Message{"path": p.Path}
	if len(p.Args) > 0 {
		params["args"] = p.Args
	}
	if p.Cwd != "" {
		params["cwd"] = p.Cwd
	}
	if p.WaitWindowMS != nil {
		params["wait_window_ms"] = *p.WaitWindowMS
	}
	h, row, err := s.d.Hosts.Conn(p.HostID)
	if err != nil {
		return nil, err
	}
	cctx, cancel := s.detached(remoteTimeout)
	defer cancel()
	info := conn.CallInfo{}
	res, err := h.Call(cctx, "app.launch", params, conn.CallOpts{Info: &info})
	o := outcomeOf(info, err)
	fields := map[string]any{}
	if err == nil {
		for k, v := range res {
			fields[k] = v
		}
	}
	_, _ = s.d.Bus.Record(sess.Caller().Entry(row.HostID, model.TimelineExec, "앱 실행: "+p.Path, timelineOutcome(o.status), ""))
	return s.envelope(row.HostID, "", info, o, nil).Map(fields), nil
}
