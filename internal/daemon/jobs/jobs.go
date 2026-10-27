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
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Job kinds.
const (
	KindExec    = "exec"
	KindInstall = "install"
	// KindTransfer is the job that mirrors a transfer; its transfer
	// manager sets its state (Mirror).
	KindTransfer = "transfer"
	// KindPTY is an interactive shell on the agent; its remote job is the
	// agent's pty.
	KindPTY = "pty"
	// KindDiag is a diagnostics result the daemon kept as an artifact.
	KindDiag = "diag"
	// KindDebug is a CDP debug session.
	KindDebug = "debug"
	// KindQA is a QA scenario run; its run manager sets its state.
	KindQA = "qa"
)

// earlyExitKeep is how long an exit reported before its job was known is
// kept for it.
const earlyExitKeep = time.Minute

// Registry keeps the jobs.
type Registry struct {
	st  *store.Store
	bus *events.Bus
	mgr *conn.Manager

	// base ends with Close; the kills of timed out jobs run under it.
	base context.Context
	stop context.CancelFunc

	mu         sync.Mutex
	earlyExits map[string]earlyExit
	// cancelling holds the jobs whose cancel or timeout kill is under way
	// with its reason; their exit finishes them by it.
	cancelling map[string]string
	// timers hold the timeouts of running exec.start jobs.
	timers map[string]*time.Timer
	closed bool
}

// Reasons a job's remote process is killed.
const (
	reasonCancel  = "cancel"
	reasonTimeout = "timeout"
)

type earlyExit struct {
	code int
	at   time.Time
}

// New returns a Registry over st that publishes on bus and reaches agents
// through mgr.
func New(st *store.Store, bus *events.Bus, mgr *conn.Manager) *Registry {
	r := &Registry{st: st, bus: bus, mgr: mgr, earlyExits: map[string]earlyExit{}, cancelling: map[string]string{},
		timers: map[string]*time.Timer{}}
	r.base, r.stop = context.WithCancel(context.Background())
	return r
}

// Close stops the timeouts and the kills under way.
func (r *Registry) Close() {
	r.mu.Lock()
	r.closed = true
	for id, t := range r.timers {
		t.Stop()
		delete(r.timers, id)
	}
	r.mu.Unlock()
	r.stop()
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

// current returns a job as it is stored.
func (r *Registry) current(jobID string) (model.Job, error) {
	j, err := r.load(jobID)
	if err != nil {
		return model.Job{}, err
	}
	return model.JobFrom(j), nil
}

// SetRemote records the remote job id of a running exec job and its start
// result. An exit the agent reported before is applied at once; a job that
// finished meanwhile is returned as it is.
func (r *Registry) SetRemote(jobID, remote string, result any) (model.Job, error) {
	err := r.st.SetJobRunning(jobID, remote, raw(result), time.Now())
	if errors.Is(err, store.ErrJobFinished) || errors.Is(err, store.ErrNotFound) {
		return r.current(jobID)
	}
	if err != nil {
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

// Finish records the final status and result of a job. A job that has
// finished keeps its status and result and is returned as it is.
func (r *Registry) Finish(jobID, status string, result any) (model.Job, error) {
	err := r.st.FinishJob(jobID, status, raw(result), time.Now())
	if errors.Is(err, store.ErrJobFinished) || errors.Is(err, store.ErrNotFound) {
		return r.current(jobID)
	}
	if err != nil {
		return model.Job{}, err
	}
	j, err := r.load(jobID)
	if err != nil {
		return model.Job{}, err
	}
	return r.publish(j), nil
}

// Mirror sets the status and result of the job jobID, finished or not,
// also when it had finished, and publishes it.
func (r *Registry) Mirror(jobID, status string, result any, finished bool) (model.Job, error) {
	if err := r.st.SetJobState(jobID, status, raw(result), finished, time.Now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return model.Job{}, apierr.NotFound(fmt.Sprintf("job %s가 없습니다. jobs.list로 job ID를 확인하세요", jobID))
		}
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
	if cur, err := r.st.GetJob(j.JobID); err == nil {
		j = cur
	}
	r.mu.Lock()
	reason := r.cancelling[j.JobID]
	r.mu.Unlock()
	if started, _ := mergeResult(j, nil)["timeout_started"].(bool); started && reason != reasonCancel {
		reason = reasonTimeout
	}
	extra := map[string]any{"state": "exited", "code": code}
	status := ExitStatus(code)
	switch reason {
	case reasonCancel:
		status = model.StatusCancelled
	case reasonTimeout:
		status = model.StatusFailed
		for k, v := range timeoutResult(j) {
			extra[k] = v
		}
	}
	return r.Finish(j.JobID, status, mergeResult(j, extra))
}

// timeoutMS returns the timeout_ms param of j, or 0.
func timeoutMS(j store.Job) int64 {
	var p struct {
		TimeoutMS int64 `json:"timeout_ms"`
	}
	if len(j.Params) == 0 || json.Unmarshal(j.Params, &p) != nil {
		return 0
	}
	return p.TimeoutMS
}

// deadline returns when j's timeout_ms ends, counted from its creation, and
// false when it has none.
func deadline(j store.Job) (time.Time, bool) {
	ms := timeoutMS(j)
	if ms <= 0 {
		return time.Time{}, false
	}
	return j.CreatedAt.Add(time.Duration(ms) * time.Millisecond), true
}

// timeoutResult holds the result members of a job its timeout killed.
func timeoutResult(j store.Job) map[string]any {
	limit := time.Duration(timeoutMS(j)) * time.Millisecond
	return map[string]any{"timed_out": true, "error": apierr.New(wire.CodeTimeout,
		fmt.Sprintf("명령이 %s 제한 시간을 초과했습니다. timeout_ms를 늘리거나 timeout_ms 없이 다시 실행하세요", limit), true, apierr.NextRetry)}
}

// ArmTimeout starts the timer of a running remote exec job with timeout_ms:
// when it ends, the job's process is killed as by jobs.cancel and the job
// ends failed with TIMEOUT. A job without timeout_ms is left alone.
func (r *Registry) ArmTimeout(jobID string) bool {
	j, err := r.st.GetJob(jobID)
	if err != nil || j.FinishedAt != nil || j.Kind != KindExec || j.RemoteJob == "" {
		return false
	}
	at, ok := deadline(j)
	if !ok {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	if t := r.timers[jobID]; t != nil {
		t.Stop()
	}
	r.timers[jobID] = time.AfterFunc(time.Until(at), func() { r.expire(jobID) })
	return true
}

// ResumeTimeouts arms the timers of the running remote exec jobs with
// timeout_ms when the daemon starts and returns how many it armed.
func (r *Registry) ResumeTimeouts() int {
	rows, err := r.st.ListJobs(store.JobFilter{Kind: KindExec, Unfinished: true, All: true})
	if err != nil {
		return 0
	}
	n := 0
	for _, j := range rows {
		if r.ArmTimeout(j.JobID) {
			n++
		}
	}
	return n
}

// expire kills the process of a job whose timeout ended. A host that is not
// connected is left to Reconcile, which kills its jobs whose timeout ended
// once it is ready.
func (r *Registry) expire(jobID string) {
	r.mu.Lock()
	delete(r.timers, jobID)
	r.mu.Unlock()
	j, err := r.st.GetJob(jobID)
	if err != nil || j.FinishedAt != nil {
		return
	}
	if r.readyHost(j.HostID) == nil {
		return
	}
	marked := mergeResult(j, map[string]any{"timeout_started": true})
	if err := r.st.SetJobRunning(jobID, j.RemoteJob, raw(marked), time.Now()); err != nil {
		if !errors.Is(err, store.ErrJobFinished) {
			r.retryTimeout(jobID)
		}
		return
	}
	ctx, cancel := context.WithTimeout(r.base, killTimeout)
	defer cancel()
	if _, err := r.kill(ctx, j, reasonTimeout, nil); err != nil {
		r.retryTimeout(jobID)
	}
}

func (r *Registry) retryTimeout(jobID string) {
	j, err := r.st.GetJob(jobID)
	if err != nil || j.FinishedAt != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if t := r.timers[jobID]; t != nil {
		t.Stop()
	}
	r.timers[jobID] = time.AfterFunc(time.Second, func() { r.expire(jobID) })
}

// killTimeout bounds the exec.kill of a timeout.
const killTimeout = 2 * time.Minute

// HandleAgentEvent applies the job.exited and pty.exited events of
// hostID's agent.
func (r *Registry) HandleAgentEvent(hostID string, ev proto.Message) {
	if name := ev.String("name"); name != "job.exited" && name != "pty.exited" {
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

// SettleInterrupted finishes, when the daemon starts, every job that had not
// finished and has no remote job: an exec.run, an exec.start whose answer
// had not come, or an install; a transfer job is its transfer manager's and
// a qa job its run manager's.
// Their outcome is unknown; each gets a job.updated event and a timeline
// entry. It returns how many it finished.
func (r *Registry) SettleInterrupted() int {
	rows, err := r.st.ListJobs(store.JobFilter{Unfinished: true, All: true})
	if err != nil {
		return 0
	}
	n := 0
	for _, j := range rows {
		if j.RemoteJob != "" || j.Kind == KindTransfer || j.Kind == KindQA {
			continue
		}
		e := apierr.New(wire.CodeExecutionOutcomeUnknown,
			"데몬이 멈춘 동안 이 작업의 결과를 받지 못했습니다. PC 상태를 확인한 뒤 필요하면 다시 실행하세요", false, apierr.NextCheckState)
		if _, err := r.Finish(j.JobID, model.StatusUnknown, mergeResult(j, map[string]any{"state": "interrupted", "error": e})); err != nil {
			continue
		}
		kind := model.TimelineExec
		if j.Kind == KindInstall {
			kind = model.TimelineInstall
		}
		_, _ = r.bus.Record(model.System.Entry(j.HostID, kind, "결과 불명(데몬이 멈춘 동안 실행 중): "+j.Summary, model.TimelineUnknown, j.JobID))
		n++
	}
	return n
}

// Reconcile reads the jobs of hostID's agent once with exec.list and
// finishes the host's running remote exec jobs that ended: an exited job by
// its exit code, and a lost job or one the agent no longer lists as
// unknown. It runs when the host becomes ready.
func (r *Registry) Reconcile(hostID string) {
	h := r.readyHost(hostID)
	if h == nil {
		return
	}
	rows, err := r.st.ListJobs(store.JobFilter{HostID: hostID, Kind: KindExec, Unfinished: true, All: true})
	if err != nil {
		return
	}
	var running []store.Job
	for _, j := range rows {
		if j.RemoteJob != "" {
			running = append(running, j)
		}
	}
	if len(running) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), reconcileTimeout)
	defer cancel()
	res, err := h.Call(ctx, "exec.list", nil, conn.CallOpts{})
	if err != nil {
		return
	}
	listed := map[string]proto.Message{}
	list, _ := res["jobs"].([]any)
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			listed[remoteID(m["job"])] = proto.Message(m)
		}
	}
	for _, j := range running {
		m, ok := listed[j.RemoteJob]
		switch {
		case ok && m.String("state") == "exited":
			_, _ = r.finishExit(j, m.Int("code"))
		case !ok || m.String("state") == "lost":
			_, _ = r.Finish(j.JobID, model.StatusUnknown, mergeResult(j, map[string]any{"state": "lost"}))
		default:
			if at, timed := deadline(j); timed && !time.Now().Before(at) {
				r.expire(j.JobID)
			}
		}
	}
}

// reconcileTimeout bounds the exec.list of Reconcile.
const reconcileTimeout = 30 * time.Second

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
	mj, err := r.Finish(j.JobID, model.StatusUnknown, mergeResult(j, map[string]any{"state": "lost"}))
	return mj, err == nil
}

// Output returns the output of a job from cursor: chunks of
// {stream, data}, next_cursor, state and status. A remote exec job is read
// from its agent and its next_cursor is "end" once the agent has given every
// byte of its output; an exec.run job returns its recorded output once its
// call finished. Any other job has no output and ends when it finished.
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
	res := mergeResult(j, nil)
	if j.FinishedAt != nil {
		if j.Kind == KindExec && cursor != "end" {
			for _, s := range []struct{ key, stream string }{{"stdout", "stdout"}, {"stderr", "stderr"}} {
				if d, _ := res[s.key].(string); d != "" {
					chunks = append(chunks, map[string]any{"stream": s.stream, "data": d})
				}
			}
		}
		next = "end"
	}
	out := map[string]any{"chunks": chunks, "next_cursor": next, "state": j.Status, "status": j.Status}
	if code, ok := res["code"]; ok && j.Kind == KindExec {
		out["code"] = code
	}
	return out, nil
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

// endOutput is the jobs.output result of a job whose output has ended.
func endOutput(j store.Job) map[string]any {
	out := map[string]any{"chunks": []map[string]any{}, "next_cursor": "end", "state": j.Status, "status": j.Status}
	res := mergeResult(j, nil)
	if s, ok := res["state"].(string); ok {
		out["state"] = s
	}
	if code, ok := res["code"]; ok {
		out["code"] = code
	}
	addFinal(out, j)
	return out
}

// addFinal copies timed_out and error of a finished job's result into a
// jobs.output result.
func addFinal(out map[string]any, j store.Job) {
	if j.FinishedAt == nil {
		return
	}
	res := mergeResult(j, nil)
	if t, ok := res["timed_out"].(bool); ok && t {
		out["timed_out"] = true
	}
	if e, ok := res["error"]; ok && e != nil {
		out["error"] = e
	}
}

// remoteOutput reads the output of j's remote job from cursor with
// exec.read. The answer's eof ends the output: next_cursor becomes "end"
// and a job still running is finished by the agent's state. A finished job
// whose remote job the agent no longer has ends without output.
func (r *Registry) remoteOutput(ctx context.Context, j store.Job, cursor string, max int) (map[string]any, error) {
	if cursor == "end" {
		return endOutput(j), nil
	}
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
		if j.FinishedAt != nil && apierr.CodeOf(err) == wire.CodeNotFound {
			return endOutput(j), nil
		}
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
	eof := res.Bool("eof")
	next := fmt.Sprintf("%d:%d", res.Int("out_next"), res.Int("err_next"))
	if eof {
		next = "end"
	}
	out := map[string]any{"chunks": chunks, "next_cursor": next, "state": state, "dropped": res.Bool("dropped")}
	if res.Has("code") {
		out["code"] = res.Int("code")
	}
	if eof && j.FinishedAt == nil {
		switch state {
		case "exited":
			_, _ = r.finishExit(j, res.Int("code"))
		case "lost":
			_, _ = r.Finish(j.JobID, model.StatusUnknown, mergeResult(j, map[string]any{"state": "lost"}))
		}
	}
	if eof {
		if cur, err := r.st.GetJob(j.JobID); err == nil {
			j = cur
		}
		addFinal(out, j)
	}
	out["status"] = j.Status
	return out, nil
}

// Cancel kills the remote process of a running exec.start or pty job and
// records it cancelled, also when its exit arrives before the answer to
// exec.kill. A job that finished before the cancel keeps its status. info
// receives what happened to the exec.kill request.
func (r *Registry) Cancel(ctx context.Context, jobID string, info *conn.CallInfo) (model.Job, error) {
	j, err := r.load(jobID)
	if err != nil {
		return model.Job{}, err
	}
	if j.FinishedAt != nil {
		return model.Job{}, apierr.Invalid(fmt.Sprintf("job %s는 이미 %s 상태로 끝났습니다. 취소할 필요가 없습니다", jobID, j.Status))
	}
	if j.Kind != KindExec && j.Kind != KindPTY || j.RemoteJob == "" {
		return model.Job{}, apierr.Invalid(fmt.Sprintf("job %s는 취소할 수 없는 종류입니다. 끝날 때까지 기다리세요", jobID))
	}
	return r.kill(ctx, j, reasonCancel, info)
}

// kill sends exec.kill for j's remote job and finishes j by reason:
// cancelled for a cancel, failed with TIMEOUT for a timeout. An exit that
// arrives while the kill is under way finishes j the same way.
func (r *Registry) kill(ctx context.Context, j store.Job, reason string, info *conn.CallInfo) (model.Job, error) {
	h := r.mgr.Get(j.HostID)
	if h == nil {
		return model.Job{}, apierr.NotFound(fmt.Sprintf("job %s의 PC %s가 등록돼 있지 않습니다. host.list로 확인하세요", j.JobID, j.HostID))
	}
	r.mu.Lock()
	if r.cancelling[j.JobID] == "" {
		r.cancelling[j.JobID] = reason
	}
	if t := r.timers[j.JobID]; t != nil && reason == reasonCancel {
		t.Stop()
		delete(r.timers, j.JobID)
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.cancelling[j.JobID] == reason {
			delete(r.cancelling, j.JobID)
		}
		r.mu.Unlock()
	}()
	res, err := h.Call(ctx, "exec.kill", proto.Message{"job": j.RemoteJob}, conn.CallOpts{Info: info})
	if err != nil {
		cur, cerr := r.current(j.JobID)
		if cerr != nil {
			cur = model.JobFrom(j)
		}
		return cur, err
	}
	extra := map[string]any{"state": "cancelled"}
	if s := res.String("state"); s != "" {
		extra["state"] = s
	}
	status := model.StatusCancelled
	if reason == reasonTimeout {
		status = model.StatusFailed
		for k, v := range timeoutResult(j) {
			extra[k] = v
		}
	}
	return r.Finish(j.JobID, status, mergeResult(j, extra))
}

// Exited finishes the job jobID whose remote process exited with code, as
// its exit event does.
func (r *Registry) Exited(jobID string, code int) (model.Job, error) {
	j, err := r.load(jobID)
	if err != nil {
		return model.Job{}, err
	}
	if j.FinishedAt != nil {
		return model.JobFrom(j), nil
	}
	return r.finishExit(j, code)
}

// StartedPIDs returns the process ids of the running exec.start and pty
// jobs of a host.
func (r *Registry) StartedPIDs(hostID string) map[int]bool {
	out := map[int]bool{}
	var rows []store.Job
	for _, kind := range []string{KindExec, KindPTY} {
		list, err := r.st.ListJobs(store.JobFilter{HostID: hostID, Kind: kind, Status: model.StatusRunning})
		if err != nil {
			return out
		}
		rows = append(rows, list...)
	}
	for _, j := range rows {
		res := mergeResult(j, nil)
		if pid, ok := res["pid"].(float64); ok && pid > 0 {
			out[int(pid)] = true
		}
	}
	return out
}
