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
	kind := fs.String("kind", "", "종류(exec, pty, transfer, install)")
	status := fs.String("status", "", "상태(running, succeeded, failed, cancelled, unknown)")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 0 {
		return r.fail(usagef("deskhand jobs list [--host H] [--kind K] [--status S] 형식으로 주세요"))
	}
	params := map[string]any{}
	if *host != "" {
		params["host_id"] = *host
	}
	if *kind != "" {
		params["kind"] = *kind
	}
	if *status != "" {
		params["status"] = *status
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
	if res.sub("result").str("killed") == "connection_closed" {
		fmt.Fprintln(r.Stdout, "중단됨: PC와의 연결이 끊겨 명령이 중간에 끝났습니다. PC 상태를 확인한 뒤 다시 실행하세요")
	}
	if n, ok := res.num("output_bytes"); ok {
		fmt.Fprintf(r.Stdout, "출력: %d바이트 (deskhand jobs watch %s 로 보기)\n", n, res.str("job_id"))
	}
	return exitOK
}

// followOpts tune follow.
type followOpts struct {
	// lines prints one JSON object per chunk and state change.
	lines          bool
	stdout, stderr io.Writer
	// timeout is the time limit the daemon enforces on the job, named in
	// the message of a job it ended at that limit.
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
			if out.boolean("timed_out") {
				res.timedOut = true
				r.timedOut(id, o)
				res.exit = exitTimeout
				return res
			}
			res.exit = r.watchExit(id, out)
			return res
		}
		select {
		case <-ctx.Done():
			res.exit = r.interrupted(id)
			return res
		case <-time.After(r.PollInterval):
		}
	}
}

// timedOut reports a job the daemon ended at its time limit.
func (r *run) timedOut(id string, o followOpts) {
	if o.timeout > 0 {
		fmt.Fprintf(r.Stderr, "명령 실행: 명령이 %s 안에 끝나지 않아 종료했습니다. --timeout을 늘리거나 --timeout 0으로 다시 실행하세요\n", o.timeout)
		return
	}
	fmt.Fprintf(r.Stderr, "작업 %s: 시간 제한 안에 끝나지 않아 종료됐습니다. 제한을 늘려 다시 실행하세요\n", id)
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

// watchDone reports whether following a job stops: its output ended
// (cursor "end"), or the job waits for a person.
func watchDone(res obj, cursor string) bool {
	return cursor == "end" || res.str("status") == model.StatusWaitingForUser
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
