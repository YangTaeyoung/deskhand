// Package hosts is the daemon's host registry: it pairs PCs over SSH,
// stores them, keeps their connections and builds the Host model of the
// local API.
package hosts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/ids"
	"github.com/YangTaeyoung/deskhand/internal/daemon/jobs"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/paths"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/install"
	"github.com/YangTaeyoung/deskhand/internal/transport"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// connectWait bounds how long add, repair and connect wait for the host to
// connect.
const connectWait = 10 * time.Second

// Options configure a Registry.
type Options struct {
	Store    *store.Store
	Bus      *events.Bus
	Manager  *conn.Manager
	Jobs     *jobs.Registry
	Identity transport.Identity
	// SSHExec runs ssh and scp; nil runs the real commands.
	SSHExec install.Runner
	// AgentBinary returns the agent binary for an architecture; nil means
	// FindAgentBinary.
	AgentBinary func(arch string) (string, error)
	// Version is the daemon version that host.doctor compares the agent's with.
	Version string
	// ConnectWait bounds the wait for a connection after add, repair and
	// connect (10 s when 0).
	ConnectWait time.Duration
	Logger      *log.Logger
}

// Registry keeps the paired hosts.
type Registry struct {
	opt Options

	// pairMu runs one pairing at a time.
	pairMu sync.Mutex

	mu   sync.Mutex
	prev map[string]conn.ConnState
}

// New returns a Registry.
func New(opt Options) *Registry {
	if opt.AgentBinary == nil {
		opt.AgentBinary = FindAgentBinary
	}
	if opt.ConnectWait <= 0 {
		opt.ConnectWait = connectWait
	}
	if opt.Logger == nil {
		opt.Logger = log.New(io.Discard, "", 0)
	}
	return &Registry{opt: opt, prev: map[string]conn.ConnState{}}
}

func target(h store.Host) conn.Target {
	return conn.Target{HostID: h.HostID, Name: h.Name, Address: h.Address, Port: h.Port, Fingerprint: h.AgentFP}
}

// Load registers every stored host with the connection manager and starts
// connecting it.
func (r *Registry) Load() error {
	rows, err := r.opt.Store.ListHosts()
	if err != nil {
		return err
	}
	for _, row := range rows {
		r.opt.Manager.Add(target(row)).Connect()
	}
	return nil
}

func (r *Registry) ssh(alias string) install.SSH {
	return install.SSH{Alias: alias, Exec: r.opt.SSHExec}
}

// Resolve returns the stored host whose id or name is ref.
func (r *Registry) Resolve(ref string) (store.Host, error) {
	if ref == "" {
		return store.Host{}, apierr.Invalid("host_id가 없습니다. host.list로 PC의 host_id나 이름을 확인해 주세요")
	}
	h, err := r.opt.Store.GetHost(ref)
	if errors.Is(err, store.ErrNotFound) {
		h, err = r.opt.Store.HostByName(ref)
	}
	if errors.Is(err, store.ErrNotFound) {
		return store.Host{}, apierr.NotFound(fmt.Sprintf("'%s' PC가 등록돼 있지 않습니다. host.list로 이름을 확인하거나 host.add로 추가하세요", ref))
	}
	if err != nil {
		return store.Host{}, apierr.Internal(fmt.Sprintf("PC 목록을 읽지 못했습니다(%v). 데몬 로그를 확인하세요", err))
	}
	return h, nil
}

// Conn returns the stored host ref and its connection.
func (r *Registry) Conn(ref string) (*conn.Host, store.Host, error) {
	row, err := r.Resolve(ref)
	if err != nil {
		return nil, store.Host{}, err
	}
	h := r.opt.Manager.Get(row.HostID)
	if h == nil {
		h = r.opt.Manager.Add(target(row))
	}
	return h, row, nil
}

// Model returns the Host model of a stored host with its connection state.
func (r *Registry) Model(row store.Host) model.Host {
	m := model.Host{HostID: row.HostID, Name: row.Name, OS: row.OS, Arch: row.Arch, Address: row.Address, Port: row.Port,
		SSHAlias: row.SSHAlias, State: string(conn.StateOffline), Caps: map[string]model.Capability{}, Displays: []any{},
		Owner: model.OwnerNone, LastSeen: model.Time(row.LastSeen)}
	h := r.opt.Manager.Get(row.HostID)
	if h == nil {
		return m
	}
	st := h.State()
	m.State, m.ConnectionGeneration = string(st), h.Generation()
	hello := h.Hello()
	if hello.Agent != "" {
		m.AgentVersion = hello.Agent
		for k, c := range hello.Caps {
			m.Caps[k] = model.Capability{State: c.State, Reason: c.Reason}
		}
		if hello.Displays != nil {
			m.Displays = hello.Displays
		}
		m.Session = sessionInfo(hello.Session, h.SessionInfo())
	}
	if seen := h.LastSeen(); seen.After(row.LastSeen) {
		m.LastSeen = model.Time(seen)
	}
	if err := h.LastError(); err != nil && st != conn.StateReady {
		m.Reason = apierr.As(err).Message
	}
	return m
}

// List returns every host.
func (r *Registry) List() ([]model.Host, error) {
	rows, err := r.opt.Store.ListHosts()
	if err != nil {
		return nil, apierr.Internal(fmt.Sprintf("PC 목록을 읽지 못했습니다(%v). 데몬 로그를 확인하세요", err))
	}
	out := make([]model.Host, 0, len(rows))
	for _, row := range rows {
		out = append(out, r.Model(row))
	}
	return out, nil
}

// Get returns the host ref.
func (r *Registry) Get(ref string) (model.Host, error) {
	row, err := r.Resolve(ref)
	if err != nil {
		return model.Host{}, err
	}
	return r.Model(row), nil
}

// SessionID returns the envelope session id of the host's desktop session,
// or "" when the session is not known.
func (r *Registry) SessionID(hostID string) string {
	h := r.opt.Manager.Get(hostID)
	if h == nil {
		return ""
	}
	s := sessionInfo(h.Hello().Session, h.SessionInfo())
	if s == nil {
		return ""
	}
	return fmt.Sprintf("%s%s-%d", ids.Session, hostID, s.SessionID)
}

// hostID returns an id for name: name in lower case with spaces as '-' when
// it has only ASCII letters, digits, '.', '_', '-' and spaces, else "h-"
// and a ULID; a taken id gets a "-2", "-3", … suffix.
func (r *Registry) hostID(name string) string {
	var b strings.Builder
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			b.WriteRune(c)
		case c >= 'A' && c <= 'Z':
			b.WriteRune(c + 'a' - 'A')
		case c == ' ':
			b.WriteRune('-')
		default:
			return ids.New(ids.Host)
		}
	}
	base := b.String()
	if base == "" || base == "." || base == ".." {
		return ids.New(ids.Host)
	}
	id := base
	for n := 2; ; n++ {
		if _, err := r.opt.Store.GetHost(id); errors.Is(err, store.ErrNotFound) {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

// AddParams are the parameters of host.add.
type AddParams struct {
	Name    string
	SSH     string
	Address string
	Port    int
	// Force pairs a PC whose agent is already paired again, with a new
	// agent key.
	Force bool
}

// installSteps describes the steps of install.Pair.
var installSteps = map[string]string{
	install.StepProbe:   "PC 확인",
	install.StepCopy:    "에이전트 복사",
	install.StepInit:    "에이전트 키 만들기",
	install.StepInstall: "에이전트 설치",
	install.StepVerify:  "설치 확인",
}

// progress publishes host.install.progress for the steps of one pairing.
type progress struct {
	bus     *events.Bus
	hostID  string
	jobID   string
	name    string
	current string
}

func (p *progress) publish(step, status string, e *apierr.Error) {
	f := map[string]any{"name": p.name, "step": step, "status": status, "detail": installSteps[step]}
	if e != nil {
		ef := map[string]any{"code": e.Code, "message": e.Message}
		if e.NextAction != "" {
			ef["next_action"] = e.NextAction
		}
		f["error"] = ef
	}
	p.bus.Publish(p.hostID, p.jobID, events.InstallProgress, f)
}

// step marks the running step done and step active.
func (p *progress) step(step string) {
	if p.current != "" {
		p.publish(p.current, "done", nil)
	}
	p.current = step
	p.publish(step, "active", nil)
}

func (p *progress) done() {
	if p.current != "" {
		p.publish(p.current, "done", nil)
	}
}

func (p *progress) fail(e *apierr.Error) {
	step := p.current
	if step == "" {
		step = install.StepProbe
	}
	p.publish(step, "failed", e)
}

// shellWord quotes s with single quotes when it holds a space or a tab.
func shellWord(s string) string {
	if strings.ContainsAny(s, " \t") {
		return "'" + s + "'"
	}
	return s
}

// pairError is the API error of a failed install.Pair of the host name over
// the SSH alias; started tells whether a step began. An agent that is
// already paired answers with the host add --force command for name and
// alias.
func pairError(err error, started bool, name, alias string) *apierr.Error {
	switch {
	case errors.Is(err, install.ErrAlreadyPaired):
		return apierr.New(wire.CodeInvalidArgument,
			fmt.Sprintf("이 PC에는 이미 Deskhand 에이전트가 페어링돼 있습니다. 이 Mac과 다시 페어링하려면 deskhand host add %s --ssh %s --force 를 실행하세요",
				shellWord(name), alias), false, apierr.NextHostAddForce)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return apierr.New(wire.CodeTimeout, "PC 설치가 끝나기 전에 요청이 취소됐습니다. 다시 실행하세요", true, apierr.NextRetry)
	case !started:
		return apierr.Invalid(err.Error())
	}
	return apierr.New(wire.CodeInternal, err.Error(), false, "")
}

// pair runs install.Pair for the host with progress events and an install
// job; the job is finished with the outcome.
func (r *Registry) pair(ctx context.Context, hostID, name, alias, summary string, force bool, c model.Caller) (install.PairResult, model.Job, error) {
	job, err := r.opt.Jobs.Create(hostID, jobs.KindInstall, "", summary, map[string]any{"name": name, "ssh": alias, "force": force})
	if err != nil {
		return install.PairResult{}, model.Job{}, err
	}
	p := &progress{bus: r.opt.Bus, hostID: hostID, jobID: job.JobID, name: name}
	res, err := install.Pair(ctx, r.ssh(alias), name, r.opt.Identity, r.opt.AgentBinary, p.step, install.PairOptions{Force: force})
	if err != nil {
		e := pairError(err, p.current != "", name, alias)
		p.fail(e)
		job, _ = r.opt.Jobs.Finish(job.JobID, model.StatusFailed, map[string]any{"error": e})
		_, _ = r.opt.Bus.Record(c.Entry(hostID, model.TimelineInstall, summary+" 실패: "+e.Message, model.TimelineFailed, job.JobID))
		return install.PairResult{}, job, e
	}
	p.done()
	return res, job, nil
}

// waitConnected waits until the host is ready, degraded or blocked, at most
// ConnectWait.
func (r *Registry) waitConnected(ctx context.Context, h *conn.Host) {
	wctx, cancel := context.WithTimeout(ctx, r.opt.ConnectWait)
	defer cancel()
	_ = h.WaitState(wctx, func(s conn.ConnState) bool {
		return s == conn.StateReady || s == conn.StateDegraded || s == conn.StateBlocked
	})
}

// Add pairs a PC over SSH, stores it and connects to it.
func (r *Registry) Add(ctx context.Context, p AddParams, c model.Caller) (model.Host, model.Job, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return model.Host{}, model.Job{}, apierr.Invalid("name이 없습니다. PC 이름을 주세요")
	}
	if p.SSH == "" {
		return model.Host{}, model.Job{}, apierr.Invalid("ssh가 없습니다. ~/.ssh/config의 호스트 별칭을 주세요")
	}
	if p.Port < 0 || p.Port > 65535 {
		return model.Host{}, model.Job{}, apierr.Invalid("port는 1~65535여야 합니다. 포트를 고쳐서 다시 실행하세요")
	}
	r.pairMu.Lock()
	defer r.pairMu.Unlock()
	if _, err := r.opt.Store.HostByName(p.Name); err == nil {
		return model.Host{}, model.Job{}, apierr.New(wire.CodeInvalidArgument,
			fmt.Sprintf("'%s' 이름의 PC가 이미 등록돼 있습니다. 다른 이름을 쓰거나 deskhand host repair %s을 실행하세요", p.Name, p.Name),
			false, apierr.NextHostRepair)
	}
	hostID := r.hostID(p.Name)
	summary := "PC 추가: " + p.Name
	res, job, err := r.pair(ctx, hostID, p.Name, p.SSH, summary, p.Force, c)
	if err != nil {
		return model.Host{}, job, err
	}
	row := store.Host{HostID: hostID, Name: p.Name, Address: res.Address, Port: res.Port, AgentFP: res.AgentFingerprint,
		OS: res.OS, Arch: res.Arch, SSHAlias: p.SSH, CreatedAt: time.Now()}
	if p.Address != "" {
		row.Address = p.Address
	}
	if p.Port > 0 {
		row.Port = p.Port
	}
	if err := r.opt.Store.PutHost(row); err != nil {
		e := apierr.Internal(fmt.Sprintf("페어링한 PC를 저장하지 못했습니다(%v). 데몬 로그를 확인하고 deskhand host repair %s을 실행하세요", err, p.Name))
		job, _ = r.opt.Jobs.Finish(job.JobID, model.StatusFailed, map[string]any{"error": e})
		return model.Host{}, job, e
	}
	_ = os.MkdirAll(paths.HostDir(hostID), 0o700)
	h := r.opt.Manager.Add(target(row))
	h.Connect()
	r.waitConnected(ctx, h)
	m := r.Model(row)
	job, _ = r.opt.Jobs.Finish(job.JobID, model.StatusSucceeded, map[string]any{"host_id": hostID, "state": m.State})
	_, _ = r.opt.Bus.Record(c.Entry(hostID, model.TimelineInstall, summary, model.TimelineOK, job.JobID))
	r.opt.Bus.Publish(hostID, "", events.HostAdded, map[string]any{"host": m})
	return m, job, nil
}

// Repair pairs the host again with a new agent key, stores the new
// fingerprint and reconnects.
func (r *Registry) Repair(ctx context.Context, ref string, c model.Caller) (model.Host, model.Job, error) {
	row, err := r.Resolve(ref)
	if err != nil {
		return model.Host{}, model.Job{}, err
	}
	if row.SSHAlias == "" {
		return model.Host{}, model.Job{}, apierr.Invalid(fmt.Sprintf("%s에는 SSH 별칭이 없어 다시 페어링할 수 없습니다. host.remove 후 host.add로 다시 추가하세요", row.Name))
	}
	r.pairMu.Lock()
	defer r.pairMu.Unlock()
	summary := "PC 다시 페어링: " + row.Name
	res, job, err := r.pair(ctx, row.HostID, row.Name, row.SSHAlias, summary, true, c)
	if err != nil {
		return model.Host{}, job, err
	}
	h := r.opt.Manager.Get(row.HostID)
	if h != nil {
		h.Disconnect()
	}
	row.AgentFP, row.OS, row.Arch = res.AgentFingerprint, res.OS, res.Arch
	if row.Address == "" {
		row.Address = res.Address
	}
	if row.Port == 0 {
		row.Port = res.Port
	}
	if err := r.opt.Store.UpdateHost(row); err != nil {
		e := apierr.Internal(fmt.Sprintf("새 지문을 저장하지 못했습니다(%v). 데몬 로그를 확인하고 다시 실행하세요", err))
		job, _ = r.opt.Jobs.Finish(job.JobID, model.StatusFailed, map[string]any{"error": e})
		return model.Host{}, job, e
	}
	h = r.opt.Manager.Add(target(row))
	h.Connect()
	r.waitConnected(ctx, h)
	m := r.Model(row)
	job, _ = r.opt.Jobs.Finish(job.JobID, model.StatusSucceeded, map[string]any{"host_id": row.HostID, "state": m.State})
	_, _ = r.opt.Bus.Record(c.Entry(row.HostID, model.TimelineInstall, summary, model.TimelineOK, job.JobID))
	r.opt.Bus.Publish(row.HostID, "", events.HostUpdated, map[string]any{"host": m})
	return m, job, nil
}

// Remove disconnects and forgets the host; with uninstall it first removes
// the agent and its key and data from the PC over SSH.
func (r *Registry) Remove(ctx context.Context, ref string, uninstall bool, c model.Caller) error {
	row, err := r.Resolve(ref)
	if err != nil {
		return err
	}
	if uninstall {
		if row.SSHAlias == "" {
			return apierr.Invalid(fmt.Sprintf("%s에는 SSH 별칭이 없어 에이전트를 지울 수 없습니다. uninstall 없이 다시 실행하세요", row.Name))
		}
		if err := install.Unpair(ctx, r.ssh(row.SSHAlias), true); err != nil {
			_, _ = r.opt.Bus.Record(c.Entry(row.HostID, model.TimelineInstall, "에이전트 제거 실패: "+row.Name, model.TimelineFailed, ""))
			return apierr.New(wire.CodeInternal, err.Error(), true, apierr.NextRetry)
		}
	}
	r.opt.Manager.Remove(row.HostID)
	if err := r.opt.Store.DeleteHost(row.HostID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return apierr.Internal(fmt.Sprintf("PC를 목록에서 지우지 못했습니다(%v). 데몬 로그를 확인하세요", err))
	}
	_ = os.RemoveAll(paths.HostDir(row.HostID))
	r.mu.Lock()
	delete(r.prev, row.HostID)
	r.mu.Unlock()
	summary := "PC 제거: " + row.Name
	if uninstall {
		summary += " (에이전트 제거)"
	}
	_, _ = r.opt.Bus.Record(c.Entry(row.HostID, model.TimelineInstall, summary, model.TimelineOK, ""))
	r.opt.Bus.Publish(row.HostID, "", events.HostRemoved, map[string]any{})
	return nil
}

// Connect starts connecting the host, or tries a blocked host once more,
// and waits up to ConnectWait for the outcome.
func (r *Registry) Connect(ctx context.Context, ref string) (model.Host, error) {
	h, row, err := r.Conn(ref)
	if err != nil {
		return model.Host{}, err
	}
	h.Connect()
	r.waitConnected(ctx, h)
	return r.Model(row), nil
}

// Disconnect closes the host's connection and stops reconnecting.
func (r *Registry) Disconnect(ref string) (model.Host, error) {
	h, row, err := r.Conn(ref)
	if err != nil {
		return model.Host{}, err
	}
	h.Disconnect()
	return r.Model(row), nil
}

// HandleConnEvent turns a connection event into local API events and
// timeline entries.
func (r *Registry) HandleConnEvent(ev conn.Event) {
	switch ev.Kind {
	case conn.KindConnectionState:
		r.connectionState(ev)
	case conn.KindAgentEvent:
		r.agentEvent(ev)
	}
}

func (r *Registry) connectionState(ev conn.Event) {
	fields := map[string]any{"state": ev.Payload["state"], "connection_generation": ev.Payload["connection_generation"]}
	reason := ev.Payload.String("reason")
	if reason != "" {
		fields["reason"] = reason
	}
	r.opt.Bus.Publish(ev.HostID, "", events.ConnectionState, fields)
	st := conn.ConnState(ev.Payload.String("state"))
	r.mu.Lock()
	prev := r.prev[ev.HostID]
	r.prev[ev.HostID] = st
	r.mu.Unlock()
	row, err := r.opt.Store.GetHost(ev.HostID)
	if err != nil {
		return
	}
	var summary, outcome string
	switch {
	case st == conn.StateReady && prev != conn.StateDegraded:
		summary = fmt.Sprintf("%s 연결됨 (세대 %v)", row.Name, ev.Payload["connection_generation"])
		outcome = model.TimelineOK
		_ = r.opt.Store.TouchHost(ev.HostID, time.Now())
	case st == conn.StateBlocked:
		summary, outcome = fmt.Sprintf("%s 연결 차단: %s", row.Name, reason), model.TimelineFailed
	case st == conn.StateReconnecting && (prev == conn.StateReady || prev == conn.StateDegraded):
		summary, outcome = fmt.Sprintf("%s 연결 끊김", row.Name), model.TimelineFailed
	}
	if summary != "" {
		_, _ = r.opt.Bus.Record(model.System.Entry(ev.HostID, model.TimelineConnection, summary, outcome, ""))
	}
	if st == conn.StateReady || st == conn.StateBlocked {
		r.opt.Bus.Publish(ev.HostID, "", events.HostUpdated, map[string]any{"host": r.Model(row)})
	}
}

func (r *Registry) agentEvent(ev conn.Event) {
	h := r.opt.Manager.Get(ev.HostID)
	switch ev.Payload.String("name") {
	case "caps.changed":
		caps := map[string]model.Capability{}
		if h != nil {
			for k, c := range h.Hello().Caps {
				caps[k] = model.Capability{State: c.State, Reason: c.Reason}
			}
		}
		r.opt.Bus.Publish(ev.HostID, "", events.CapsChanged, map[string]any{"caps": caps})
	case "session.changed":
		if h == nil {
			return
		}
		if s := sessionInfo(h.Hello().Session, h.SessionInfo()); s != nil {
			r.opt.Bus.Publish(ev.HostID, "", events.SessionChanged, map[string]any{"session": s})
		}
	case "display.changed":
		displays, _ := ev.Payload["displays"].([]any)
		if displays == nil {
			displays = []any{}
		}
		r.opt.Bus.Publish(ev.HostID, "", events.DisplayChanged, map[string]any{"displays": displays})
	case "job.exited":
		r.opt.Jobs.HandleAgentEvent(ev.HostID, ev.Payload)
	}
}
