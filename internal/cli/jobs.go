package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/client"
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
	res, err := r.simple(ctx, "jobs.cancel", map[string]any{"job_id": pos[0]})
	if err != nil {
		return r.failMut(err, "")
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
	c, err := r.dial(ctx)
	if err != nil {
		return r.fail(err)
	}
	defer c.Close()
	return r.follow(ctx, c, pos[0], *jsonl || r.asJSON)
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

// follow polls jobs.output of id with the cursor until the output ends.
func (r *run) follow(ctx context.Context, c *client.Client, id string, lines bool) int {
	cursor, lastState := "", ""
	for {
		res, err := r.call(ctx, c, "jobs.output", map[string]any{"job_id": id, "cursor": cursor}, callTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return r.interrupted(id)
			}
			return r.fail(err)
		}
		for _, ch := range res.list("chunks") {
			stream, data := ch.str("stream"), ch.str("data")
			if lines {
				writeJSON(r.Stdout, obj{"type": "chunk", "job_id": id, "stream": stream, "data": data})
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
				ev := obj{"type": "state", "job_id": id, "state": state, "status": status}
				if code, ok := res.num("code"); ok {
					ev["code"] = code
				}
				writeJSON(r.Stdout, ev)
			}
		}
		if watchDone(res, cursor) {
			return r.watchExit(id, res)
		}
		select {
		case <-ctx.Done():
			return r.interrupted(id)
		case <-time.After(r.PollInterval):
		}
	}
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
