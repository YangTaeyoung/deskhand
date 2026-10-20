package qa

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/artifacts"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/ids"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// execMargin is added to an exec step's timeout for the round trip.
	execMargin = 30 * time.Second
	// evidenceTimeout bounds the capture of the evidence of a failed step.
	evidenceTimeout = 30 * time.Second
	// envTimeout bounds the diag.system read at the start of a run.
	envTimeout = 15 * time.Second
	// actualCut bounds a text recorded as an assertion's value.
	actualCut = 2000
)

// defaultTimeouts are the timeouts of the step kinds in milliseconds.
var defaultTimeouts = map[string]int64{ActPush: 600000, ActLaunch: 60000, ActWaitWindow: 15000, ActClick: 30000, ActType: 30000,
	ActKey: 30000, ActUIAct: 30000, ActAssertUI: 5000, ActAssertScreenText: 5000, ActAssertFile: 5000, ActExec: 120000,
	ActScreenshot: 30000, ActCollect: 120000, ActClose: 15000}

// gated are the step kinds that drive the host's UI and check control
// first.
var gated = map[string]bool{ActLaunch: true, ActClick: true, ActType: true, ActKey: true, ActUIAct: true, ActClose: true}

// blockingCodes are the error codes that make a step blocked rather than
// failed.
var blockingCodes = map[string]bool{wire.CodeControlHeldByHuman: true, wire.CodeControlPreempted: true, wire.CodeCapabilityUnsupported: true,
	wire.CodeOffline: true, wire.CodeAuthRequired: true, wire.CodeSessionLocked: true, wire.CodeDisplayUnavailable: true,
	wire.CodeElevationRequired: true, wire.CodeExecutionOutcomeUnknown: true}

// runner runs one run.
type runner struct {
	m       *Manager
	sc      *Scenario
	caller  model.Caller
	host    Host
	agent   Agent
	dir     string
	started time.Time
	timeout int64

	mainCtx    context.Context
	cancelMain context.CancelCauseFunc
	stopTimer  context.CancelFunc
	tdCtx      context.Context
	cancelTD   context.CancelCauseFunc

	mu         sync.Mutex
	rec        record
	inTeardown bool

	// The fields below belong to the goroutine of the run.
	window          map[string]any
	screenOff       bool
	seenUnsupported map[string]bool
}

// stepOut is what a step did.
type stepOut struct {
	status     string
	message    string
	err        *ErrorInfo
	assertions []Assertion
	result     map[string]any
	evidence   []model.Artifact
	// polled marks a fail that is the last check of a polling step.
	polled bool
	// uiWindow, when set, is the window whose UI tree a failure keeps;
	// tree is a UI tree read already.
	uiWindow *string
	tree     proto.Message
	// content is the file content a failed file check read.
	content []byte
}

func pass(msg string) stepOut { return stepOut{status: StepPass, message: msg} }

func failOut(code, msg string) stepOut {
	return stepOut{status: StepFail, message: msg, err: &ErrorInfo{Code: code, Message: msg}}
}

func blockedOut(code, msg string) stepOut {
	return stepOut{status: StepBlocked, message: msg, err: &ErrorInfo{Code: code, Message: msg}}
}

// fromError classifies an error of the host.
func fromError(err error) stepOut {
	e := apierr.As(err)
	if blockingCodes[e.Code] {
		return blockedOut(e.Code, e.Message)
	}
	return failOut(e.Code, e.Message)
}

// snapshot returns the run, with its steps when steps is set.
func (r *runner) snapshot(steps bool) Run {
	r.mu.Lock()
	run := clone(r.rec.Run)
	r.mu.Unlock()
	if !steps {
		run.Steps = nil
	}
	return run
}

// saveLocked stores the run with the next rev and publishes qa.updated
// with step, the step that changed (0 for none).
func (r *runner) saveLocked(step int) {
	run := &r.rec.Run
	run.Rev++
	now := time.Now()
	run.UpdatedAt = model.Time(now)
	count(run)
	data, err := json.Marshal(r.rec)
	if err != nil {
		r.m.lg.Printf("QA run %s: encode: %v", run.RunID, err)
		return
	}
	row := store.QARun{RunID: run.RunID, Status: run.Status, Rev: run.Rev, Data: data, UpdatedAt: now}
	if run.FinishedAt != "" {
		row.FinishedAt = &now
	}
	if err := r.m.opt.Store.SaveQARun(row); err != nil {
		r.m.lg.Printf("QA run %s: save: %v", run.RunID, err)
	}
	fields := map[string]any{"run": summary(*run)}
	if step > 0 {
		var s StepResult
		if b, err := json.Marshal(run.Steps[step-1]); err == nil && json.Unmarshal(b, &s) == nil {
			fields["step"] = s
		}
	}
	r.m.opt.Bus.Publish(run.HostID, run.JobID, events.QAUpdated, fields)
}

// update changes the step at index i (0-based) and saves the run.
func (r *runner) update(i int, fn func(s *StepResult, run *Run)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(&r.rec.Run.Steps[i], &r.rec.Run)
	r.saveLocked(i + 1)
}

// cancel asks the run to stop; a run that has ended is refused unless it
// was cancelled.
func (r *runner) cancel(c model.Caller) (Run, error) {
	r.mu.Lock()
	if run := r.rec.Run; run.FinishedAt != "" {
		r.mu.Unlock()
		if run.Status == RunCancelled {
			return r.snapshot(true), nil
		}
		return Run{}, ended(run)
	}
	first := !r.rec.Run.CancelRequested
	if r.inTeardown {
		r.cancelTD(errCancelled)
	}
	r.cancelMain(errCancelled)
	if first {
		r.rec.Run.CancelRequested = true
		r.saveLocked(0)
	}
	r.mu.Unlock()
	if first {
		_, _ = r.m.opt.Bus.Record(c.Entry(r.host.HostID, model.TimelineQA, "QA 취소 요청: "+r.rec.Run.Name, model.TimelineOK, r.rec.Run.JobID))
	}
	return r.snapshot(true), nil
}

// execute runs the steps, then the teardown, and writes the result.
func (r *runner) execute() {
	defer r.m.finished(r)
	defer func() {
		if r.stopTimer != nil {
			r.stopTimer()
		}
		r.cancelMain(nil)
		r.cancelTD(nil)
	}()
	r.environment()
	stop := ""
	for i, st := range r.sc.Steps {
		if st.Phase == PhaseTeardown {
			continue
		}
		if r.m.closing() {
			return
		}
		if stop == "" && r.mainCtx.Err() != nil {
			stop = stopReason(context.Cause(r.mainCtx))
		}
		if stop != "" {
			r.notRun(i, stop)
			continue
		}
		status := r.runStep(r.mainCtx, i)
		if r.m.closing() {
			return
		}
		if (status == StepFail || status == StepBlocked) && !st.Optional && !st.ContinueOnFail {
			stop = fmt.Sprintf("%d단계(%s)가 %s여서 실행하지 않았습니다", i+1, st.Name, stepLabel(status))
		}
	}
	r.mu.Lock()
	r.inTeardown = true
	r.mu.Unlock()
	tdStop := ""
	for i, st := range r.sc.Steps {
		if st.Phase != PhaseTeardown {
			continue
		}
		if r.m.closing() {
			return
		}
		if tdStop == "" && r.tdCtx.Err() != nil {
			tdStop = "취소로 정리 단계를 멈춰 실행하지 않았습니다"
		}
		if tdStop != "" {
			r.notRun(i, tdStop)
			continue
		}
		r.runStep(r.tdCtx, i)
	}
	if r.m.closing() {
		return
	}
	r.finish()
}

// stopReason says why the steps after a stop of the run are not run.
func stopReason(cause error) string {
	switch cause {
	case errCancelled:
		return "취소로 실행하지 않았습니다"
	case errRunTimeout:
		return "실행 제한 시간이 지나 실행하지 않았습니다"
	}
	return "실행이 멈춰 실행하지 않았습니다"
}

func (r *runner) notRun(i int, why string) {
	r.update(i, func(s *StepResult, _ *Run) {
		s.Status, s.Message = StepNotRun, why
	})
}

// environment reads the system of the host for the report; a host that
// cannot answer leaves it out.
func (r *runner) environment() {
	if r.agent == nil {
		return
	}
	if c, ok := r.agent.Caps()["diag"]; ok && c.State != "ready" {
		return
	}
	ctx, cancel := context.WithTimeout(r.mainCtx, envTimeout)
	defer cancel()
	res, err := r.agent.Call(ctx, "diag.system", proto.Message{}, conn.CallOpts{})
	if err != nil {
		return
	}
	env := map[string]any{}
	for _, k := range []string{"os", "computer_name", "cpu", "memory", "disks", "gpus", "boot_time", "uptime_s"} {
		if v, ok := res[k]; ok {
			env[k] = v
		}
	}
	r.mu.Lock()
	r.rec.Environment = env
	r.mu.Unlock()
}

// runStep runs the step at index i under ctx and returns its status.
func (r *runner) runStep(ctx context.Context, i int) string {
	st := r.sc.Steps[i]
	start := time.Now()
	r.update(i, func(s *StepResult, run *Run) {
		s.Status, s.StartedAt = StepRunning, model.Time(start)
		idx := i + 1
		run.CurrentStep = &idx
	})
	timeout := st.TimeoutMS
	if timeout == 0 {
		timeout = defaultTimeouts[st.Action]
	}
	var sctx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		d := time.Duration(timeout) * time.Millisecond
		if st.Action == ActExec {
			d += execMargin
		}
		sctx, cancel = context.WithTimeoutCause(ctx, d, errStepTimeout)
	} else {
		sctx, cancel = context.WithCancel(ctx)
	}
	out := r.do(sctx, st, i, timeout)
	stepCause := context.Cause(sctx)
	cancel()
	if r.m.closing() {
		return ""
	}
	if out.status != StepPass {
		switch {
		case ctx.Err() != nil && context.Cause(ctx) == errCancelled:
			out.status, out.err, out.message = StepBlocked, nil, "취소로 멈췄습니다"
		case ctx.Err() != nil && context.Cause(ctx) == errRunTimeout:
			msg := fmt.Sprintf("실행 제한 시간(%s)이 지나 이 단계를 멈췄습니다. timeout_ms를 늘리거나 시나리오를 나누세요", millis(r.timeout))
			out.status, out.err, out.message = StepFail, &ErrorInfo{Code: wire.CodeTimeout, Message: msg}, msg
		case stepCause == errStepTimeout && !out.polled:
			msg := fmt.Sprintf("단계 제한 시간(%s) 안에 끝나지 않았습니다. timeout_ms를 늘리세요", millis(timeout))
			out.status, out.err, out.message = StepFail, &ErrorInfo{Code: wire.CodeTimeout, Message: msg}, msg
		}
	}
	if out.status == StepFail {
		r.failEvidence(st, i, &out)
	}
	if out.status == StepBlocked && out.err != nil && out.err.Code == wire.CodeCapabilityUnsupported && !r.seenUnsupported[st.Action] {
		r.seenUnsupported[st.Action] = true
		r.mu.Lock()
		r.rec.Run.Unsupported = append(r.rec.Run.Unsupported, Unsupported{Action: st.Action, Code: out.err.Code, Reason: r.sc.Scrub(out.err.Message)})
		r.mu.Unlock()
	}
	dur := time.Since(start).Milliseconds()
	r.update(i, func(s *StepResult, run *Run) {
		s.Status = out.status
		s.Message = r.sc.Scrub(out.message)
		if out.err != nil {
			s.Error = &ErrorInfo{Code: out.err.Code, Message: r.sc.Scrub(out.err.Message)}
		}
		for _, a := range out.assertions {
			a.Expected, a.Actual = r.sc.ScrubAny(a.Expected), r.sc.ScrubAny(a.Actual)
			s.Assertions = append(s.Assertions, a)
		}
		if out.result != nil {
			s.Result = r.sc.ScrubAny(out.result).(map[string]any)
		}
		s.Evidence = append(s.Evidence, out.evidence...)
		s.DurationMS = &dur
		run.CurrentStep = nil
	})
	return out.status
}

// millis writes a duration in milliseconds for people.
func millis(ms int64) string {
	if ms%1000 == 0 {
		return fmt.Sprintf("%d초", ms/1000)
	}
	return fmt.Sprintf("%.1f초", float64(ms)/1000)
}

// failEvidence keeps the evidence of a failed step: a screenshot, the UI
// tree of an assertion's window and the content a file check read.
func (r *runner) failEvidence(st Step, i int, out *stepOut) {
	ctx, cancel := context.WithTimeout(r.m.base, evidenceTimeout)
	defer cancel()
	if a, ok := r.capture(ctx, i, st.Action, "screenshot", 0, "", defaultMaxWidth); ok {
		out.evidence = append(out.evidence, a)
	}
	tree := out.tree
	if tree == nil && out.uiWindow != nil && r.agent != nil {
		params := proto.Message{"filter": "interactive", "depth": 12, "max": 300}
		if *out.uiWindow != "" {
			params["window"] = *out.uiWindow
		}
		if res, err := r.agent.Call(ctx, "ui.tree", params, conn.CallOpts{}); err == nil {
			tree = res
		}
	}
	if tree != nil {
		if data, err := json.MarshalIndent(tree, "", "  "); err == nil {
			if a, err := r.save(i, st.Action, "ui-tree", ".json", "ui_tree", []byte(r.sc.Scrub(string(data)))); err == nil {
				out.evidence = append(out.evidence, a)
			}
		}
	}
	if out.content != nil {
		data := out.content
		if len(data) > 64<<10 {
			data = data[len(data)-64<<10:]
		}
		if a, err := r.save(i, st.Action, "content", ".txt", "log", []byte(r.sc.Scrub(string(data)))); err == nil {
			out.evidence = append(out.evidence, a)
		}
	}
}

// capture takes a PNG screenshot as evidence of the step at index i; it
// reports false when the host cannot take one.
func (r *runner) capture(ctx context.Context, i int, action, suffix string, display int, window string, maxWidth int) (model.Artifact, bool) {
	if r.screenOff || r.agent == nil {
		return model.Artifact{}, false
	}
	if c, ok := r.agent.Caps()["screen"]; !ok || c.State != "ready" {
		r.screenOff = true
		return model.Artifact{}, false
	}
	data, _, err := r.screenshot(ctx, display, window, maxWidth)
	if err != nil {
		if apierr.CodeOf(err) == wire.CodeCapabilityUnsupported {
			r.screenOff = true
		}
		return model.Artifact{}, false
	}
	a, err := r.save(i, action, suffix, ".png", "screenshot", data)
	return a, err == nil
}

// screenshot reads a PNG of a display or a window of the host.
func (r *runner) screenshot(ctx context.Context, display int, window string, maxWidth int) ([]byte, proto.Message, error) {
	params := proto.Message{"display": display, "format": "png", "max_width": maxWidth}
	if window != "" {
		params["window"] = window
	}
	res, err := r.agent.Call(ctx, "screen.capture", params, conn.CallOpts{})
	if err != nil {
		return nil, nil, err
	}
	data, err := decodeBase64(res.String("data"))
	if err != nil {
		return nil, nil, apierr.Internal("에이전트가 보낸 화면을 읽지 못했습니다. 에이전트를 업데이트하세요")
	}
	return data, res, nil
}

// save writes data as the evidence file evidence/<NN>-<action>[-suffix]<ext>
// of the step at index i and returns its artifact.
func (r *runner) save(i int, action, suffix, ext, kind string, data []byte) (model.Artifact, error) {
	width := len(strconv.Itoa(len(r.sc.Steps)))
	if width < 2 {
		width = 2
	}
	name := fmt.Sprintf("%0*d-%s", width, i+1, action)
	if suffix != "" {
		name += "-" + safeName(suffix)
	}
	name += ext
	dir := filepath.Join(r.dir, "evidence")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return model.Artifact{}, err
	}
	p := uniquePath(filepath.Join(dir, name))
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return model.Artifact{}, err
	}
	sum := sha256.Sum256(data)
	return model.Artifact{ArtifactID: ids.New(ids.Artifact), JobID: r.rec.Run.JobID, Kind: kind, Name: filepath.Base(p), Path: p,
		Size: int64(len(data)), MIME: artifacts.MIMEOf(p), SHA256: hex.EncodeToString(sum[:]), CreatedAt: model.Time(time.Now())}, nil
}

// uniquePath returns p, or p with "-n" before its extension for the first
// n that is free.
func uniquePath(p string) string {
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for n := 1; ; n++ {
		if _, err := os.Lstat(p); err != nil {
			return p
		}
		p = fmt.Sprintf("%s-%d%s", base, n, ext)
	}
}

// safeName keeps the letters, digits, '.', '-' and '_' of s and turns the
// other characters into '_'.
func safeName(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case unicode.IsLetter(c), unicode.IsDigit(c), c == '.', c == '-', c == '_':
			b.WriteRune(c)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if r := []rune(out); len(r) > 80 {
		out = string(r[:80])
	}
	if out == "" {
		out = "file"
	}
	return out
}

// finish computes the run's status, writes its bundle and ends its job.
func (r *runner) finish() {
	r.mu.Lock()
	run := &r.rec.Run
	run.Status = finalStatus(*run, context.Cause(r.mainCtx))
	if context.Cause(r.mainCtx) == errRunTimeout {
		run.Error = &ErrorInfo{Code: wire.CodeTimeout, Message: fmt.Sprintf("실행 제한 시간(%s)이 지났습니다. timeout_ms를 늘리거나 시나리오를 나누세요", millis(r.timeout))}
	}
	run.CurrentStep = nil
	now := time.Now()
	run.FinishedAt = model.Time(now)
	count(run)
	if err := writeBundle(&r.rec, r.dir); err != nil {
		r.m.lg.Printf("QA run %s: write the bundle: %v", run.RunID, err)
	}
	r.m.release(r)
	r.saveLocked(0)
	final := clone(*run)
	report := r.rec.Run.Report
	r.mu.Unlock()
	if r.m.opt.Jobs != nil {
		_, _ = r.m.opt.Jobs.Finish(final.JobID, jobStatus(final.Status), jobResult(final))
	}
	entry := r.caller.Entry(final.HostID, model.TimelineQA, fmt.Sprintf("QA 끝: %s (%s)", final.Name, runLabel(final.Status)), timelineOutcome(final.Status), final.JobID)
	if report != nil {
		var arts []model.Artifact
		if m, ok, err := artifacts.ReadManifest(r.dir); err == nil && ok {
			for _, f := range m.Files {
				if f.Kind == "report" {
					p := filepath.Join(r.dir, filepath.FromSlash(f.Path))
					arts = append(arts, model.Artifact{ArtifactID: f.ArtifactID, JobID: final.JobID, Kind: f.Kind, Name: filepath.Base(p), Path: p,
						Size: f.Size, MIME: f.MIME, SHA256: f.SHA256, CreatedAt: final.FinishedAt})
				}
			}
		}
		if b, err := json.Marshal(arts); err == nil && len(arts) > 0 {
			entry.Artifacts = b
		}
	}
	_, _ = r.m.opt.Bus.Record(entry)
}

// finalStatus is the status of a run whose steps have ended; cause is the
// cause of its main context.
func finalStatus(run Run, cause error) string {
	switch cause {
	case errCancelled:
		return RunCancelled
	case errRunTimeout:
		return RunFailed
	}
	failed, blocked := false, false
	for _, s := range run.Steps {
		if s.Optional {
			continue
		}
		failed = failed || s.Status == StepFail
		blocked = blocked || s.Status == StepBlocked
	}
	switch {
	case failed:
		return RunFailed
	case blocked:
		return RunBlocked
	}
	return RunPassed
}

// stepLabel is the Korean word of a step status.
func stepLabel(status string) string {
	switch status {
	case StepPass:
		return "통과"
	case StepFail:
		return "실패"
	case StepBlocked:
		return "차단"
	case StepNotRun:
		return "실행 안 함"
	case StepRunning:
		return "실행 중"
	}
	return "대기"
}

// runLabel is the Korean word of a run status.
func runLabel(status string) string {
	switch status {
	case RunPassed:
		return "통과"
	case RunFailed:
		return "실패"
	case RunBlocked:
		return "차단"
	case RunCancelled:
		return "취소됨"
	case RunError:
		return "오류"
	}
	return "실행 중"
}
