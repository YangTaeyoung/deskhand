package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const debugUsage = `deskhand debug <명령> — CDP 디버거

명령:
  launch <호스트> <경로> [--arg A]… [--cwd D] [--break] [--bp M:P:L]… [--exceptions 모드]
  attach <호스트> (--port P [--role main|renderer] | --pid N) [--bp M:P:L]… [--exceptions 모드]
  list [호스트]                         세션 목록
  get <debug_id>                        세션 상세
  bp set <debug_id> <match> <pattern> <line> [--col C] [--cond 식]
  bp rm <debug_id> <breakpoint_id>
  bp enable <debug_id> <breakpoint_id> [--off]
  exceptions <debug_id> <none|uncaught|all>
  pause|resume <debug_id> <target_id>
  step <debug_id> <target_id> <over|into|out>
  state <debug_id> <target_id>
  vars <debug_id> <target_id> <object_id> [--offset O] [--limit L]
  eval <debug_id> <target_id> <식> [--frame F]
  scripts <debug_id> [target_id]
  source <debug_id> <target_id> <script_id>
  console <debug_id> [--cursor C | --before B] [--limit L]
  close <debug_id> [--keep]`

func (r *run) debug(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return r.fail(usagef("%s", debugUsage))
	}
	switch args[0] {
	case "launch":
		return r.debugLaunch(ctx, args[1:])
	case "attach":
		return r.debugAttach(ctx, args[1:])
	case "list":
		return r.debugList(ctx, args[1:])
	case "get":
		return r.debugGet(ctx, args[1:])
	case "bp":
		return r.debugBP(ctx, args[1:])
	case "exceptions":
		return r.debugExceptions(ctx, args[1:])
	case "pause", "resume":
		return r.debugPauseResume(ctx, args[0], args[1:])
	case "step":
		return r.debugStep(ctx, args[1:])
	case "state":
		return r.debugState(ctx, args[1:])
	case "vars":
		return r.debugVars(ctx, args[1:])
	case "eval":
		return r.debugEval(ctx, args[1:])
	case "scripts":
		return r.debugScripts(ctx, args[1:])
	case "source":
		return r.debugSource(ctx, args[1:])
	case "console":
		return r.debugConsole(ctx, args[1:])
	case "close":
		return r.debugClose(ctx, args[1:])
	case "help", "-h", "--help":
		fmt.Fprintln(r.Stdout, debugUsage)
		return exitOK
	}
	return r.fail(usagef("알 수 없는 debug 명령 %q입니다. deskhand debug help로 확인하세요", args[0]))
}

// parseBreakSpecs reads repeated --bp match:pattern:line[:col] into
// breakpoint specs, splitting from the right so a pattern with colons (a
// file:/// URL, a Windows drive) stays whole.
func parseBreakSpecs(vals []string) ([]map[string]any, error) {
	var out []map[string]any
	for _, v := range vals {
		i := strings.IndexByte(v, ':')
		if i < 0 {
			return nil, usagef("--bp는 match:pattern:line[:col] 형식이어야 합니다(예: file_name:main.js:10): %q", v)
		}
		match, rest := v[:i], v[i+1:]
		tokens := strings.Split(rest, ":")
		line, col, patternEnd := 0, 0, len(tokens)
		// Peel one or two trailing numeric tokens as line and optional col.
		if n := len(tokens); n >= 2 {
			if a, ea := strconv.Atoi(tokens[n-2]); ea == nil && a >= 1 {
				if b, eb := strconv.Atoi(tokens[n-1]); eb == nil && b >= 1 {
					line, col, patternEnd = a, b, n-2
				}
			}
		}
		if line == 0 {
			last, err := strconv.Atoi(tokens[len(tokens)-1])
			if err != nil || last < 1 {
				return nil, usagef("--bp의 line은 1 이상의 숫자여야 합니다: %q", v)
			}
			line, patternEnd = last, len(tokens)-1
		}
		pattern := strings.Join(tokens[:patternEnd], ":")
		if match == "" || pattern == "" {
			return nil, usagef("--bp는 match:pattern:line[:col] 형식이어야 합니다: %q", v)
		}
		spec := map[string]any{"match": match, "pattern": pattern, "line": line}
		if col > 0 {
			spec["column"] = col
		}
		out = append(out, spec)
	}
	return out, nil
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func (r *run) debugLaunch(ctx context.Context, args []string) int {
	fs := newFlags("debug launch")
	var argv, bps stringList
	fs.Var(&argv, "arg", "앱에 넘길 인수(여러 번)")
	fs.Var(&bps, "bp", "중단점 match:pattern:line(여러 번)")
	cwd := fs.String("cwd", "", "작업 폴더")
	brk := fs.Bool("break", false, "첫 줄에서 멈춤(--inspect-brk)")
	exc := fs.String("exceptions", "", "예외 중단 모드 none|uncaught|all")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 2 {
		return r.fail(usagef("deskhand debug launch <호스트> <경로> 형식으로 주세요"))
	}
	specs, err := parseBreakSpecs(bps)
	if err != nil {
		return r.fail(err)
	}
	params := map[string]any{"host_id": pos[0], "path": pos[1], "pause_on_start": *brk}
	if len(argv) > 0 {
		params["args"] = []string(argv)
	}
	if *cwd != "" {
		params["cwd"] = *cwd
	}
	if len(specs) > 0 {
		params["breakpoints"] = specs
	}
	if *exc != "" {
		params["pause_on_exceptions"] = *exc
	}
	res, err := r.simple(ctx, "debug.electron.launch", params)
	if err != nil {
		return r.failMut(err, pos[0])
	}
	r.printSession(res.sub("session"))
	return r.settle(res, "디버그 실행")
}

func (r *run) debugAttach(ctx context.Context, args []string) int {
	fs := newFlags("debug attach")
	var bps stringList
	fs.Var(&bps, "bp", "중단점 match:pattern:line(여러 번)")
	port := fs.Int("port", 0, "원격 PC 루프백 포트")
	pid := fs.Int("pid", 0, "부착할 프로세스 PID")
	role := fs.String("role", "", "main 또는 renderer")
	exc := fs.String("exceptions", "", "예외 중단 모드")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 {
		return r.fail(usagef("deskhand debug attach <호스트> --port P | --pid N 형식으로 주세요"))
	}
	if (*port == 0) == (*pid == 0) {
		return r.fail(usagef("--port나 --pid 중 하나만 주세요"))
	}
	specs, err := parseBreakSpecs(bps)
	if err != nil {
		return r.fail(err)
	}
	params := map[string]any{"host_id": pos[0]}
	if *port != 0 {
		params["port"] = *port
	}
	if *pid != 0 {
		params["pid"] = *pid
	}
	if *role != "" {
		params["role"] = *role
	}
	if len(specs) > 0 {
		params["breakpoints"] = specs
	}
	if *exc != "" {
		params["pause_on_exceptions"] = *exc
	}
	res, err := r.simple(ctx, "debug.attach", params)
	if err != nil {
		return r.failMut(err, pos[0])
	}
	r.printSession(res.sub("session"))
	return r.settle(res, "디버그 부착")
}

func (r *run) debugList(ctx context.Context, args []string) int {
	params := map[string]any{}
	if len(args) == 1 {
		params["host_id"] = args[0]
	} else if len(args) > 1 {
		return r.fail(usagef("deskhand debug list [호스트] 형식으로 주세요"))
	}
	res, err := r.simple(ctx, "debug.list", params)
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	sessions := res.list("sessions")
	if len(sessions) == 0 {
		fmt.Fprintln(r.Stdout, "열린 디버그 세션이 없습니다")
		return exitOK
	}
	rows := make([][]string, 0, len(sessions))
	for _, s := range sessions {
		rows = append(rows, []string{s.str("debug_id"), s.str("host_id"), s.str("kind"), s.str("name"), s.str("state"),
			strconv.Itoa(len(s.list("targets")))})
	}
	writeTable(r.Stdout, []string{"ID", "PC", "종류", "이름", "상태", "대상"}, rows)
	return exitOK
}

func (r *run) debugGet(ctx context.Context, args []string) int {
	if len(args) != 1 {
		return r.fail(usagef("deskhand debug get <debug_id> 형식으로 주세요"))
	}
	res, err := r.simple(ctx, "debug.get", map[string]any{"debug_id": args[0]})
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	r.printSession(res)
	return exitOK
}

func (r *run) printSession(s obj) {
	if s == nil || r.asJSON {
		return
	}
	fmt.Fprintf(r.Stdout, "세션 %s (%s) 상태 %s\n", s.str("debug_id"), s.str("name"), s.str("state"))
	if reason := s.str("main_unavailable_reason"); reason != "" {
		fmt.Fprintf(r.Stdout, "  main 없음: %s\n", reason)
	}
	if reason := s.str("renderer_unavailable_reason"); reason != "" {
		fmt.Fprintf(r.Stdout, "  renderer 없음: %s\n", reason)
	}
	for _, t := range s.list("targets") {
		fmt.Fprintf(r.Stdout, "  대상 %s [%s/%s] paused=%v %s\n", t.str("id"), t.str("role"), t.str("type"), t.get("paused"), t.str("url"))
	}
	for _, b := range s.list("breakpoints") {
		fmt.Fprintf(r.Stdout, "  중단점 %s %s:%s:%v resolved=%v\n", b.str("breakpoint_id"), b.str("match"), b.str("pattern"), b.get("line"), b.get("resolved"))
	}
}

func (r *run) debugBP(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return r.fail(usagef("deskhand debug bp set|rm|enable … 형식으로 주세요"))
	}
	switch args[0] {
	case "set":
		return r.debugBPSet(ctx, args[1:])
	case "rm":
		return r.debugBPRemove(ctx, args[1:])
	case "enable":
		return r.debugBPEnable(ctx, args[1:])
	}
	return r.fail(usagef("알 수 없는 bp 명령 %q입니다(set|rm|enable)", args[0]))
}

func (r *run) debugBPSet(ctx context.Context, args []string) int {
	fs := newFlags("debug bp set")
	col := fs.Int("col", 0, "열(1부터)")
	cond := fs.String("cond", "", "조건식")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 4 {
		return r.fail(usagef("deskhand debug bp set <debug_id> <match> <pattern> <line> 형식으로 주세요"))
	}
	line, err := strconv.Atoi(pos[3])
	if err != nil || line < 1 {
		return r.fail(usagef("line은 1 이상의 숫자여야 합니다: %q", pos[3]))
	}
	params := map[string]any{"debug_id": pos[0], "match": pos[1], "pattern": pos[2], "line": line}
	if *col != 0 {
		params["column"] = *col
	}
	if *cond != "" {
		params["condition"] = *cond
	}
	res, err := r.simple(ctx, "debug.breakpoint.set", params)
	if err != nil {
		return r.fail(err)
	}
	if bp := res.sub("breakpoint"); bp != nil {
		r.say("중단점 %s (resolved=%v)", bp.str("breakpoint_id"), bp.get("resolved"))
	}
	return r.settle(res, "중단점 설정")
}

func (r *run) debugBPRemove(ctx context.Context, args []string) int {
	if len(args) != 2 {
		return r.fail(usagef("deskhand debug bp rm <debug_id> <breakpoint_id> 형식으로 주세요"))
	}
	res, err := r.simple(ctx, "debug.breakpoint.remove", map[string]any{"debug_id": args[0], "breakpoint_id": args[1]})
	if err != nil {
		return r.fail(err)
	}
	r.say("중단점 %s를 지웠습니다", args[1])
	return r.settle(res, "중단점 삭제")
}

func (r *run) debugBPEnable(ctx context.Context, args []string) int {
	fs := newFlags("debug bp enable")
	off := fs.Bool("off", false, "중단점을 끔")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 2 {
		return r.fail(usagef("deskhand debug bp enable <debug_id> <breakpoint_id> [--off] 형식으로 주세요"))
	}
	res, err := r.simple(ctx, "debug.breakpoint.update", map[string]any{"debug_id": pos[0], "breakpoint_id": pos[1], "enabled": !*off})
	if err != nil {
		return r.fail(err)
	}
	return r.settle(res, "중단점 변경")
}

func (r *run) debugExceptions(ctx context.Context, args []string) int {
	if len(args) != 2 {
		return r.fail(usagef("deskhand debug exceptions <debug_id> <none|uncaught|all> 형식으로 주세요"))
	}
	res, err := r.simple(ctx, "debug.exceptions", map[string]any{"debug_id": args[0], "mode": args[1]})
	if err != nil {
		return r.fail(err)
	}
	return r.settle(res, "예외 중단")
}

func (r *run) debugPauseResume(ctx context.Context, cmd string, args []string) int {
	if len(args) != 2 {
		return r.fail(usagef("deskhand debug %s <debug_id> <target_id> 형식으로 주세요", cmd))
	}
	res, err := r.simple(ctx, "debug."+cmd, map[string]any{"debug_id": args[0], "target_id": args[1]})
	if err != nil {
		return r.failMut(err, "")
	}
	return r.settle(res, "디버그 "+cmd)
}

func (r *run) debugStep(ctx context.Context, args []string) int {
	if len(args) != 3 {
		return r.fail(usagef("deskhand debug step <debug_id> <target_id> <over|into|out> 형식으로 주세요"))
	}
	res, err := r.simple(ctx, "debug.step", map[string]any{"debug_id": args[0], "target_id": args[1], "kind": args[2]})
	if err != nil {
		return r.failMut(err, "")
	}
	return r.settle(res, "한 단계 실행")
}

func (r *run) debugState(ctx context.Context, args []string) int {
	if len(args) != 2 {
		return r.fail(usagef("deskhand debug state <debug_id> <target_id> 형식으로 주세요"))
	}
	res, err := r.simple(ctx, "debug.state", map[string]any{"debug_id": args[0], "target_id": args[1]})
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	if res.get("paused") != true {
		fmt.Fprintln(r.Stdout, "멈추지 않았습니다")
		return exitOK
	}
	fmt.Fprintf(r.Stdout, "멈춤(%s)\n", res.str("reason"))
	for _, f := range res.list("call_frames") {
		fmt.Fprintf(r.Stdout, "  %s %s:%v\n", dash(f.str("function")), f.str("url"), f.get("line"))
		for _, sc := range f.list("scope_chain") {
			fmt.Fprintf(r.Stdout, "    scope %s %s\n", sc.str("type"), sc.str("object_id"))
		}
	}
	return exitOK
}

func (r *run) debugVars(ctx context.Context, args []string) int {
	fs := newFlags("debug vars")
	offset := fs.Int("offset", 0, "건너뛸 개수")
	limit := fs.Int("limit", 0, "최대 개수")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 3 {
		return r.fail(usagef("deskhand debug vars <debug_id> <target_id> <object_id> 형식으로 주세요"))
	}
	params := map[string]any{"debug_id": pos[0], "target_id": pos[1], "object_id": pos[2]}
	if *offset != 0 {
		params["offset"] = *offset
	}
	if *limit != 0 {
		params["limit"] = *limit
	}
	res, err := r.simple(ctx, "debug.variables", params)
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	for _, p := range res.list("properties") {
		fmt.Fprintf(r.Stdout, "  %s = %s\n", p.str("name"), p.str("value_preview"))
	}
	fmt.Fprintf(r.Stdout, "전체 %v개\n", res.get("total"))
	return exitOK
}

func (r *run) debugEval(ctx context.Context, args []string) int {
	fs := newFlags("debug eval")
	frame := fs.String("frame", "", "프레임 ID")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 3 {
		return r.fail(usagef("deskhand debug eval <debug_id> <target_id> <식> 형식으로 주세요"))
	}
	params := map[string]any{"debug_id": pos[0], "target_id": pos[1], "expression": pos[2]}
	if *frame != "" {
		params["frame_id"] = *frame
	}
	res, err := r.simple(ctx, "debug.evaluate", params)
	if err != nil {
		return r.failMut(err, "")
	}
	if !r.asJSON {
		if ex := res.sub("exception"); ex != nil {
			fmt.Fprintf(r.Stdout, "예외: %s\n", ex.str("text"))
		} else {
			fmt.Fprintf(r.Stdout, "%s\n", res.str("value_preview"))
		}
	}
	return r.settle(res, "식 평가")
}

func (r *run) debugScripts(ctx context.Context, args []string) int {
	if len(args) < 1 || len(args) > 2 {
		return r.fail(usagef("deskhand debug scripts <debug_id> [target_id] 형식으로 주세요"))
	}
	params := map[string]any{"debug_id": args[0]}
	if len(args) == 2 {
		params["target_id"] = args[1]
	}
	res, err := r.simple(ctx, "debug.scripts", params)
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	for _, s := range res.list("scripts") {
		fmt.Fprintf(r.Stdout, "  %s %s\n", s.str("script_id"), s.str("url"))
	}
	return exitOK
}

func (r *run) debugSource(ctx context.Context, args []string) int {
	if len(args) != 3 {
		return r.fail(usagef("deskhand debug source <debug_id> <target_id> <script_id> 형식으로 주세요"))
	}
	res, err := r.simple(ctx, "debug.source", map[string]any{"debug_id": args[0], "target_id": args[1], "script_id": args[2]})
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	fmt.Fprint(r.Stdout, res.str("text"))
	if res.get("truncated") == true {
		fmt.Fprintln(r.Stderr, "\n(2 MiB에서 잘렸습니다)")
	}
	return exitOK
}

func (r *run) debugConsole(ctx context.Context, args []string) int {
	fs := newFlags("debug console")
	cursor := fs.Int("cursor", -1, "이 seq 다음 항목")
	before := fs.Int("before", -1, "이 seq 이전 항목")
	limit := fs.Int("limit", 0, "최대 개수")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 {
		return r.fail(usagef("deskhand debug console <debug_id> 형식으로 주세요"))
	}
	params := map[string]any{"debug_id": pos[0]}
	if *cursor >= 0 {
		params["cursor"] = *cursor
	}
	if *before >= 0 {
		params["before"] = *before
	}
	if *limit != 0 {
		params["limit"] = *limit
	}
	res, err := r.simple(ctx, "debug.console", params)
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	for _, e := range res.list("entries") {
		fmt.Fprintf(r.Stdout, "[%v] %s %s %s\n", e.get("seq"), e.str("source"), e.str("level"), e.str("text"))
	}
	if res.get("gap") == true {
		fmt.Fprintln(r.Stderr, "(이 앞의 항목 일부가 버퍼에서 밀려났습니다)")
	}
	return exitOK
}

func (r *run) debugClose(ctx context.Context, args []string) int {
	fs := newFlags("debug close")
	keep := fs.Bool("keep", false, "프로세스를 끝내지 않음")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 {
		return r.fail(usagef("deskhand debug close <debug_id> [--keep] 형식으로 주세요"))
	}
	params := map[string]any{"debug_id": pos[0], "keep_process": *keep}
	res, err := r.simple(ctx, "debug.close", params)
	if err != nil {
		return r.fail(err)
	}
	if res.get("inspector_left_open") == true {
		r.say("검사기를 닫지 못했습니다(inspector_left_open)")
	}
	return r.settle(res, "디버그 세션 닫기")
}
