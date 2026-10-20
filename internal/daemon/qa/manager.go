// Package qa runs QA scenarios on a host: it parses a scenario, runs its
// steps through the agent, records each step's assertions and evidence and
// writes the result bundle (docs/local-api.md §10).
package qa

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/artifacts"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/ids"
	"github.com/YangTaeyoung/deskhand/internal/daemon/jobs"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// maxScenarioBytes bounds a scenario.
	maxScenarioBytes = 1 << 20
	// defaultPoll is the wait between two checks of a polling step.
	defaultPoll = 300 * time.Millisecond
	// listDefault and listMax are the page sizes of List.
	listDefault = 50
	listMax     = 500
)

// Causes that end the context of a run.
var (
	errCancelled   = errors.New("the run was cancelled")
	errRunTimeout  = errors.New("the run timeout ended")
	errStepTimeout = errors.New("the step timeout ended")
	errClosing     = errors.New("the daemon is closing")
)

// Options configure a Manager. Push, Control and Input may be nil: without
// Push and Input their steps are blocked, without Control nobody holds
// control.
type Options struct {
	Store   *store.Store
	Bus     *events.Bus
	Jobs    *jobs.Registry
	Hosts   Hosts
	Push    Pusher
	Control Control
	Input   Input
	// Version is the daemon version a report names.
	Version string
	Logger  *log.Logger
	// Poll is the wait between two checks of a polling step; 0 means
	// 300 ms.
	Poll time.Duration
}

// StartParams are the parameters of qa.run: the scenario file Path on this
// Mac or the scenario Text, with BaseDir for the relative paths of Text.
type StartParams struct {
	HostRef   string
	Path      string
	Text      []byte
	BaseDir   string
	Vars      map[string]string
	TimeoutMS int64
}

// Manager owns the runs.
type Manager struct {
	opt  Options
	lg   *log.Logger
	poll time.Duration

	// base ends with Close; every run runs under it.
	base context.Context
	stop context.CancelCauseFunc
	once sync.Once
	wg   sync.WaitGroup

	mu     sync.Mutex
	live   map[string]*runner
	busy   map[string]string
	closed bool
}

// New returns a Manager; Load settles the runs a stopped daemon left.
func New(opt Options) *Manager {
	lg := opt.Logger
	if lg == nil {
		lg = log.New(io.Discard, "", 0)
	}
	if opt.Control == nil {
		opt.Control = NoOwner{}
	}
	poll := opt.Poll
	if poll <= 0 {
		poll = defaultPoll
	}
	m := &Manager{opt: opt, lg: lg, poll: poll, live: map[string]*runner{}, busy: map[string]string{}}
	m.base, m.stop = context.WithCancelCause(context.Background())
	return m
}

// NoOwner is the Control of a daemon whose hosts nobody holds.
type NoOwner struct{}

// Check always lets the input go.
func (NoOwner) Check(context.Context, string, model.Caller) error { return nil }

// Close stops the runs without recording an end for them and waits for
// them; Load ends them when the daemon starts again.
func (m *Manager) Close() {
	m.once.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
		m.stop(errClosing)
	})
	m.wg.Wait()
}

func (m *Manager) closing() bool { return context.Cause(m.base) == errClosing }

// record is what the store keeps of a run: the run and what its report
// names.
type record struct {
	Run         Run               `json:"run"`
	Scenario    scenarioInfo      `json:"scenario"`
	Vars        map[string]string `json:"vars"`
	Host        hostInfo          `json:"host"`
	Environment map[string]any    `json:"environment,omitempty"`
	Daemon      string            `json:"daemon_version"`
}

type scenarioInfo struct {
	Path   string `json:"path,omitempty"`
	SHA256 string `json:"sha256"`
	Source string `json:"source"`
}

type hostInfo struct {
	HostID       string `json:"host_id"`
	Name         string `json:"name"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Address      string `json:"address"`
	AgentVersion string `json:"agent_version,omitempty"`
}

// clone returns a copy of run with fresh slices and maps, as a client
// reads it.
func clone(run Run) Run {
	b, err := json.Marshal(run)
	if err != nil {
		return run
	}
	var out Run
	if json.Unmarshal(b, &out) != nil {
		return run
	}
	if out.Unsupported == nil {
		out.Unsupported = []Unsupported{}
	}
	return out
}

func decodeRecord(row store.QARun) (record, error) {
	var rec record
	if err := json.Unmarshal(row.Data, &rec); err != nil {
		return record{}, apierr.Internal(fmt.Sprintf("QA 실행 %s의 기록을 읽지 못했습니다(%v). 데몬 로그를 확인하세요", row.RunID, err))
	}
	if rec.Run.Unsupported == nil {
		rec.Run.Unsupported = []Unsupported{}
	}
	return rec, nil
}

func notFound(runID string) *apierr.Error {
	return apierr.NotFound(fmt.Sprintf("QA 실행 %s가 없습니다. qa.list로 run_id를 확인하세요", runID))
}

// Start checks a scenario, creates its run and job and starts it.
func (m *Manager) Start(ctx context.Context, p StartParams, c model.Caller) (Run, error) {
	if p.TimeoutMS < 0 {
		return Run{}, apierr.Invalid("timeout_ms는 0 이상이어야 합니다. 값을 고쳐 다시 보내세요")
	}
	if (p.Path != "") == (len(p.Text) > 0) {
		return Run{}, apierr.Invalid("qa.run에는 scenario_path와 scenario 중 하나만 주세요")
	}
	if p.BaseDir != "" && !filepath.IsAbs(p.BaseDir) {
		return Run{}, apierr.Invalid(fmt.Sprintf("base_dir %q는 이 Mac의 절대 경로여야 합니다", p.BaseDir))
	}
	src, file, dir := p.Text, "scenario", p.BaseDir
	if p.Path != "" {
		if !filepath.IsAbs(p.Path) {
			return Run{}, apierr.Invalid(fmt.Sprintf("scenario_path %q는 이 Mac의 절대 경로여야 합니다", p.Path))
		}
		data, err := readScenario(p.Path)
		if err != nil {
			return Run{}, err
		}
		src, file, dir = data, p.Path, filepath.Dir(p.Path)
	}
	if len(src) > maxScenarioBytes {
		return Run{}, apierr.Invalid(fmt.Sprintf("시나리오가 %d바이트를 넘습니다. 시나리오를 나누세요", maxScenarioBytes))
	}
	runID := ids.New(ids.QARun)
	sc, err := Parse(src, ParseOptions{File: file, Dir: dir, Vars: p.Vars, RunID: runID})
	if err != nil {
		return Run{}, apierr.Invalid(err.Error())
	}
	ref := p.HostRef
	if ref == "" {
		ref = sc.Host
	}
	if ref == "" {
		return Run{}, apierr.Invalid("실행할 PC가 없습니다. qa.run의 host_id나 시나리오의 host를 주세요")
	}
	host, err := m.opt.Hosts.Resolve(ref)
	if err != nil {
		return Run{}, err
	}
	timeout := p.TimeoutMS
	if timeout == 0 {
		timeout = sc.TimeoutMS
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Run{}, apierr.New(wire.CodeOffline, "데몬이 끝나는 중입니다. 데몬이 다시 시작한 뒤 실행하세요", true, apierr.NextRetry)
	}
	if other := m.busy[host.HostID]; other != "" {
		m.mu.Unlock()
		return Run{}, apierr.New(wire.CodeInProgress, fmt.Sprintf("%s PC에서 QA 실행 %s가 진행 중입니다. 끝날 때까지 기다리거나 qa.cancel로 멈춘 뒤 다시 실행하세요", host.Name, other),
			true, apierr.NextCheckState)
	}
	m.busy[host.HostID] = runID
	m.mu.Unlock()
	r, err := m.create(runID, sc, src, p, host, timeout, c)
	if err != nil {
		m.mu.Lock()
		delete(m.busy, host.HostID)
		m.mu.Unlock()
		return Run{}, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Run{}, apierr.New(wire.CodeOffline, "데몬이 끝나는 중입니다. 데몬이 다시 시작한 뒤 실행 결과를 qa.get으로 확인하세요", true, apierr.NextCheckState)
	}
	m.live[runID] = r
	m.wg.Add(1)
	m.mu.Unlock()
	run := r.snapshot(true)
	go r.execute()
	return run, nil
}

func readScenario(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, apierr.NotFound(fmt.Sprintf("시나리오 파일 %s가 없습니다. 경로를 확인하세요", path))
	}
	if err != nil {
		return nil, apierr.Invalid(fmt.Sprintf("시나리오 파일 %s를 읽지 못했습니다(%v). 권한을 확인하세요", path, err))
	}
	if fi.IsDir() {
		return nil, apierr.Invalid(fmt.Sprintf("%s는 폴더입니다. 시나리오 YAML 파일을 주세요", path))
	}
	if fi.Size() > maxScenarioBytes {
		return nil, apierr.Invalid(fmt.Sprintf("시나리오 파일 %s가 %d바이트를 넘습니다. 시나리오를 나누세요", path, maxScenarioBytes))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, apierr.Invalid(fmt.Sprintf("시나리오 파일 %s를 읽지 못했습니다(%v). 권한을 확인하세요", path, err))
	}
	return data, nil
}

// create stores the run of sc with its job and returns its runner.
func (m *Manager) create(runID string, sc *Scenario, src []byte, p StartParams, host Host, timeout int64, c model.Caller) (*runner, error) {
	now := time.Now()
	vars := sc.DisplayVars()
	name := sc.Scrub(sc.Name)
	jobID := runID
	if m.opt.Jobs != nil {
		params := map[string]any{"run_id": runID, "vars": vars}
		if p.Path != "" {
			params["scenario_path"] = p.Path
		}
		if timeout > 0 {
			params["timeout_ms"] = timeout
		}
		job, err := m.opt.Jobs.Create(host.HostID, KindQA, "", "QA: "+name, params)
		if err != nil {
			return nil, err
		}
		jobID = job.JobID
	}
	run := Run{RunID: runID, JobID: jobID, HostID: host.HostID, Name: name, ScenarioPath: p.Path, Status: RunRunning, Rev: 1,
		StartedAt: model.Time(now), UpdatedAt: model.Time(now), Unsupported: []Unsupported{}}
	for i, st := range sc.Steps {
		params, _ := sc.ScrubAny(st.Params).(map[string]any)
		run.Steps = append(run.Steps, StepResult{Index: i + 1, Phase: st.Phase, Name: sc.Scrub(st.Name), Action: st.Action, Line: st.Line,
			Params: params, Optional: st.Optional, ContinueOnFail: st.ContinueOnFail, Status: StepPending, Assertions: []Assertion{},
			Evidence: []model.Artifact{}})
	}
	count(&run)
	sum := sha256.Sum256(src)
	rec := record{Run: run, Scenario: scenarioInfo{Path: p.Path, SHA256: hex.EncodeToString(sum[:]), Source: sc.Scrub(string(src))},
		Vars: vars, Host: hostInfo{HostID: host.HostID, Name: host.Name, OS: host.OS, Arch: host.Arch, Address: host.Address,
			AgentVersion: host.AgentVersion}, Daemon: m.opt.Version}
	data, err := json.Marshal(rec)
	if err != nil {
		return nil, apierr.Internal(fmt.Sprintf("QA 실행을 기록하지 못했습니다(%v). 데몬 로그를 확인하세요", err))
	}
	if err := m.opt.Store.CreateQARun(store.QARun{RunID: runID, JobID: jobID, HostID: host.HostID, Name: name, Status: RunRunning, Rev: 1,
		Data: data, CreatedAt: now, UpdatedAt: now}); err != nil {
		if m.opt.Jobs != nil {
			_, _ = m.opt.Jobs.Finish(jobID, model.StatusFailed, map[string]any{"error": apierr.Internal(err.Error())})
		}
		return nil, apierr.Internal(fmt.Sprintf("QA 실행을 저장하지 못했습니다(%v). 데몬 로그를 확인하세요", err))
	}
	r := &runner{m: m, sc: sc, caller: c, host: host, agent: m.opt.Hosts.Agent(host.HostID), rec: rec, started: now,
		dir: artifacts.Dir(jobID), timeout: timeout, seenUnsupported: map[string]bool{}}
	r.mainCtx, r.cancelMain = context.WithCancelCause(m.base)
	if timeout > 0 {
		var stop context.CancelFunc
		r.mainCtx, stop = context.WithTimeoutCause(r.mainCtx, time.Duration(timeout)*time.Millisecond, errRunTimeout)
		r.stopTimer = stop
	}
	r.tdCtx, r.cancelTD = context.WithCancelCause(m.base)
	m.opt.Bus.Publish(host.HostID, jobID, events.QAUpdated, map[string]any{"run": summary(run)})
	_, _ = m.opt.Bus.Record(c.Entry(host.HostID, model.TimelineQA, "QA 시작: "+name, model.TimelineOK, jobID))
	return r, nil
}

// summary is the run without its steps.
func summary(run Run) Run {
	out := clone(run)
	out.Steps = nil
	return out
}

// count sets the step counts of run from its steps.
func count(run *Run) {
	run.StepsTotal = len(run.Steps)
	run.StepsPassed, run.StepsFailed, run.StepsBlocked, run.StepsNotRun = 0, 0, 0, 0
	for _, s := range run.Steps {
		switch s.Status {
		case StepPass:
			run.StepsPassed++
		case StepFail:
			run.StepsFailed++
		case StepBlocked:
			run.StepsBlocked++
		case StepNotRun:
			run.StepsNotRun++
		}
	}
}

// release frees the host of a runner for the next run.
func (m *Manager) release(r *runner) {
	m.mu.Lock()
	if m.busy[r.host.HostID] == r.rec.Run.RunID {
		delete(m.busy, r.host.HostID)
	}
	m.mu.Unlock()
}

// finished removes a runner whose run has ended.
func (m *Manager) finished(r *runner) {
	m.release(r)
	m.mu.Lock()
	delete(m.live, r.rec.Run.RunID)
	m.mu.Unlock()
	m.wg.Done()
}

// Get returns a run with its steps.
func (m *Manager) Get(runID string) (Run, error) {
	m.mu.Lock()
	r := m.live[runID]
	m.mu.Unlock()
	if r != nil {
		return r.snapshot(true), nil
	}
	row, err := m.opt.Store.GetQARun(runID)
	if errors.Is(err, store.ErrNotFound) {
		return Run{}, notFound(runID)
	}
	if err != nil {
		return Run{}, apierr.Internal(fmt.Sprintf("QA 실행 기록을 읽지 못했습니다(%v). 데몬 로그를 확인하세요", err))
	}
	rec, err := decodeRecord(row)
	if err != nil {
		return Run{}, err
	}
	return clone(rec.Run), nil
}

// List returns a page of the runs of hostID ("" for every host), newest
// first, without steps, and the cursor of the next page.
func (m *Manager) List(hostID string, limit int, cursor string) ([]Run, string, error) {
	if limit == 0 {
		limit = listDefault
	}
	if limit < 0 || limit > listMax {
		return nil, "", apierr.Invalid(fmt.Sprintf("limit은 1부터 %d까지입니다. 값을 고쳐 다시 보내세요", listMax))
	}
	if cursor != "" && !strings.HasPrefix(cursor, ids.QARun) {
		return nil, "", apierr.Invalid(fmt.Sprintf("cursor %q를 읽지 못했습니다. 이전 qa.list 결과의 next_cursor를 그대로 주세요", cursor))
	}
	rows, err := m.opt.Store.ListQARuns(store.QARunFilter{HostID: hostID, Before: cursor, Limit: limit + 1})
	if err != nil {
		return nil, "", apierr.Internal(fmt.Sprintf("QA 실행 목록을 읽지 못했습니다(%v). 데몬 로그를 확인하세요", err))
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = rows[limit-1].RunID
	}
	out := make([]Run, 0, len(rows))
	for _, row := range rows {
		rec, err := decodeRecord(row)
		if err != nil {
			return nil, "", err
		}
		out = append(out, summary(rec.Run))
	}
	return out, next, nil
}

// Cancel stops a running run: its running step ends blocked, the steps
// left are not run and the teardown runs; a later Cancel that arrives
// while the teardown runs stops the teardown too. A cancelled run is
// returned as it is; a run that ended otherwise is refused.
func (m *Manager) Cancel(runID string, c model.Caller) (Run, error) {
	m.mu.Lock()
	r := m.live[runID]
	m.mu.Unlock()
	if r != nil {
		return r.cancel(c)
	}
	run, err := m.Get(runID)
	if err != nil {
		return Run{}, err
	}
	if run.Status != RunCancelled && run.FinishedAt != "" {
		return Run{}, ended(run)
	}
	return run, nil
}

// ended is the refusal of a cancel of a run that has ended.
func ended(run Run) error {
	return apierr.Invalid(fmt.Sprintf("QA 실행 %s는 이미 %s 상태로 끝났습니다. 취소할 필요가 없습니다", run.RunID, runLabel(run.Status)))
}

// Load ends, when the daemon starts, the runs that had not finished: the
// running step is blocked with an unknown outcome, the steps left are not
// run, the run ends in error and its bundle is written. It returns how
// many it ended.
func (m *Manager) Load() int {
	rows, err := m.opt.Store.ListQARuns(store.QARunFilter{Unfinished: true})
	if err != nil {
		m.lg.Printf("list unfinished QA runs: %v", err)
		return 0
	}
	n := 0
	for _, row := range rows {
		rec, err := decodeRecord(row)
		if err != nil {
			m.lg.Printf("QA run %s: %v", row.RunID, err)
			continue
		}
		if err := m.settle(row, rec); err != nil {
			m.lg.Printf("settle QA run %s: %v", row.RunID, err)
			continue
		}
		n++
	}
	return n
}

// settle ends the interrupted run of rec.
func (m *Manager) settle(row store.QARun, rec record) error {
	run := &rec.Run
	unknown := &ErrorInfo{Code: wire.CodeExecutionOutcomeUnknown, Message: "데몬이 멈춘 동안 이 단계의 결과를 받지 못했습니다. PC 상태를 확인한 뒤 시나리오를 다시 실행하세요"}
	for i := range run.Steps {
		s := &run.Steps[i]
		switch s.Status {
		case StepRunning:
			s.Status, s.Error, s.Message = StepBlocked, unknown, unknown.Message
		case StepPending:
			s.Status, s.Message = StepNotRun, "데몬이 멈춰 실행하지 않았습니다"
		}
	}
	now := time.Now()
	run.Status, run.CurrentStep = RunError, nil
	run.Error = &ErrorInfo{Code: wire.CodeExecutionOutcomeUnknown, Message: "데몬이 멈춰 실행이 끊겼습니다. PC 상태를 확인한 뒤 시나리오를 다시 실행하세요"}
	run.FinishedAt, run.UpdatedAt = model.Time(now), model.Time(now)
	run.Rev = row.Rev + 1
	count(run)
	if err := writeBundle(&rec, artifacts.Dir(run.JobID)); err != nil {
		m.lg.Printf("write the bundle of QA run %s: %v", run.RunID, err)
		run.Report, run.ReportDir = nil, ""
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := m.opt.Store.SaveQARun(store.QARun{RunID: run.RunID, Status: run.Status, Rev: run.Rev, Data: data, UpdatedAt: now, FinishedAt: &now}); err != nil {
		return err
	}
	if m.opt.Jobs != nil && run.JobID != run.RunID {
		_, _ = m.opt.Jobs.Mirror(run.JobID, jobStatus(run.Status), jobResult(*run), true)
	}
	m.opt.Bus.Publish(run.HostID, run.JobID, events.QAUpdated, map[string]any{"run": summary(*run)})
	_, _ = m.opt.Bus.Record(model.System.Entry(run.HostID, model.TimelineQA, "QA 끊김(데몬 재시작): "+run.Name, model.TimelineUnknown, run.JobID))
	return nil
}

// jobStatus is the job status of a run status.
func jobStatus(status string) string {
	switch status {
	case RunPassed:
		return model.StatusSucceeded
	case RunCancelled:
		return model.StatusCancelled
	case RunError:
		return model.StatusUnknown
	case RunRunning:
		return model.StatusRunning
	}
	return model.StatusFailed
}

// jobResult is the result of the job of run.
func jobResult(run Run) map[string]any {
	out := map[string]any{"run_id": run.RunID, "status": run.Status, "steps_passed": run.StepsPassed, "steps_failed": run.StepsFailed,
		"steps_blocked": run.StepsBlocked, "steps_not_run": run.StepsNotRun}
	if run.ReportDir != "" {
		out["report_dir"] = run.ReportDir
	}
	if run.Error != nil {
		out["error"] = run.Error
	}
	return out
}

// timelineOutcome is the timeline outcome of a run status.
func timelineOutcome(status string) string {
	switch status {
	case RunPassed:
		return model.TimelineOK
	case RunBlocked:
		return model.TimelineDenied
	case RunError:
		return model.TimelineUnknown
	}
	return model.TimelineFailed
}
