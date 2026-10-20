package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/client"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// toObj converts a decoded JSON value to an obj whose numbers keep their digits.
func toObj(v any) obj {
	b, err := json.Marshal(v)
	if err != nil {
		return obj{}
	}
	o, err := decodeObj(b)
	if err != nil {
		return obj{}
	}
	return o
}

// isLocalPath reports whether a command line path is on this Mac rather
// than a <host>:<path> argument.
func isLocalPath(s string) bool {
	for _, p := range []string{"/", "./", "../", "~"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return !strings.Contains(s, ":")
}

// localAbs returns the absolute form of a path on this Mac, expanding a
// leading ~.
func localAbs(s string) (string, error) {
	if s == "~" || strings.HasPrefix(s, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", usagef("홈 폴더를 알 수 없어 %q를 풀 수 없습니다. 전체 경로로 주세요", s)
		}
		s = filepath.Join(home, strings.TrimPrefix(s, "~"))
	}
	return filepath.Abs(s)
}

// remoteBase returns the last element of a remote path written with \ or /.
func remoteBase(p string) string {
	p = strings.TrimRight(p, `\/`)
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		p = p[i+1:]
	}
	if strings.HasSuffix(p, ":") {
		return ""
	}
	return p
}

// resolveRemoteTarget returns the full remote path of an upload of src to
// remote: remote itself, or the folder remote with the name of src appended
// when remote ends with a separator.
func resolveRemoteTarget(remote, src string) string {
	if !strings.HasSuffix(remote, `\`) && !strings.HasSuffix(remote, "/") {
		return remote
	}
	return remote + filepath.Base(src)
}

// resolveLocalTarget returns the full local path of a download of remote to
// target: target itself, or the folder target with the name of remote
// appended when target is an existing folder or is written with a trailing /.
func resolveLocalTarget(target string, trailingSlash bool, remote string) string {
	if fi, err := os.Stat(target); (err == nil && fi.IsDir()) || trailingSlash {
		return filepath.Join(target, remoteBase(remote))
	}
	return target
}

const xferHelp = `사용법:
  deskhand files push <로컬 경로> <호스트>:<원격 경로> [옵션]
  deskhand files pull <호스트>:<원격 경로> <로컬 경로> [옵션]
  deskhand files move <원본> <대상> [옵션]   한쪽만 <호스트>:<경로>. 받은 쪽 검증이 끝난 뒤에만 원본을 정리합니다.
                                              PC의 원본은 휴지통으로, 이 Mac의 원본은 지웁니다. 결과 문장이 원본의 행방을 알려 줍니다.

대상 경로는 옮겨 둘 전체 경로입니다. 폴더 경로로 끝나는 대상(원격은 \ 또는 /, 로컬은 / 또는 기존 폴더)은 그 안에 원본 이름으로 둡니다.
옵션:
  --on-conflict fail|replace|skip|rename   대상이 이미 있을 때(기본 fail)
  --detach                                  시작만 하고 전송 ID를 출력
  --jsonl                                   transfer.updated 이벤트를 JSON 한 줄씩 출력
종료 코드: 0 모두 끝남, 1 일부 실패·취소·원본 남김(--json에 파일별 상세), 2 사용법, 4 결과 불명, 5 오프라인.
`

// filesTransfer runs files push, pull or move.
func (r *run) filesTransfer(ctx context.Context, name string, args []string) int {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "-help" {
			fmt.Fprint(r.Stdout, xferHelp)
			return exitOK
		}
	}
	fs := newFlags("files " + name)
	onConflict := fs.String("on-conflict", "fail", "대상이 이미 있을 때")
	detach := fs.Bool("detach", false, "시작만 하고 전송 ID를 출력")
	jsonl := fs.Bool("jsonl", false, "이벤트를 JSON 한 줄씩 출력")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 2 {
		return r.fail(usagef("deskhand files %s <원본> <대상> 형식으로 주세요(deskhand files %s --help)", name, name))
	}
	if !wire.IsConflictPolicy(*onConflict) {
		return r.fail(usagef("--on-conflict는 %s 중 하나여야 합니다", strings.Join(wire.ConflictPolicies(), ", ")))
	}
	if *detach && *jsonl {
		return r.fail(usagef("--detach에는 --jsonl을 쓸 수 없습니다. deskhand transfer watch <전송-id> --jsonl 로 따라가세요"))
	}
	up, err := r.xferUp(name, pos)
	if err != nil {
		return r.fail(err)
	}
	var host, local, remote, direction string
	if up {
		src, aerr := localAbs(pos[0])
		dst, perr := parseRemote(pos[1])
		if err := firstErr(aerr, perr); err != nil {
			return r.fail(err)
		}
		host, local, remote = dst.Host, src, resolveRemoteTarget(dst.Path, src)
		direction = map[bool]string{true: "move_push", false: "push"}[name == "move"]
	} else {
		src, perr := parseRemote(pos[0])
		dst, aerr := localAbs(pos[1])
		if err := firstErr(perr, aerr); err != nil {
			return r.fail(err)
		}
		host, remote = src.Host, src.Path
		local = resolveLocalTarget(dst, strings.HasSuffix(pos[1], "/"), src.Path)
		direction = map[bool]string{true: "move_pull", false: "pull"}[name == "move"]
	}
	params := map[string]any{"direction": direction, "host_id": host, "local": local, "remote": remote, "on_conflict": *onConflict}
	c, err := r.dial(ctx)
	if err != nil {
		return r.fail(err)
	}
	defer c.Close()
	start, err := r.call(ctx, c, "transfer.start", params, callTimeout)
	if err != nil {
		return r.failMut(err, host)
	}
	r.reqID, r.jobID = start.str("request_id"), start.str("job_id")
	id := start.str("transfer_id")
	if id == "" {
		if code := r.settle(start, "전송 시작"); code != exitOK {
			return code
		}
		return r.unknownExitWhy(start, "전송 시작", "전송 ID를 받지 못했습니다")
	}
	if *detach {
		if r.asJSON {
			writeJSON(r.Stdout, start)
		} else {
			fmt.Fprintln(r.Stdout, id)
			r.info("전송을 시작했습니다. deskhand transfer watch %s 로 진행을 보세요", id)
		}
		return exitOK
	}
	return r.followTransfer(ctx, c, id, *jsonl, start)
}

// xferUp reports whether files push/pull/move sends from this Mac to the PC,
// and checks that the two arguments fit the command.
func (r *run) xferUp(name string, pos []string) (bool, error) {
	switch name {
	case "push":
		if !isLocalPath(pos[0]) {
			return false, usagef("files push의 첫 인수 %q는 이 Mac의 경로여야 합니다. deskhand files push <로컬 경로> <호스트>:<원격 경로> 형식으로 주세요", pos[0])
		}
		return true, nil
	case "pull":
		if !isLocalPath(pos[1]) {
			return false, usagef("files pull의 둘째 인수 %q는 이 Mac의 경로여야 합니다. deskhand files pull <호스트>:<원격 경로> <로컬 경로> 형식으로 주세요", pos[1])
		}
		return false, nil
	}
	a, b := isLocalPath(pos[0]), isLocalPath(pos[1])
	if a == b {
		return false, usagef("files move는 한쪽만 <호스트>:<경로>여야 합니다. 같은 PC 안에서 옮기려면 deskhand files mv를 쓰세요")
	}
	return a, nil
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func (r *run) getTransfer(ctx context.Context, c *client.Client, id string) (obj, error) {
	return r.call(ctx, c, "transfer.get", map[string]any{"transfer_id": id}, callTimeout)
}

// followTransfer shows the progress of transfer id until it ends or pauses
// and returns the exit code for its last state. start is the envelope of
// transfer.start, or nil.
func (r *run) followTransfer(ctx context.Context, c *client.Client, id string, jsonl bool, start obj) int {
	bar := r.Progress != nil && r.Progress() && !r.quiet && !jsonl
	events, err := c.Subscribe(ctx, []string{"transfer.updated"})
	if err != nil {
		events = nil
	}
	cur, err := r.getTransfer(ctx, c, id)
	if err != nil {
		if ctx.Err() != nil {
			return r.transferInterrupted(id)
		}
		return r.failMut(err, "")
	}
	lastRev := int64(-1)
	accept := func(t, extra obj) {
		rev, _ := t.num("rev")
		if rev <= lastRev {
			return
		}
		lastRev, cur = rev, t
		if jsonl {
			ev := obj{"kind": "transfer.updated", "transfer": t}
			if cf, ok := extra["changed_files"]; ok {
				ev["changed_files"] = cf
			}
			writeJSON(r.Stdout, ev)
		}
		if bar {
			fmt.Fprint(r.Stderr, "\r\x1b[2K"+progressLine(t, progressBarWidth))
		}
	}
	erase := func() {
		if bar {
			fmt.Fprint(r.Stderr, "\r\x1b[2K")
		}
	}
	accept(cur, nil)
	for !transferEnded(cur.str("state")) && cur.str("state") != statePaused {
		select {
		case <-ctx.Done():
			erase()
			return r.transferInterrupted(id)
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if t := toObj(ev.Params["transfer"]); ev.Kind == "transfer.updated" && t.str("transfer_id") == id {
				accept(t, toObj(ev.Params))
			}
		case <-time.After(r.PollInterval):
			d, err := r.getTransfer(ctx, c, id)
			if err != nil {
				erase()
				if ctx.Err() != nil {
					return r.transferInterrupted(id)
				}
				return r.failMut(err, "")
			}
			accept(d, nil)
		}
	}
	erase()
	detail, err := r.getTransfer(ctx, c, id)
	if err != nil {
		if ctx.Err() != nil {
			return r.transferInterrupted(id)
		}
		detail = cur
	}
	accept(detail, nil)
	return r.reportTransfer(detail, start, jsonl)
}

func (r *run) transferInterrupted(id string) int {
	fmt.Fprintf(r.Stderr, "중단했습니다. 전송 %s는 계속 진행됩니다. deskhand transfer watch %s 로 다시 보거나 deskhand transfer cancel %s 로 취소하세요\n", id, id, id)
	return exitUnknown
}

// detailJSON is the transfer detail with the ids of the starting call and
// the fate of the source of a move.
func detailJSON(d, start obj) obj {
	out := obj{}
	for k, v := range d {
		out[k] = v
	}
	for _, k := range []string{"request_id", "job_id"} {
		if v := start.str(k); v != "" {
			out[k] = v
		}
	}
	if s := sourceOutcome(d); s != "" {
		out["source"] = s
	}
	return out
}

// reportTransfer prints the result of a transfer in its last state and
// returns the exit code.
func (r *run) reportTransfer(d, start obj, jsonl bool) int {
	code := transferExit(d)
	if r.asJSON && !jsonl {
		writeJSON(r.Stdout, detailJSON(d, start))
	}
	if jsonl {
		return code
	}
	id := d.str("transfer_id")
	state := d.str("state")
	files := d.list("files")
	count := func(outcome string) int {
		n := 0
		for _, f := range files {
			if f.str("outcome") == outcome {
				n++
			}
		}
		return n
	}
	src, dst := d.str("host_id")+":"+d.str("remote"), d.str("local")
	if strings.HasSuffix(d.str("direction"), "push") {
		src, dst = d.str("local"), d.str("host_id")+":"+d.str("remote")
	}
	switch state {
	case stateDone:
		verb := map[string]string{"push": "올렸습니다", "pull": "받았습니다", "move_push": "옮겼습니다", "move_pull": "옮겼습니다"}[d.str("direction")]
		total, _ := d.num("bytes_total")
		done, _ := d.num("files_done")
		line := fmt.Sprintf("%s: %s → %s (%d개 파일, %s)", verb, src, dst, done, humanSize(total))
		if n := count(outcomeSkipped); n > 0 {
			line += fmt.Sprintf(", 대상에 이미 있어 건너뛴 파일 %d개", n)
		}
		if n := count("renamed"); n > 0 {
			line += fmt.Sprintf(", 이름을 바꿔 둔 파일 %d개", n)
		}
		r.say("%s", line)
		if s := moveSentence(d); s != "" {
			if code == exitOK {
				r.say("%s", s)
			} else {
				fmt.Fprintln(r.Stderr, s)
			}
		}
		return code
	case statePaused:
		fmt.Fprintf(r.Stderr, "전송 %s가 일시 정지 상태입니다. deskhand transfer resume %s 로 다시 시작하세요\n", id, id)
	case stateCancelled:
		fmt.Fprintf(r.Stderr, "전송 %s가 취소됐습니다. 끝난 파일은 대상에 남아 있습니다\n", id)
	default:
		failed, _ := d.num("files_failed")
		total, _ := d.num("files_total")
		msg := d.sub("error").str("message")
		fmt.Fprintf(r.Stderr, "전송 %s가 실패했습니다: 파일 %d개 중 %d개가 실패했습니다", id, total, failed)
		if msg != "" {
			fmt.Fprintf(r.Stderr, " (%s)", msg)
		}
		fmt.Fprintln(r.Stderr)
		shown := 0
		for _, f := range files {
			if f.str("state") != stateFailed {
				continue
			}
			if shown == 5 {
				fmt.Fprintf(r.Stderr, "  ... 나머지는 --json으로 확인하세요\n")
				break
			}
			fmt.Fprintf(r.Stderr, "  %s: %s\n", f.str("rel"), dash(f.sub("error").str("message")))
			shown++
		}
		fmt.Fprintf(r.Stderr, "다음 단계: deskhand transfer resume %s 로 실패한 파일부터 이어서 보내세요\n", id)
	}
	if s := moveSentence(d); s != "" {
		fmt.Fprintln(r.Stderr, s)
	}
	return code
}

func (r *run) transfer(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return r.fail(usagef("deskhand transfer list|get|watch|pause|resume|cancel 중 하나를 고르세요"))
	}
	switch args[0] {
	case "list":
		return r.transferList(ctx, args[1:])
	case "get":
		return r.transferGet(ctx, args[1:])
	case "watch":
		return r.transferWatch(ctx, args[1:], false)
	case "resume":
		return r.transferWatch(ctx, args[1:], true)
	case "pause":
		return r.transferControl(ctx, "pause", args[1:])
	case "cancel":
		return r.transferControl(ctx, "cancel", args[1:])
	}
	return r.fail(usagef("알 수 없는 transfer 명령 %q입니다. list, get, watch, pause, resume, cancel 중 하나를 고르세요", args[0]))
}

func (r *run) transferList(ctx context.Context, args []string) int {
	fs := newFlags("transfer list")
	host := fs.String("host", "", "호스트")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 0 {
		return r.fail(usagef("deskhand transfer list [--host H] 형식으로 주세요"))
	}
	params := map[string]any{}
	if *host != "" {
		params["host_id"] = *host
	}
	res, err := r.simple(ctx, "transfer.list", params)
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	list := res.list("transfers")
	if len(list) == 0 {
		fmt.Fprintln(r.Stdout, "전송이 없습니다")
		return exitOK
	}
	rows := make([][]string, 0, len(list))
	for _, t := range list {
		done, _ := t.num("bytes_done")
		total, _ := t.num("bytes_total")
		fd, _ := t.num("files_done")
		ft, _ := t.num("files_total")
		rows = append(rows, []string{t.str("transfer_id"), t.str("host_id"), directionKorean(t.str("direction")), stateKorean(t.str("state")),
			fmt.Sprintf("%d/%d개 %s/%s", fd, ft, humanSize(done), humanSize(total)), t.str("updated_at")})
	}
	writeTable(r.Stdout, []string{"전송", "호스트", "방향", "상태", "진행", "갱신"}, rows)
	return exitOK
}

func (r *run) transferGet(ctx context.Context, args []string) int {
	pos, _, err := parseArgs(newFlags("transfer get"), args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 {
		return r.fail(usagef("deskhand transfer get <전송-id> 형식으로 주세요"))
	}
	res, err := r.simple(ctx, "transfer.get", map[string]any{"transfer_id": pos[0]})
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, detailJSON(res, nil))
		return exitOK
	}
	writeTransferDetail(r.Stdout, res)
	return exitOK
}

func writeTransferDetail(w io.Writer, d obj) {
	fmt.Fprintf(w, "전송: %s\n호스트: %s\n방향: %s\n상태: %s\n로컬 경로(Mac): %s\n원격 경로: %s\n", d.str("transfer_id"), d.str("host_id"),
		directionKorean(d.str("direction")), stateKorean(d.str("state")), d.str("local"), d.str("remote"))
	fmt.Fprintf(w, "진행: %s\n", progressLine(d, progressBarWidth))
	if e := d.sub("error"); e != nil {
		fmt.Fprintf(w, "오류: %s\n", e.str("message"))
	}
	if s := moveSentence(d); s != "" {
		fmt.Fprintln(w, s)
	}
	files := d.list("files")
	if len(files) == 0 {
		return
	}
	rows := make([][]string, 0, len(files))
	for _, f := range files {
		size, _ := f.num("size")
		note := f.str("outcome")
		if m := f.sub("error").str("message"); m != "" {
			note = m
		}
		rows = append(rows, []string{f.str("rel"), humanSize(size), stateKorean(f.str("state")), note})
	}
	writeTable(w, []string{"파일", "크기", "상태", "비고"}, rows)
}

// transferWatch follows a transfer; with resume it restarts the transfer
// first.
func (r *run) transferWatch(ctx context.Context, args []string, resume bool) int {
	name := "transfer watch"
	if resume {
		name = "transfer resume"
	}
	fs := newFlags(name)
	jsonl := fs.Bool("jsonl", false, "이벤트를 JSON 한 줄씩 출력")
	detach := fs.Bool("detach", false, "다시 시작만 하고 돌아옴")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 || (*detach && !resume) {
		return r.fail(usagef("deskhand %s <전송-id> [--jsonl]%s 형식으로 주세요", name, map[bool]string{true: " [--detach]", false: ""}[resume]))
	}
	if *detach && *jsonl {
		return r.fail(usagef("--detach에는 --jsonl을 쓸 수 없습니다"))
	}
	c, err := r.dial(ctx)
	if err != nil {
		return r.fail(err)
	}
	defer c.Close()
	if resume {
		res, err := r.call(ctx, c, "transfer.resume", map[string]any{"transfer_id": pos[0]}, callTimeout)
		if err != nil {
			return r.failMut(err, "")
		}
		if *detach {
			if r.asJSON {
				writeJSON(r.Stdout, detailJSON(res, nil))
			} else {
				r.say("전송 %s를 다시 시작했습니다(상태: %s)", pos[0], stateKorean(res.str("state")))
			}
			return exitOK
		}
	}
	return r.followTransfer(ctx, c, pos[0], *jsonl, nil)
}

// transferControl pauses or cancels a transfer.
func (r *run) transferControl(ctx context.Context, action string, args []string) int {
	pos, _, err := parseArgs(newFlags("transfer "+action), args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 {
		return r.fail(usagef("deskhand transfer %s <전송-id> 형식으로 주세요", action))
	}
	res, err := r.simple(ctx, "transfer."+action, map[string]any{"transfer_id": pos[0]})
	if err != nil {
		return r.failMut(err, "")
	}
	if r.asJSON {
		writeJSON(r.Stdout, detailJSON(res, nil))
		return exitOK
	}
	if action == "pause" {
		r.say("전송 %s를 일시 정지했습니다(상태: %s). deskhand transfer resume %s 로 다시 시작하세요", pos[0], stateKorean(res.str("state")), pos[0])
		return exitOK
	}
	done, _ := res.num("files_done")
	r.say("전송 %s를 취소했습니다(상태: %s). 끝난 파일 %d개는 대상에 남아 있습니다", pos[0], stateKorean(res.str("state")), done)
	if s := moveSentence(res); s != "" {
		r.say("%s", s)
	}
	return exitOK
}
