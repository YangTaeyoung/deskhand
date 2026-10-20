package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/client"
)

const qaHelp = `사용법:
  deskhand qa run <시나리오.yaml> [--host H] [--var 이름=값]... [--var-file 파일] [--timeout 10m] [--detach]
  deskhand qa list [--host H] [--limit N]
  deskhand qa get <실행-id>
  deskhand qa report <실행-id> [--open]
  deskhand qa cancel <실행-id>

qa run은 단계가 끝날 때마다 한 줄씩 보이고, 끝나면 결과와 보고서 경로를 출력합니다.
Ctrl-C는 실행을 취소합니다(정리 단계 teardown은 실행합니다, 최대 1분 기다림). 기다리지 않으려면 --detach.
비밀값은 --var 대신 --var-file(한 줄에 이름=값)로 넘기고, 이름을 secret_로 시작하면 기록에서 ***로 가립니다.
시나리오 형식: docs/local-api.md §10, 예: docs/examples/qa/fixture-smoke.yaml
종료 코드: 0 통과, 1 실패·취소, 2 사용법, 3 차단(사람이 조작 중이거나 이 PC가 못 하는 단계), 4 오류·결과 불명.
`

// QA run statuses.
const (
	qaRunning   = "running"
	qaPassed    = "passed"
	qaFailed    = "failed"
	qaBlocked   = "blocked"
	qaCancelled = "cancelled"
	qaError     = "error"
)

// qaCancelWait bounds the wait for a cancelled run's teardown.
const qaCancelWait = time.Minute

func (r *run) qa(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return r.fail(usagef("deskhand qa run|list|get|report|cancel 중 하나를 고르세요(deskhand qa --help)"))
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(r.Stdout, qaHelp)
		return exitOK
	case "run":
		return r.qaRun(ctx, args[1:])
	case "list":
		return r.qaList(ctx, args[1:])
	case "get":
		return r.qaGet(ctx, args[1:])
	case "report":
		return r.qaReport(ctx, args[1:])
	case "cancel":
		return r.qaCancel(ctx, args[1:])
	}
	return r.fail(usagef("알 수 없는 qa 명령 %q입니다. run, list, get, report, cancel 중 하나를 고르세요", args[0]))
}

// varFlags collects repeated --var 이름=값 flags.
type varFlags map[string]string

func (v varFlags) String() string { return "" }

func (v varFlags) Set(s string) error {
	name, value, ok := strings.Cut(s, "=")
	if !ok || name == "" {
		return fmt.Errorf("--var %q는 이름=값 형식이어야 합니다", s)
	}
	v[name] = value
	return nil
}

// readVarFile reads the 이름=값 lines of a vars file; blank lines and lines
// starting with # are skipped.
func readVarFile(path string, into map[string]string) error {
	f, err := os.Open(path)
	if err != nil {
		return usagef("--var-file %s를 읽지 못했습니다(%v). 경로를 확인하세요", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return usagef("%s:%d는 이름=값 형식이 아닙니다. 줄을 고치세요", path, n)
		}
		into[strings.TrimSpace(name)] = value
	}
	if err := sc.Err(); err != nil {
		return usagef("--var-file %s를 읽지 못했습니다(%v)", path, err)
	}
	return nil
}

func (r *run) qaRun(ctx context.Context, args []string) int {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "-help" {
			fmt.Fprint(r.Stdout, qaHelp)
			return exitOK
		}
	}
	fs := newFlags("qa run")
	host := fs.String("host", "", "실행할 PC")
	vars := varFlags{}
	fs.Var(vars, "var", "변수 이름=값")
	varFile := fs.String("var-file", "", "변수 파일")
	timeout := fs.Duration("timeout", 0, "실행 제한 시간")
	detach := fs.Bool("detach", false, "시작만 하고 실행 ID를 출력")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 {
		return r.fail(usagef("deskhand qa run <시나리오.yaml> [--host H] 형식으로 주세요(deskhand qa --help)"))
	}
	if *timeout < 0 {
		return r.fail(usagef("--timeout은 0 이상이어야 합니다"))
	}
	path, err := localAbs(pos[0])
	if err != nil {
		return r.fail(err)
	}
	all := map[string]string{}
	if *varFile != "" {
		if err := readVarFile(*varFile, all); err != nil {
			return r.fail(err)
		}
	}
	for k, v := range vars {
		all[k] = v
	}
	params := map[string]any{"scenario_path": path}
	if *host != "" {
		params["host_id"] = *host
	}
	if len(all) > 0 {
		params["vars"] = all
	}
	if *timeout > 0 {
		params["timeout_ms"] = timeout.Milliseconds()
	}
	c, err := r.dial(ctx)
	if err != nil {
		return r.fail(err)
	}
	defer c.Close()
	var events <-chan client.Event
	if !*detach {
		if ch, err := c.Subscribe(ctx, []string{"qa.updated"}); err == nil {
			events = ch
		}
	}
	start, err := r.call(ctx, c, "qa.run", params, callTimeout)
	if err != nil {
		return r.failMut(err, *host)
	}
	r.jobID = start.str("job_id")
	id := start.str("run_id")
	if id == "" {
		return r.unknownExitWhy(start, "QA 실행", "실행 ID를 받지 못했습니다")
	}
	if *detach {
		if r.asJSON {
			writeJSON(r.Stdout, start)
		} else {
			fmt.Fprintln(r.Stdout, id)
			r.info("QA 실행을 시작했습니다. deskhand qa get %s 로 결과를 보세요", id)
		}
		return exitOK
	}
	run := start.sub("run")
	r.say("QA 실행 %s: %s (PC %s, 단계 %d개)", id, run.str("name"), run.str("host_id"), len(run.list("steps")))
	return r.followQA(ctx, c, id, events)
}

// qaProgress prints the steps of a run in order as they end.
type qaProgress struct {
	r       *run
	steps   map[int64]obj
	total   int64
	next    int64
	lastRev int64
}

func (p *qaProgress) step(s obj) {
	i, ok := s.num("index")
	if !ok {
		return
	}
	p.steps[i] = s
	for {
		cur, ok := p.steps[p.next]
		if !ok || !stepEnded(cur.str("status")) {
			return
		}
		p.r.say("%s", stepLine(cur, p.total))
		p.next++
	}
}

func (p *qaProgress) run(run obj) bool {
	rev, _ := run.num("rev")
	if rev < p.lastRev {
		return false
	}
	p.lastRev = rev
	if n, ok := run.num("steps_total"); ok {
		p.total = n
	}
	for _, s := range run.list("steps") {
		p.step(s)
	}
	return true
}

func stepEnded(status string) bool {
	return status == "pass" || status == "fail" || status == "blocked" || status == "not_run"
}

// followQA shows the steps of run id until it ends and returns the exit
// code of its status; Ctrl-C cancels the run and waits for its teardown.
func (r *run) followQA(ctx context.Context, c *client.Client, id string, events <-chan client.Event) int {
	p := &qaProgress{r: r, steps: map[int64]obj{}, next: 1}
	cur := obj{}
	get := func(ctx context.Context) error {
		res, err := r.call(ctx, c, "qa.get", map[string]any{"run_id": id}, callTimeout)
		if err != nil {
			return err
		}
		if p.run(res) {
			cur = res
		}
		return nil
	}
	watch := ctx
	cancelled := false
	var deadline <-chan time.Time
	for {
		if err := get(watch); err != nil {
			if watch.Err() != nil && !cancelled {
				watch, deadline, cancelled = r.cancelQA(c, id)
				continue
			}
			if watch.Err() != nil {
				return r.qaStillRunning(id)
			}
			return r.failMut(err, "")
		}
		if cur.str("finished_at") != "" {
			break
		}
		select {
		case <-watch.Done():
			if cancelled {
				return r.qaStillRunning(id)
			}
			watch, deadline, cancelled = r.cancelQA(c, id)
		case <-deadline:
			return r.qaStillRunning(id)
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if run := toObj(ev.Params["run"]); run.str("run_id") == id {
				if s, ok := ev.Params["step"]; ok {
					p.step(toObj(s))
				}
			}
		case <-time.After(r.PollInterval):
		}
	}
	return r.reportQA(cur)
}

// cancelQA asks the daemon to cancel run id after Ctrl-C and returns the
// context to keep watching under and the end of the wait.
func (r *run) cancelQA(c *client.Client, id string) (context.Context, <-chan time.Time, bool) {
	cctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	if _, err := r.call(cctx, c, "qa.cancel", map[string]any{"run_id": id}, callTimeout); err != nil {
		fmt.Fprintf(r.Stderr, "QA 실행 %s를 취소하지 못했습니다: %v. deskhand qa cancel %s 로 다시 취소하세요\n", id, err, id)
	} else {
		fmt.Fprintf(r.Stderr, "취소를 요청했습니다. 정리 단계(teardown)를 마치는 중입니다\n")
	}
	return context.Background(), time.After(qaCancelWait), true
}

func (r *run) qaStillRunning(id string) int {
	fmt.Fprintf(r.Stderr, "QA 실행 %s가 아직 끝나지 않았습니다. deskhand qa get %s 로 결과를 확인하거나 deskhand qa cancel %s 로 정리 단계까지 멈추세요\n", id, id, id)
	return exitUnknown
}

// qaLabel is the Korean word of a run status.
func qaLabel(status string) string {
	switch status {
	case qaPassed:
		return "통과"
	case qaFailed:
		return "실패"
	case qaBlocked:
		return "차단"
	case qaCancelled:
		return "취소됨"
	case qaError:
		return "오류"
	case qaRunning:
		return "실행 중"
	}
	return dash(status)
}

// stepLabel is the Korean word of a step status.
func stepLabel(status string) string {
	switch status {
	case "pass":
		return "통과"
	case "fail":
		return "실패"
	case "blocked":
		return "차단"
	case "not_run":
		return "실행 안 함"
	case "running":
		return "실행 중"
	}
	return "대기"
}

func phaseLabel(phase string) string {
	switch phase {
	case "setup":
		return "준비"
	case "teardown":
		return "정리"
	}
	return "단계"
}

func msText(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1f초", float64(ms)/1000)
}

// stepLine is one line for a step of a run with total steps.
func stepLine(s obj, total int64) string {
	i, _ := s.num("index")
	line := fmt.Sprintf("  [%s] %d/%d %s · %s (%s", stepLabel(s.str("status")), i, total, phaseLabel(s.str("phase")), s.str("name"), s.str("action"))
	if ms, ok := s.num("duration_ms"); ok {
		line += ", " + msText(ms)
	}
	line += ")"
	if s.str("status") != "pass" && s.str("message") != "" {
		line += " — " + s.str("message")
	}
	return line
}

// qaExit is the exit code of a run status.
func qaExit(status string) int {
	switch status {
	case qaPassed:
		return exitOK
	case qaFailed, qaCancelled:
		return exitFailure
	case qaBlocked:
		return exitNeedsUser
	}
	return exitUnknown
}

func qaCounts(run obj) string {
	n := func(k string) int64 { v, _ := run.num(k); return v }
	return fmt.Sprintf("통과 %d · 실패 %d · 차단 %d · 실행 안 함 %d, 전체 %d단계", n("steps_passed"), n("steps_failed"), n("steps_blocked"),
		n("steps_not_run"), n("steps_total"))
}

// reportQA prints the end of a run and returns its exit code.
func (r *run) reportQA(run obj) int {
	status := run.str("status")
	if r.asJSON {
		writeJSON(r.Stdout, run)
		return qaExit(status)
	}
	r.say("결과: %s — %s", qaLabel(status), qaCounts(run))
	if e := run.sub("error"); e != nil {
		r.say("실행 오류: %s", e.str("message"))
	}
	for _, u := range run.list("unsupported") {
		r.say("이 PC·데몬에서 할 수 없던 단계: %s (%s)", u.str("action"), u.str("reason"))
	}
	if rep := run.sub("report"); rep != nil {
		r.say("보고서: %s", rep.str("html"))
	}
	if status == qaBlocked {
		fmt.Fprintln(r.Stderr, "다음 단계: 차단된 단계의 이유를 해결한 뒤(사람이 제어권을 넘기거나 필요한 기능이 있는 PC에서) 다시 실행하세요")
	}
	return qaExit(status)
}

func (r *run) qaList(ctx context.Context, args []string) int {
	fs := newFlags("qa list")
	host := fs.String("host", "", "PC")
	limit := fs.Int("limit", 0, "개수")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 0 {
		return r.fail(usagef("deskhand qa list [--host H] [--limit N] 형식으로 주세요"))
	}
	params := map[string]any{}
	if *host != "" {
		params["host_id"] = *host
	}
	if *limit != 0 {
		params["limit"] = *limit
	}
	res, err := r.simple(ctx, "qa.list", params)
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	runs := res.list("runs")
	if len(runs) == 0 {
		fmt.Fprintln(r.Stdout, "QA 실행이 없습니다")
		return exitOK
	}
	rows := make([][]string, 0, len(runs))
	for _, run := range runs {
		n := func(k string) int64 { v, _ := run.num(k); return v }
		rows = append(rows, []string{run.str("run_id"), run.str("host_id"), run.str("name"), qaLabel(run.str("status")),
			fmt.Sprintf("%d/%d/%d/%d", n("steps_passed"), n("steps_failed"), n("steps_blocked"), n("steps_not_run")), run.str("started_at")})
	}
	writeTable(r.Stdout, []string{"실행", "PC", "이름", "결과", "통과/실패/차단/안 함", "시작"}, rows)
	if next := res.str("next_cursor"); next != "" {
		fmt.Fprintln(r.Stdout, "더 있습니다. --limit을 늘려 다시 보세요")
	}
	return exitOK
}

func (r *run) qaOne(ctx context.Context, args []string, name string, flags func(*run, []string) ([]string, error)) (obj, int) {
	pos, err := flags(r, args)
	if err != nil {
		return nil, r.fail(err)
	}
	if len(pos) != 1 {
		return nil, r.fail(usagef("deskhand qa %s <실행-id> 형식으로 주세요", name))
	}
	res, err := r.simple(ctx, "qa.get", map[string]any{"run_id": pos[0]})
	if err != nil {
		return nil, r.fail(err)
	}
	return res, -1
}

func (r *run) qaGet(ctx context.Context, args []string) int {
	run, code := r.qaOne(ctx, args, "get", func(_ *run, a []string) ([]string, error) {
		pos, _, err := parseArgs(newFlags("qa get"), a)
		return pos, err
	})
	if code >= 0 {
		return code
	}
	if r.asJSON {
		writeJSON(r.Stdout, run)
		return exitOK
	}
	total, _ := run.num("steps_total")
	fmt.Fprintf(r.Stdout, "QA 실행 %s: %s\nPC: %s\n결과: %s — %s\n시작: %s\n", run.str("run_id"), run.str("name"), run.str("host_id"),
		qaLabel(run.str("status")), qaCounts(run), run.str("started_at"))
	if f := run.str("finished_at"); f != "" {
		fmt.Fprintf(r.Stdout, "끝: %s\n", f)
	}
	if run.boolean("cancel_requested") && run.str("finished_at") == "" {
		fmt.Fprintln(r.Stdout, "취소를 요청했습니다. 정리 단계가 끝나면 멈춥니다")
	}
	for _, s := range run.list("steps") {
		fmt.Fprintln(r.Stdout, stepLine(s, total))
		if s.str("status") == "fail" || s.str("status") == "blocked" {
			for _, a := range s.list("assertions") {
				if a.boolean("passed") {
					continue
				}
				fmt.Fprintf(r.Stdout, "      %s: 기대 %s, 실제 %s\n", a.str("kind"), shortValue(a["expected"]), shortValue(a["actual"]))
			}
			for _, ev := range s.list("evidence") {
				fmt.Fprintf(r.Stdout, "      증거: %s\n", ev.str("path"))
			}
		}
	}
	if rep := run.sub("report"); rep != nil {
		fmt.Fprintf(r.Stdout, "보고서: %s\n", rep.str("html"))
	}
	return exitOK
}

// shortValue writes an assertion value in at most 120 characters.
func shortValue(v any) string {
	s := fmt.Sprint(v)
	if str, ok := v.(string); ok {
		s = fmt.Sprintf("%q", str)
	}
	if v == nil {
		s = "없음"
	}
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120]) + "…"
	}
	return s
}

func (r *run) qaReport(ctx context.Context, args []string) int {
	open := false
	run, code := r.qaOne(ctx, args, "report", func(_ *run, a []string) ([]string, error) {
		fs := newFlags("qa report")
		o := fs.Bool("open", false, "report.html 열기")
		pos, _, err := parseArgs(fs, a)
		open = *o
		return pos, err
	})
	if code >= 0 {
		return code
	}
	rep := run.sub("report")
	if rep == nil {
		fmt.Fprintf(r.Stderr, "QA 실행 %s의 보고서가 아직 없습니다. 실행이 끝난 뒤 다시 보세요(deskhand qa get %s)\n", run.str("run_id"), run.str("run_id"))
		return exitFailure
	}
	if r.asJSON {
		writeJSON(r.Stdout, obj{"run_id": run.str("run_id"), "report_dir": run.str("report_dir"), "report": rep})
	} else {
		fmt.Fprintf(r.Stdout, "HTML: %s\nMarkdown: %s\nJSON: %s\nmanifest: %s\n", rep.str("html"), rep.str("md"), rep.str("json"), rep.str("manifest"))
	}
	if open {
		if r.Open == nil {
			fmt.Fprintln(r.Stderr, "이 시스템에서는 보고서를 열 수 없습니다. 위 경로를 브라우저로 여세요")
			return exitFailure
		}
		if err := r.Open(rep.str("html")); err != nil {
			fmt.Fprintf(r.Stderr, "보고서를 열지 못했습니다(%v). 위 경로를 브라우저로 여세요\n", err)
			return exitFailure
		}
	}
	return exitOK
}

func (r *run) qaCancel(ctx context.Context, args []string) int {
	pos, _, err := parseArgs(newFlags("qa cancel"), args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 {
		return r.fail(usagef("deskhand qa cancel <실행-id> 형식으로 주세요"))
	}
	res, err := r.simple(ctx, "qa.cancel", map[string]any{"run_id": pos[0]})
	if err != nil {
		return r.failMut(err, "")
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	if res.sub("run").str("finished_at") != "" {
		r.say("QA 실행 %s는 이미 취소됐습니다", pos[0])
		return exitOK
	}
	r.say("QA 실행 %s의 취소를 요청했습니다. 정리 단계(teardown)를 실행한 뒤 끝납니다. deskhand qa get %s 로 확인하세요", pos[0], pos[0])
	return exitOK
}
