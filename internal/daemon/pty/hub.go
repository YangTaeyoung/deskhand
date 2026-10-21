// Package pty relays the interactive shells of the hosts (docs/local-api.md
// §3 pty.open, §4 kind "pty"): it starts each shell on its agent as a pty
// job, attaches local API streams to it and follows its end.
package pty

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/ids"
	"github.com/YangTaeyoung/deskhand/internal/daemon/jobs"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// MaxSize bounds the columns and rows of a terminal.
	MaxSize = 1000
	// lineTimeout bounds the wait for the agent's acceptance line and its
	// attached frame.
	lineTimeout = 30 * time.Second
	// writeTimeout bounds one frame write to either side of a relay.
	writeTimeout = 30 * time.Second
	// maxClientFrame bounds the frames a relay reads from its client.
	maxClientFrame = 1 << 20
	// settleWait bounds how long an open whose answer was lost waits for
	// pty.list to settle it.
	settleWait = 30 * time.Second
	// listTimeout bounds one pty.list call.
	listTimeout = 30 * time.Second
	// detachWait bounds the wait for the agent to end its direction after
	// the client detached.
	detachWait = 5 * time.Second
)

// Shell names a local API client may open.
var Shells = []string{"powershell", "pwsh", "cmd", "sh"}

// Options configure a Hub.
type Options struct {
	Store  *store.Store
	Conns  *conn.Manager
	Jobs   *jobs.Registry
	Logger *log.Logger
}

// Hub opens, attaches and follows the pty jobs.
type Hub struct {
	opt Options
}

// New returns a Hub.
func New(opt Options) *Hub {
	if opt.Logger == nil {
		opt.Logger = log.New(io.Discard, "", 0)
	}
	return &Hub{opt: opt}
}

// jobParams are the params kept with a pty job.
type jobParams struct {
	Shell string `json:"shell"`
	Cols  int    `json:"cols"`
	Rows  int    `json:"rows"`
	PTYID string `json:"pty_id"`
}

// OpenParams are the params of pty.open.
type OpenParams struct {
	HostID string `json:"host_id"`
	Shell  string `json:"shell"`
	Cols   int    `json:"cols"`
	Rows   int    `json:"rows"`
}

// Check returns an INVALID_ARGUMENT error for params pty.open refuses.
func (p OpenParams) Check() error {
	if p.HostID == "" {
		return apierr.Invalid("host_id가 없습니다. host.list로 PC의 host_id나 이름을 확인해 주세요")
	}
	if p.Shell != "" && !known(p.Shell) {
		return apierr.Invalid(fmt.Sprintf("shell %q를 모릅니다. powershell, pwsh, cmd 중 하나를 주세요(Windows가 아닌 PC는 sh)", p.Shell))
	}
	if p.Cols < 0 || p.Rows < 0 || p.Cols > MaxSize || p.Rows > MaxSize || (p.Cols == 0) != (p.Rows == 0) {
		return apierr.Invalid(fmt.Sprintf("cols와 rows는 함께 1–%d 사이로 주세요(둘 다 생략하면 80×24)", MaxSize))
	}
	return nil
}

func known(shell string) bool {
	for _, s := range Shells {
		if s == shell {
			return true
		}
	}
	return false
}

// Opened is what pty.open did: the job and pty id of the shell, the
// request id and connection generation of the start, and the envelope's
// status, action and error.
type Opened struct {
	Job        model.Job
	PTYID      string
	RID        string
	Generation uint64
	Status     string
	Action     model.Action
	Err        *apierr.Error
}

// shellTitle is the name of a shell in job summaries.
func shellTitle(shell string) string {
	switch shell {
	case "powershell":
		return "PowerShell"
	case "pwsh":
		return "PowerShell 7"
	}
	return shell
}

// Open starts a shell on host as a new pty job of caller. The shell is the
// one p names, which caps.pty has to list, or the first one it lists. A
// refusal or an unreachable host finishes the job failed; a start whose
// answer was lost is settled with pty.list.
func (h *Hub) Open(ctx context.Context, host *conn.Host, p OpenParams, caller model.Caller) (Opened, error) {
	if err := p.Check(); err != nil {
		return Opened{}, err
	}
	cols, rows := p.Cols, p.Rows
	if cols == 0 {
		cols, rows = 80, 24
	}
	hello := host.Hello()
	shell := p.Shell
	capPTY, hasCaps := hello.Caps["pty"]
	if shell == "" {
		shell = "powershell"
		if len(capPTY.Shells) > 0 {
			shell = capPTY.Shells[0]
		}
	}
	rid := conn.NewRID()
	ptyID := ids.New(ids.PTY)
	summary := shellTitle(shell) + " 터미널"
	job, err := h.opt.Jobs.Create(host.ID(), jobs.KindPTY, rid, summary, jobParams{Shell: shell, Cols: cols, Rows: rows, PTYID: ptyID})
	if err != nil {
		return Opened{}, err
	}
	out := Opened{PTYID: ptyID, RID: rid, Generation: host.Generation()}
	fail := func(status string, action model.Action, e *apierr.Error) (Opened, error) {
		out.Status, out.Action, out.Err = status, action, e
		out.Job, _ = h.opt.Jobs.Finish(job.JobID, status, map[string]any{"shell": shell, "error": e})
		return out, nil
	}
	if hasCaps && host.State() != conn.StateOffline {
		switch {
		case capPTY.State != "ready":
			reason := capPTY.Reason
			if reason == "" {
				reason = "에이전트가 터미널을 지원하지 않습니다"
			}
			return fail(model.StatusFailed, model.NotStarted(), apierr.New(wire.CodeCapabilityUnsupported,
				fmt.Sprintf("이 PC에서 터미널을 열 수 없습니다(%s). deskhand host doctor로 에이전트를 점검하세요", reason), false, apierr.NextHostDoctor))
		case !contains(capPTY.Shells, shell):
			return fail(model.StatusFailed, model.NotStarted(), apierr.New(wire.CodeCapabilityUnsupported,
				fmt.Sprintf("이 PC에는 %s 셸이 없습니다. %v 중 하나로 여세요", shell, capPTY.Shells), false, apierr.NextFixParams))
		}
	}
	hdr := wire.StreamHeader{S: "pty", New: &wire.PTYNew{Shell: shell, Cols: cols, Rows: rows, RID: rid}}
	st, gen, err := host.OpenStream(ctx, hdr)
	if err != nil {
		return fail(model.StatusFailed, model.NotStarted(), apierr.As(err))
	}
	bootID := host.Hello().BootID
	out.Generation = gen
	accept, _, err := readAcceptance(st)
	st.CancelRead()
	_ = st.CloseWrite()
	switch {
	case err != nil:
		return h.settle(ctx, host, job, out, shell, bootID, caller)
	case accept["ok"] != true:
		return fail(model.StatusFailed, model.NotStarted(), refusal(accept))
	}
	return h.started(job, out, shell, accept.String("job"), accept.Int("pid"), caller)
}

// started records the agent's pty and pid of a pty job that started.
func (h *Hub) started(job model.Job, out Opened, shell, remote string, pid int, caller model.Caller) (Opened, error) {
	result := map[string]any{"shell": shell, "pid": pid}
	if caller.ClientName != "" {
		result["client_name"] = caller.ClientName
	}
	j, err := h.opt.Jobs.SetRemote(job.JobID, remote, result)
	if err != nil {
		return Opened{}, err
	}
	out.Job, out.Status, out.Action = j, j.Status, model.Executed()
	return out, nil
}

// settle finds out with pty.list whether the shell of a start whose answer
// was lost runs: listed with its rid, it started; not listed by the same
// run of the agent, it did not; otherwise its outcome is unknown.
func (h *Hub) settle(ctx context.Context, host *conn.Host, job model.Job, out Opened, shell, bootID string, caller model.Caller) (Opened, error) {
	sctx, cancel := context.WithTimeout(ctx, settleWait)
	defer cancel()
	for sctx.Err() == nil {
		lctx, lcancel := context.WithTimeout(sctx, listTimeout)
		res, err := host.Call(lctx, "pty.list", nil, conn.CallOpts{})
		lcancel()
		if err != nil {
			if apierr.CodeOf(err) == wire.CodeCapabilityUnsupported {
				break
			}
			select {
			case <-sctx.Done():
			case <-time.After(250 * time.Millisecond):
			}
			continue
		}
		starting := false
		for _, item := range listed(res) {
			if item.String("rid") != out.RID {
				continue
			}
			if item.String("state") != "starting" {
				return h.started(job, out, shell, item.String("job"), item.Int("pid"), caller)
			}
			starting = true
		}
		if starting {
			select {
			case <-sctx.Done():
			case <-time.After(250 * time.Millisecond):
			}
			continue
		}
		if host.Hello().BootID == bootID {
			e := apierr.New(wire.CodeOffline, "PC와의 연결이 끊겨 셸을 열지 못했습니다. 다시 열어 주세요", true, apierr.NextRetry)
			out.Status, out.Action, out.Err = model.StatusFailed, model.NotStarted(), e
			out.Job, _ = h.opt.Jobs.Finish(job.JobID, model.StatusFailed, map[string]any{"shell": shell, "error": e})
			return out, nil
		}
		break
	}
	e := apierr.New(wire.CodeExecutionOutcomeUnknown,
		"셸을 여는 동안 PC와의 연결이 끊겨 셸이 열렸는지 확인하지 못했습니다. jobs.list로 터미널 목록을 확인한 뒤 필요하면 다시 여세요", false, apierr.NextCheckState)
	out.Status, out.Action, out.Err = model.StatusUnknown, model.UnknownAction(), e
	out.Job, _ = h.opt.Jobs.Finish(job.JobID, model.StatusUnknown, map[string]any{"shell": shell, "state": "interrupted", "error": e})
	return out, nil
}

// listed returns the entries of a pty.list answer.
func listed(res proto.Message) []proto.Message {
	var out []proto.Message
	list, _ := res["ptys"].([]any)
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, proto.Message(m))
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// refusal returns the error of an agent's refusal line.
func refusal(m proto.Message) *apierr.Error {
	code := m.String("code")
	if code == "" {
		code = wire.CodeInternal
	}
	msg := m.String("error")
	if msg == "" {
		msg = "에이전트가 터미널 스트림을 거절했습니다. 데몬 로그를 확인하세요"
	}
	next := ""
	if code == wire.CodeCapabilityUnsupported {
		next = apierr.NextHostRepair
	}
	return apierr.New(code, msg, m.Bool("retryable"), next)
}

// readAcceptance reads the agent's first line of a pty stream within
// lineTimeout and returns it with a reader of what follows it.
func readAcceptance(st conn.DataStream) (proto.Message, *bufio.Reader, error) {
	_ = st.SetReadDeadline(time.Now().Add(lineTimeout))
	defer st.SetReadDeadline(time.Time{})
	r := bufio.NewReader(st)
	var m proto.Message
	if err := wire.ReadHeader(r, &m); err != nil {
		return nil, nil, err
	}
	return m, r, nil
}

// Ref names the pty a stream attaches to: the pty id of pty.open or the
// job id.
type Ref struct {
	PTYID string `json:"pty_id"`
	JobID string `json:"job_id"`
}

// find returns the pty job ref names.
func (h *Hub) find(ref Ref) (store.Job, error) {
	switch {
	case ref.JobID != "" && ref.PTYID != "":
		return store.Job{}, apierr.Invalid("stream.attach에는 pty_id와 job_id 중 하나만 주세요")
	case ref.JobID != "":
		j, err := h.opt.Store.GetJob(ref.JobID)
		if errors.Is(err, store.ErrNotFound) || err == nil && j.Kind != jobs.KindPTY {
			return store.Job{}, apierr.NotFound(fmt.Sprintf("터미널 job %s가 없습니다. jobs.list{kind:\"pty\"}로 열린 터미널을 확인하세요", ref.JobID))
		}
		return j, err
	case ref.PTYID != "":
		rows, err := h.opt.Store.ListJobs(store.JobFilter{Kind: jobs.KindPTY, All: true})
		if err != nil {
			return store.Job{}, err
		}
		for _, j := range rows {
			var p jobParams
			if json.Unmarshal(j.Params, &p) == nil && p.PTYID == ref.PTYID {
				return j, nil
			}
		}
		return store.Job{}, apierr.NotFound(fmt.Sprintf("터미널 %s가 없습니다. pty.open으로 새 터미널을 여세요", ref.PTYID))
	}
	return store.Job{}, apierr.Invalid("stream.attach{kind:\"pty\"}에는 pty_id나 job_id가 필요합니다")
}

// HostOf returns the host of the pty job ref names.
func (h *Hub) HostOf(ref Ref) (string, error) {
	j, err := h.find(ref)
	if err != nil {
		return "", err
	}
	return j.HostID, nil
}

// ended is the NOT_FOUND error of an attach to a pty job that finished.
func ended(j store.Job) *apierr.Error {
	var res struct {
		Code *int `json:"code"`
	}
	_ = json.Unmarshal(j.Result, &res)
	if res.Code != nil {
		return apierr.NotFound(fmt.Sprintf("터미널 job %s의 셸은 종료 코드 %d로 끝났습니다. 새 터미널을 여세요", j.JobID, *res.Code))
	}
	return apierr.NotFound(fmt.Sprintf("터미널 job %s는 이미 %s 상태로 끝났습니다. 새 터미널을 여세요", j.JobID, j.Status))
}

// Relay is a pty stream of an agent, attached for one local API stream.
type Relay struct {
	h      *Hub
	job    string
	hostID string
	pid    int
	st     conn.DataStream
	r      *bufio.Reader
}

// Job returns the job id of the relay's pty.
func (r *Relay) Job() string { return r.job }

// PID returns the process id of the relay's shell.
func (r *Relay) PID() int { return r.pid }

// HostID returns the host of the relay's pty.
func (r *Relay) HostID() string { return r.hostID }

// Attach opens a pty stream to the running shell of the pty job ref names
// and reads the agent's acceptance and attached frame. A job that finished,
// or whose shell the agent no longer has, is NOT_FOUND.
func (h *Hub) Attach(ctx context.Context, ref Ref) (*Relay, error) {
	j, err := h.find(ref)
	if err != nil {
		return nil, err
	}
	if j.FinishedAt != nil {
		return nil, ended(j)
	}
	if j.RemoteJob == "" {
		return nil, apierr.New(wire.CodeInProgress, fmt.Sprintf("터미널 job %s가 아직 열리는 중입니다. 잠시 뒤 다시 붙으세요", j.JobID), true, apierr.NextRetry)
	}
	host := h.opt.Conns.Get(j.HostID)
	if host == nil {
		return nil, apierr.NotFound(fmt.Sprintf("터미널 job %s의 PC %s가 등록돼 있지 않습니다. host.list로 확인하세요", j.JobID, j.HostID))
	}
	st, _, err := host.OpenStream(ctx, wire.StreamHeader{S: "pty", Job: j.RemoteJob})
	if err != nil {
		return nil, err
	}
	accept, r, err := readAcceptance(st)
	if err == nil && accept["ok"] != true {
		st.CancelRead()
		_ = st.CloseWrite()
		e := refusal(accept)
		if e.Code == wire.CodeNotFound {
			h.Refresh(ctx, j.HostID)
			if cur, err := h.opt.Store.GetJob(j.JobID); err == nil && cur.FinishedAt != nil {
				return nil, ended(cur)
			}
		}
		return nil, e
	}
	var kind byte
	var payload []byte
	if err == nil {
		_ = st.SetReadDeadline(time.Now().Add(lineTimeout))
		kind, payload, err = wire.ReadFrame(r, wire.MaxFrame)
		_ = st.SetReadDeadline(time.Time{})
	}
	var a wire.PTYAttached
	if err == nil && (kind != wire.KindPTYAttached || json.Unmarshal(payload, &a) != nil) {
		err = fmt.Errorf("the first frame of the agent's pty stream is %#x, not attached", kind)
	}
	if err != nil {
		st.CancelRead()
		st.CancelWrite()
		return nil, apierr.New(wire.CodeOffline, fmt.Sprintf("PC와의 터미널 스트림이 끊겼습니다(%v). 잠시 뒤 다시 붙으세요", err), true, apierr.NextRetry)
	}
	return &Relay{h: h, job: j.JobID, hostID: j.HostID, pid: a.PID, st: st, r: r}, nil
}

// Close ends the relay's agent stream; the shell keeps running.
func (r *Relay) Close() {
	r.st.CancelRead()
	_ = r.st.CloseWrite()
}

// deadliner is a client stream whose writes can be bounded in time.
type deadliner interface {
	SetWriteDeadline(time.Time) error
}

// Run writes the attached frame with the daemon's job id to client, then
// relays frames until one side ends: data from the agent and its exit go
// to client, data and resize frames from client go to the agent. The exit
// finishes the job by its code. It closes client and the agent stream.
func (r *Relay) Run(client io.ReadWriteCloser) {
	defer client.Close()
	defer r.Close()
	write := func(kind byte, payload []byte) error {
		if d, ok := client.(deadliner); ok {
			_ = d.SetWriteDeadline(time.Now().Add(writeTimeout))
		}
		return wire.WriteFrame(client, kind, payload)
	}
	attached, _ := json.Marshal(wire.PTYAttached{Job: r.job, PID: r.pid})
	if write(wire.KindPTYAttached, attached) != nil {
		return
	}
	fromClient := make(chan struct{})
	go func() {
		defer close(fromClient)
		r.fromClient(client)
		_ = r.st.CloseWrite()
		time.AfterFunc(detachWait, r.st.CancelRead)
	}()
	r.toClient(write)
	_ = client.Close()
	<-fromClient
}

// toClient copies the agent's data and exit frames to the client until the
// agent's stream or a client write ends.
func (r *Relay) toClient(write func(byte, []byte) error) {
	for {
		kind, payload, err := wire.ReadFrame(r.r, wire.MaxFrame)
		if err != nil {
			return
		}
		switch kind {
		case wire.KindPTYData:
			if write(kind, payload) != nil {
				return
			}
		case wire.KindPTYExit:
			var e wire.PTYExit
			if err := json.Unmarshal(payload, &e); err == nil {
				if _, err := r.h.opt.Jobs.Exited(r.job, e.Code); err != nil {
					r.h.opt.Logger.Printf("pty %s: record the exit: %v", r.job, err)
				}
			}
			_ = write(kind, payload)
			return
		}
	}
}

// fromClient copies the client's data and resize frames to the agent until
// the client's stream or an agent write ends; other kinds and resize
// frames outside 1–MaxSize are dropped.
func (r *Relay) fromClient(client io.Reader) {
	br := bufio.NewReader(client)
	for {
		kind, payload, err := wire.ReadFrame(br, maxClientFrame)
		if err != nil {
			return
		}
		switch kind {
		case wire.KindPTYData:
		case wire.KindPTYResize:
			var size wire.PTYResize
			if json.Unmarshal(payload, &size) != nil || size.Cols < 1 || size.Rows < 1 || size.Cols > MaxSize || size.Rows > MaxSize {
				continue
			}
			payload, _ = json.Marshal(size)
		default:
			continue
		}
		_ = r.st.SetWriteDeadline(time.Now().Add(writeTimeout))
		if wire.WriteFrame(r.st, kind, payload) != nil {
			return
		}
	}
}

// Refresh reads pty.list from hostID's agent once and finishes the host's
// running pty jobs that ended: an exited shell by its exit code, a shell
// the agent no longer lists as unknown. It runs when the host becomes
// ready.
func (h *Hub) Refresh(ctx context.Context, hostID string) {
	rows, err := h.opt.Store.ListJobs(store.JobFilter{HostID: hostID, Kind: jobs.KindPTY, Unfinished: true, All: true})
	if err != nil {
		return
	}
	var running []store.Job
	for _, j := range rows {
		if j.RemoteJob != "" {
			running = append(running, j)
		}
	}
	host := h.opt.Conns.Get(hostID)
	if len(running) == 0 || host == nil {
		return
	}
	if st := host.State(); st != conn.StateReady && st != conn.StateDegraded {
		return
	}
	lctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	res, err := host.Call(lctx, "pty.list", nil, conn.CallOpts{})
	if err != nil {
		return
	}
	byJob := map[string]proto.Message{}
	for _, item := range listed(res) {
		byJob[item.String("job")] = item
	}
	for _, j := range running {
		item, ok := byJob[j.RemoteJob]
		switch {
		case ok && item.String("state") == "exited":
			_, _ = h.opt.Jobs.Exited(j.JobID, item.Int("code"))
		case !ok:
			result := map[string]any{}
			_ = json.Unmarshal(j.Result, &result)
			result["state"] = "lost"
			result["error"] = apierr.New(wire.CodeExecutionOutcomeUnknown,
				"에이전트가 다시 시작돼 셸이 끝났습니다. 셸에서 하던 일을 확인한 뒤 새 터미널을 여세요", false, apierr.NextCheckState)
			_, _ = h.opt.Jobs.Finish(j.JobID, model.StatusUnknown, result)
		}
	}
}
