// Package jobs keeps the daemon's jobs: exec runs and starts, installs. It
// stores them, follows remote exec jobs through the agent and publishes
// job.updated.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/ids"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
)

// Job kinds.
const (
	KindExec    = "exec"
	KindInstall = "install"
)

// earlyExitKeep is how long an exit reported before its job was known is
// kept for it.
const earlyExitKeep = time.Minute

// Registry keeps the jobs.
type Registry struct {
	st  *store.Store
	bus *events.Bus
	mgr *conn.Manager

	mu         sync.Mutex
	earlyExits map[string]earlyExit
}

type earlyExit struct {
	code int
	at   time.Time
}

// New returns a Registry over st that publishes on bus and reaches agents
// through mgr.
func New(st *store.Store, bus *events.Bus, mgr *conn.Manager) *Registry {
	return &Registry{st: st, bus: bus, mgr: mgr, earlyExits: map[string]earlyExit{}}
}

func raw(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	if b, ok := v.(json.RawMessage); ok {
		return b
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// publish returns the API form of the job and publishes it as job.updated.
func (r *Registry) publish(j store.Job) model.Job {
	mj := model.JobFrom(j)
	r.bus.Publish(j.HostID, j.JobID, events.JobUpdated, map[string]any{"job": mj})
	return mj
}

func (r *Registry) load(jobID string) (store.Job, error) {
	j, err := r.st.GetJob(jobID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Job{}, apierr.NotFound(fmt.Sprintf("job %s가 없습니다. jobs.list로 job ID를 확인하세요", jobID))
	}
	return j, err
}

// Create stores a running job.
func (r *Registry) Create(hostID, kind, requestID, summary string, params any) (model.Job, error) {
	now := time.Now()
	j := store.Job{JobID: ids.New(ids.Job), HostID: hostID, Kind: kind, RequestID: requestID, Status: model.StatusRunning,
		Summary: summary, Params: raw(params), CreatedAt: now, UpdatedAt: now}
	if err := r.st.CreateJob(j); err != nil {
		return model.Job{}, apierr.Internal(fmt.Sprintf("job을 저장하지 못했습니다(%v). 데몬 로그를 확인하세요", err))
	}
	return r.publish(j), nil
}

// SetRemote records the remote job id of a running exec job and its start
// result. An exit the agent reported before is applied at once.
func (r *Registry) SetRemote(jobID, remote string, result any) (model.Job, error) {
	if err := r.st.SetJobRunning(jobID, remote, raw(result), time.Now()); err != nil {
		return model.Job{}, err
	}
	j, err := r.load(jobID)
	if err != nil {
		return model.Job{}, err
	}
	r.mu.Lock()
	key := j.HostID + "/" + remote
	ex, ok := r.earlyExits[key]
	delete(r.earlyExits, key)
	r.mu.Unlock()
	if ok {
		return r.finishExit(j, ex.code)
	}
	return r.publish(j), nil
}

// Finish records the final status and result of a job.
func (r *Registry) Finish(jobID, status string, result any) (model.Job, error) {
	if err := r.st.FinishJob(jobID, status, raw(result), time.Now()); err != nil {
		return model.Job{}, err
	}
	j, err := r.load(jobID)
	if err != nil {
		return model.Job{}, err
	}
	return r.publish(j), nil
}

// ExitStatus is the job status of an exit code: succeeded for 0, failed
// otherwise.
func ExitStatus(code int) string {
	if code == 0 {
		return model.StatusSucceeded
	}
	return model.StatusFailed
}

// mergeResult returns the stored result of j with extra set.
func mergeResult(j store.Job, extra map[string]any) map[string]any {
	out := map[string]any{}
	if len(j.Result) > 0 {
		_ = json.Unmarshal(j.Result, &out)
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func (r *Registry) finishExit(j store.Job, code int) (model.Job, error) {
	return r.Finish(j.JobID, ExitStatus(code), mergeResult(j, map[string]any{"state": "exited", "code": code}))
}

// HandleAgentEvent applies the job.exited event of hostID's agent.
func (r *Registry) HandleAgentEvent(hostID string, ev proto.Message) {
	if ev.String("name") != "job.exited" {
		return
	}
	remote := remoteID(ev["job"])
	if remote == "" {
		return
	}
	code := ev.Int("code")
	j, err := r.st.JobByRemote(hostID, remote)
	if err != nil {
		r.mu.Lock()
		now := time.Now()
		for k, e := range r.earlyExits {
			if now.Sub(e.at) > earlyExitKeep {
				delete(r.earlyExits, k)
			}
		}
		r.earlyExits[hostID+"/"+remote] = earlyExit{code: code, at: now}
		r.mu.Unlock()
		return
	}
	if j.FinishedAt != nil {
		return
	}
	_, _ = r.finishExit(j, code)
}

// remoteID returns an agent job id, which is a string or a number.
func remoteID(v any) string {
	switch id := v.(type) {
	case string:
		return id
	case float64:
		return strconv.FormatInt(int64(id), 10)
	}
	return ""
}

// List returns the jobs f selects, newest first.
func (r *Registry) List(f store.JobFilter) ([]model.Job, error) {
	rows, err := r.st.ListJobs(f)
	if err != nil {
		return nil, err
	}
	out := make([]model.Job, 0, len(rows))
	for _, j := range rows {
		out = append(out, model.JobFrom(j))
	}
	return out, nil
}

// readyHost returns the connection of the job's host when it is connected.
func (r *Registry) readyHost(hostID string) *conn.Host {
	h := r.mgr.Get(hostID)
	if h == nil {
		return nil
	}
	if st := h.State(); st != conn.StateReady && st != conn.StateDegraded {
		return nil
	}
	return h
}

// Get returns a job; a running remote exec job is first brought up to date
// from its agent when the host is connected.
func (r *Registry) Get(ctx context.Context, jobID string) (model.Job, error) {
	j, err := r.load(jobID)
	if err != nil {
		return model.Job{}, err
	}
	if j.FinishedAt == nil && j.Kind == KindExec && j.RemoteJob != "" {
		if h := r.readyHost(j.HostID); h != nil {
			if updated, ok := r.refresh(ctx, h, j); ok {
				return updated, nil
			}
		}
	}
	return model.JobFrom(j), nil
}

// refresh reads the state of j's remote job with exec.list and finishes j
// when the remote job ended.
func (r *Registry) refresh(ctx context.Context, h *conn.Host, j store.Job) (model.Job, bool) {
	res, err := h.Call(ctx, "exec.list", nil, conn.CallOpts{})
	if err != nil {
		return model.Job{}, false
	}
	list, _ := res["jobs"].([]any)
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok || remoteID(m["job"]) != j.RemoteJob {
			continue
		}
		jm := proto.Message(m)
		switch jm.String("state") {
		case "exited":
			mj, err := r.finishExit(j, jm.Int("code"))
			return mj, err == nil
		case "lost":
			mj, err := r.Finish(j.JobID, model.StatusUnknown, mergeResult(j, map[string]any{"state": "lost"}))
			return mj, err == nil
		}
		return model.JobFrom(j), true
	}
	return model.JobFrom(j), true
}

// Output returns the output of a job from cursor: chunks of
// {stream, data}, next_cursor and state. A remote exec job is read from
// its agent; an exec.run job returns its recorded output once.
func (r *Registry) Output(ctx context.Context, jobID, cursor string, max int) (map[string]any, error) {
	j, err := r.load(jobID)
	if err != nil {
		return nil, err
	}
	if j.Kind == KindExec && j.RemoteJob != "" {
		return r.remoteOutput(ctx, j, cursor, max)
	}
	chunks := []map[string]any{}
	next := cursor
	state := j.Status
	if j.Kind == KindExec {
		res := mergeResult(j, nil)
		if cursor != "end" && j.FinishedAt != nil {
			for _, s := range []struct{ key, stream string }{{"stdout", "stdout"}, {"stderr", "stderr"}} {
				if d, _ := res[s.key].(string); d != "" {
					chunks = append(chunks, map[string]any{"stream": s.stream, "data": d})
				}
			}
			next = "end"
		}
		out := map[string]any{"chunks": chunks, "next_cursor": next, "state": state, "status": j.Status}
		if code, ok := res["code"]; ok {
			out["code"] = code
		}
		return out, nil
	}
	return map[string]any{"chunks": chunks, "next_cursor": next, "state": state, "status": j.Status}, nil
}

// parseCursor reads "out:err" byte offsets; "" is 0:0.
func parseCursor(c string) (int, int, error) {
	if c == "" {
		return 0, 0, nil
	}
	a, b, ok := strings.Cut(c, ":")
	out, err1 := strconv.Atoi(a)
	errOff, err2 := strconv.Atoi(b)
	if !ok || err1 != nil || err2 != nil || out < 0 || errOff < 0 {
		return 0, 0, apierr.Invalid(fmt.Sprintf("cursor %q가 올바르지 않습니다. 이전 jobs.output 결과의 next_cursor를 그대로 주세요", c))
	}
	return out, errOff, nil
}

func (r *Registry) remoteOutput(ctx context.Context, j store.Job, cursor string, max int) (map[string]any, error) {
	outOff, errOff, err := parseCursor(cursor)
	if err != nil {
		return nil, err
	}
	h := r.mgr.Get(j.HostID)
	if h == nil {
		return nil, apierr.NotFound(fmt.Sprintf("job %s의 PC %s가 등록돼 있지 않습니다. host.list로 확인하세요", j.JobID, j.HostID))
	}
	params := proto.Message{"job": j.RemoteJob, "out_offset": outOff, "err_offset": errOff}
	if max > 0 {
		params["max"] = max
	}
	res, err := h.Call(ctx, "exec.read", params, conn.CallOpts{})
	if err != nil {
		return nil, err
	}
	chunks := []map[string]any{}
	if d := res.String("out"); d != "" {
		chunks = append(chunks, map[string]any{"stream": "stdout", "data": d})
	}
	if d := res.String("err"); d != "" {
		chunks = append(chunks, map[string]any{"stream": "stderr", "data": d})
	}
	state := res.String("state")
	out := map[string]any{"chunks": chunks, "next_cursor": fmt.Sprintf("%d:%d", res.Int("out_next"), res.Int("err_next")),
		"state": state, "dropped": res.Bool("dropped")}
	if res.Has("code") {
		out["code"] = res.Int("code")
	}
	status := j.Status
	if j.FinishedAt == nil {
		switch state {
		case "exited":
			if mj, err := r.finishExit(j, res.Int("code")); err == nil {
				status = mj.Status
			}
		case "lost":
			if mj, err := r.Finish(j.JobID, model.StatusUnknown, mergeResult(j, map[string]any{"state": "lost"})); err == nil {
				status = mj.Status
			}
		}
	}
	out["status"] = status
	return out, nil
}

// Cancel kills the remote process of a running exec.start job and records
// it cancelled. info receives what happened to the exec.kill request.
func (r *Registry) Cancel(ctx context.Context, jobID string, info *conn.CallInfo) (model.Job, error) {
	j, err := r.load(jobID)
	if err != nil {
		return model.Job{}, err
	}
	if j.FinishedAt != nil {
		return model.Job{}, apierr.Invalid(fmt.Sprintf("job %s는 이미 %s 상태로 끝났습니다. 취소할 필요가 없습니다", jobID, j.Status))
	}
	if j.Kind != KindExec || j.RemoteJob == "" {
		return model.Job{}, apierr.Invalid(fmt.Sprintf("job %s는 취소할 수 없는 종류입니다. 끝날 때까지 기다리세요", jobID))
	}
	h := r.mgr.Get(j.HostID)
	if h == nil {
		return model.Job{}, apierr.NotFound(fmt.Sprintf("job %s의 PC %s가 등록돼 있지 않습니다. host.list로 확인하세요", j.JobID, j.HostID))
	}
	res, err := h.Call(ctx, "exec.kill", proto.Message{"job": j.RemoteJob}, conn.CallOpts{Info: info})
	if err != nil {
		return model.JobFrom(j), err
	}
	extra := map[string]any{"state": "cancelled"}
	if s := res.String("state"); s != "" {
		extra["state"] = s
	}
	return r.Finish(j.JobID, model.StatusCancelled, mergeResult(j, extra))
}

// StartedPIDs returns the process ids of the running exec.start jobs of a
// host.
func (r *Registry) StartedPIDs(hostID string) map[int]bool {
	rows, err := r.st.ListJobs(store.JobFilter{HostID: hostID, Kind: KindExec, Status: model.StatusRunning})
	out := map[int]bool{}
	if err != nil {
		return out
	}
	for _, j := range rows {
		res := mergeResult(j, nil)
		if pid, ok := res["pid"].(float64); ok && pid > 0 {
			out[int(pid)] = true
		}
	}
	return out
}
