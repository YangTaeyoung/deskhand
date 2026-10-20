package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
)

func (r *run) jobs(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return r.fail(usagef("deskhand jobs list|watch|cancel 중 하나를 고르세요"))
	}
	switch args[0] {
	case "list":
		return r.jobsList(ctx, args[1:])
	case "watch":
		return r.jobsWatch(ctx, args[1:])
	case "cancel":
		return r.jobsCancel(ctx, args[1:])
	}
	return r.fail(usagef("알 수 없는 jobs 명령 %q입니다. list, watch, cancel 중 하나를 고르세요", args[0]))
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
	res, err := r.simple(ctx, "jobs.cancel", map[string]any{"job_id": pos[0]})
	if err != nil {
		return r.fail(err)
	}
	code := r.settle(res, "작업 취소")
	if code == exitOK || res.str("status") == model.StatusCancelled {
		r.say("작업 %s를 취소했습니다", pos[0])
		return exitOK
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
	lines := *jsonl || r.asJSON
	c, err := r.dial(ctx)
	if err != nil {
		return r.fail(err)
	}
	defer c.Close()
	cursor, lastState := "", ""
	for {
		res, err := r.call(ctx, c, "jobs.output", map[string]any{"job_id": pos[0], "cursor": cursor}, callTimeout)
		if err != nil {
			return r.fail(err)
		}
		for _, ch := range res.list("chunks") {
			stream, data := ch.str("stream"), ch.str("data")
			if lines {
				writeJSON(r.Stdout, obj{"type": "chunk", "job_id": pos[0], "stream": stream, "data": data})
			} else if stream == "stderr" {
				fmt.Fprint(r.Stderr, data)
			} else {
				fmt.Fprint(r.Stdout, data)
			}
		}
		state, status := res.str("state"), res.str("status")
		if next := res.str("next_cursor"); next != "" {
			cursor = next
		}
		if state != lastState {
			lastState = state
			if lines {
				ev := obj{"type": "state", "job_id": pos[0], "state": state, "status": status}
				if code, ok := res.num("code"); ok {
					ev["code"] = code
				}
				writeJSON(r.Stdout, ev)
			}
		}
		if watchDone(res, cursor) {
			return r.watchExit(pos[0], res, lines)
		}
		select {
		case <-ctx.Done():
			return exitFailure
		case <-time.After(r.PollInterval):
		}
	}
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
	case model.StatusSucceeded, model.StatusFailed, model.StatusCancelled, model.StatusUnknown:
		return true
	}
	return false
}

func (r *run) watchExit(id string, res obj, lines bool) int {
	status := res.str("status")
	code, hasCode := res.num("code")
	switch {
	case status == model.StatusUnknown || res.str("state") == "lost":
		fmt.Fprintf(r.Stderr, "작업 %s의 결과를 알 수 없습니다. 원격 PC가 응답하지 않으면 deskhand host doctor로 점검한 뒤 deskhand jobs list로 다시 확인하세요\n", id)
		return exitUnknown
	case hasCode && code != 0:
		return remoteExitCode(code)
	case status == model.StatusFailed || status == model.StatusCancelled:
		return exitFailure
	}
	return exitOK
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
