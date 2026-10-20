package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/client"
	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
)

func (r *run) jobs(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return r.fail(usagef("deskhand jobs list|get|watch|cancel 중 하나를 고르세요"))
	}
	switch args[0] {
	case "list":
		return r.jobsList(ctx, args[1:])
	case "watch":
		return r.jobsWatch(ctx, args[1:])
	case "cancel":
		return r.jobsCancel(ctx, args[1:])
	case "get":
		return r.jobsGet(ctx, args[1:])
	}
	return r.fail(usagef("알 수 없는 jobs 명령 %q입니다. list, get, watch, cancel 중 하나를 고르세요", args[0]))
}

func (r *run) jobsList(ctx context.Context, args []string) int {
	fs := newFlags("jobs list")
	host := fs.String("host", "", "호스트")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 0 {
		return r.fail(usagef("deskhand jobs list [--host H] 형식으로 주세요"))
	}
	params := map[string]any{}
	if *host != "" {
		params["host_id"] = *host
	}
	res, err := r.simple(ctx, "jobs.list", params)
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	jobs := res.list("jobs")
	if len(jobs) == 0 {
		fmt.Fprintln(r.Stdout, "작업이 없습니다")
		return exitOK
	}
	rows := make([][]string, 0, len(jobs))
	for _, j := range jobs {
		rows = append(rows, []string{j.str("job_id"), j.str("host_id"), j.str("kind"), statusKorean(j.str("status")),
			j.str("updated_at"), j.str("summary")})
	}
	writeTable(r.Stdout, []string{"작업", "호스트", "종류", "상태", "갱신", "요약"}, rows)
	return exitOK
}

func (r *run) jobsCancel(ctx context.Context, args []string) int {
	pos, _, err := parseArgs(newFlags("jobs cancel"), args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 {
		return r.fail(usagef("deskhand jobs cancel <job-id> 형식으로 주세요"))
	}
	r.jobID = pos[0]
	res, err := r.simple(ctx, "jobs.cancel", map[string]any{"job_id": pos[0]})
	if err != nil {
		return r.failMut(err, "")
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
	}
	code := r.statusExit(res, "작업 취소", allowCancelled)
	if code == exitOK {
		r.say("작업 %s를 취소했습니다", pos[0])
	}
	return code
}

// jobsWatch polls jobs.output with the cursor until the job's output ends.
func (r *run) jobsWatch(ctx context.Context, args []string) int {
	fs := newFlags("jobs watch")
	jsonl := fs.Bool("jsonl", false, "청크·상태 변화마다 JSON 한 줄")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 {
		return r.fail(usagef("deskhand jobs watch <job-id> [--jsonl] 형식으로 주세요"))
	}
	c, err := r.dial(ctx)
	if err != nil {
		return r.fail(err)
	}
	defer c.Close()
	f := r.follow(ctx, c, pos[0], followOpts{lines: *jsonl || r.asJSON, stdout: r.Stdout, stderr: r.Stderr})
	return f.exit
}

func (r *run) jobsGet(ctx context.Context, args []string) int {
	pos, _, err := parseArgs(newFlags("jobs get"), args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 {
		return r.fail(usagef("deskhand jobs get <job-id> 형식으로 주세요"))
	}
	res, err := r.simple(ctx, "jobs.get", map[string]any{"job_id": pos[0]})
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	fmt.Fprintf(r.Stdout, "작업: %s\n호스트: %s\n종류: %s\n상태: %s\n요약: %s\n갱신: %s\n", res.str("job_id"), res.str("host_id"),
		res.str("kind"), statusKorean(res.str("status")), res.str("summary"), res.str("updated_at"))
	return exitOK
}

// followOpts tune follow.
type followOpts struct {
	// lines prints one JSON object per chunk and state change.
	lines          bool
	stdout, stderr io.Writer
	// timeout cancels the job once it has run that long; 0 waits for the end.
	timeout time.Duration
}

// followResult is how a followed job ended.
type followResult struct {
	exit     int
	status   string
	code     int64
	hasCode  bool
	timedOut bool
	// printed is set when the failure was already reported as JSON.
	printed bool
}

// follow polls jobs.output of id with the cursor until the output ends.
func (r *run) follow(ctx context.Context, c *client.Client, id string, o followOpts) followResult {
	r.jobID = id
	var deadline time.Time
	if o.timeout > 0 {
		deadline = time.Now().Add(o.timeout)
	}
	cursor, lastState := "", ""
	res := followResult{}
	for {
		out, err := r.call(ctx, c, "jobs.output", map[string]any{"job_id": id, "cursor": cursor}, callTimeout)
		if err != nil {
			if ctx.Err() != nil {
				res.exit = r.interrupted(id)
			} else {
				res.exit = r.failMut(err, "")
				res.printed = r.asJSON
			}
			return res
		}
		r.emit(out, id, &lastState, o)
		if next := out.str("next_cursor"); next != "" {
			cursor = next
		}
		res.status = out.str("status")
		res.code, res.hasCode = out.num("code")
		if watchDone(out, cursor) {
			res.exit = r.watchExit(id, out)
			return res
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return r.timeoutJob(ctx, c, id, o, &cursor, &lastState)
		}
		select {
		case <-ctx.Done():
			res.exit = r.interrupted(id)
			return res
		case <-time.After(r.PollInterval):
		}
	}
}

// emit prints the chunks and the state change of one jobs.output result.
func (r *run) emit(out obj, id string, lastState *string, o followOpts) {
	for _, ch := range out.list("chunks") {
		stream, data := ch.str("stream"), ch.str("data")
		switch {
		case o.lines:
			writeJSON(o.stdout, obj{"type": "chunk", "job_id": id, "stream": stream, "data": data})
		case stream == "stderr":
			fmt.Fprint(o.stderr, data)
		default:
			fmt.Fprint(o.stdout, data)
		}
	}
	state := out.str("state")
	if state != *lastState {
		*lastState = state
		if o.lines {
			ev := obj{"type": "state", "job_id": id, "state": state, "status": out.str("status")}
			if code, ok := out.num("code"); ok {
				ev["code"] = code
			}
			writeJSON(o.stdout, ev)
		}
	}
}

// timeoutJob cancels a job that ran past its time limit and settles the
// result by what the cancel did: a cancelled job ends with the timeout exit
// code, a job that had already finished returns its real result, and a
// cancel whose result is unknown exits 4.
func (r *run) timeoutJob(ctx context.Context, c *client.Client, id string, o followOpts, cursor, lastState *string) followResult {
	res := followResult{}
	cancel, err := r.call(ctx, c, "jobs.cancel", map[string]any{"job_id": id}, callTimeout)
	status := cancel.str("status")
	finished := status == model.StatusSucceeded || (status == model.StatusFailed && cancel.sub("error") == nil)
	if err != nil || !(status == model.StatusCancelled || finished) {
		why := "취소 결과를 확인하지 못했습니다"
		if err != nil {
			why = "취소 요청이 실패했습니다(" + apierrMessage(err) + ")"
		}
		fmt.Fprintf(r.Stderr, "작업 %s: 시간 제한을 넘겼지만 %s. 명령이 아직 실행 중일 수 있습니다. deskhand jobs get %s 로 상태를 확인하세요\n", id, why, id)
		res.exit = exitUnknown
		return res
	}
	last := r.drain(ctx, c, id, o, cursor, lastState)
	if finished {
		res.status = last.str("status")
		res.code, res.hasCode = last.num("code")
		res.exit = r.watchExit(id, last)
		return res
	}
	res.timedOut = true
	fmt.Fprintf(r.Stderr, "명령 실행: 명령이 %s 안에 끝나지 않아 종료했습니다. --timeout을 늘리거나 --timeout 0으로 다시 실행하세요\n", o.timeout)
	res.exit = exitTimeout
	return res
}

// drain prints the remaining output of id until it ends or cannot be read
// and returns the last jobs.output result.
func (r *run) drain(ctx context.Context, c *client.Client, id string, o followOpts, cursor, lastState *string) obj {
	var last obj
	for i := 0; i < drainPolls; i++ {
		out, err := r.call(ctx, c, "jobs.output", map[string]any{"job_id": id, "cursor": *cursor}, callTimeout)
		if err != nil {
			return last
		}
		last = out
		r.emit(out, id, lastState, o)
		if next := out.str("next_cursor"); next != "" {
			*cursor = next
		}
		if watchDone(out, *cursor) {
			return last
		}
		time.Sleep(r.PollInterval)
	}
	return last
}

// drainPolls bounds the reads that follow a cancel.
const drainPolls = 20

func apierrMessage(err error) string {
	var ae *apierr.Error
	if errors.As(err, &ae) {
		return ae.Message
	}
	return err.Error()
}

func (r *run) interrupted(id string) int {
	fmt.Fprintf(r.Stderr, "중단했습니다. 작업 %s는 PC에서 계속 실행 중일 수 있습니다. deskhand jobs get %s 로 상태를 확인하고 필요하면 deskhand jobs cancel %s 로 끝내세요\n", id, id, id)
	return exitUnknown
}

func watchDone(res obj, cursor string) bool {
	switch res.str("state") {
	case "exited", "lost", "end":
		return true
	}
	if cursor == "end" {
		return true
	}
	switch res.str("status") {
	case model.StatusSucceeded, model.StatusFailed, model.StatusCancelled, model.StatusUnknown, model.StatusWaitingForUser:
		return true
	}
	return false
}

func (r *run) watchExit(id string, res obj) int {
	status := res.str("status")
	code, hasCode := res.num("code")
	switch {
	case status == model.StatusWaitingForUser:
		fmt.Fprintf(r.Stderr, "작업 %s: 사람의 확인이 필요합니다. PC 화면에서 확인을 마친 뒤 deskhand jobs get %s 로 상태를 보세요\n", id, id)
		return exitNeedsUser
	case status == model.StatusUnknown || res.str("state") == "lost":
		fmt.Fprintf(r.Stderr, "작업 %s의 결과를 알 수 없습니다. 원격 PC가 응답하지 않으면 deskhand host doctor로 점검한 뒤 deskhand jobs get %s 로 다시 확인하세요\n", id, id)
		return exitUnknown
	case hasCode && code != 0:
		return remoteExitCode(code)
	case status == model.StatusCancelled:
		fmt.Fprintf(r.Stderr, "작업 %s가 취소됐습니다\n", id)
		return exitFailure
	case status == model.StatusFailed:
		return exitFailure
	}
	if status == model.StatusSucceeded || (hasCode && code == 0) {
		return exitOK
	}
	fmt.Fprintf(r.Stderr, "작업 %s: 출력은 끝났지만 종료 상태(%s)를 확인하지 못했습니다. deskhand jobs get %s 로 상태를 확인하세요\n", id, dash(status), id)
	return exitUnknown
}

func statusKorean(s string) string {
	switch s {
	case model.StatusSucceeded:
		return "성공"
	case model.StatusFailed:
		return "실패"
	case model.StatusRunning:
		return "실행 중"
	case model.StatusWaitingForUser:
		return "사람 대기"
	case model.StatusUnknown:
		return "결과 불명"
	case model.StatusCancelled:
		return "취소됨"
	}
	return dash(s)
}
