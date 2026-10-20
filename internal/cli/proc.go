package cli

import (
	"context"
	"fmt"
	"strconv"
)

func (r *run) proc(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return r.fail(usagef("deskhand proc ls|kill 중 하나를 고르세요"))
	}
	switch args[0] {
	case "ls":
		return r.procList(ctx, args[1:])
	case "kill":
		return r.procKill(ctx, args[1:])
	}
	return r.fail(usagef("알 수 없는 proc 명령 %q입니다. ls, kill 중 하나를 고르세요", args[0]))
}

func (r *run) procList(ctx context.Context, args []string) int {
	fs := newFlags("proc ls")
	name := fs.String("name", "", "프로세스 이름")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 {
		return r.fail(usagef("deskhand proc ls <호스트> [--name N] 형식으로 주세요"))
	}
	params := map[string]any{"host_id": pos[0]}
	if *name != "" {
		params["name"] = *name
	}
	res, err := r.simple(ctx, "proc.list", params)
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	procs := res.list("procs")
	if len(procs) == 0 {
		fmt.Fprintln(r.Stdout, "일치하는 프로세스가 없습니다")
		return exitOK
	}
	rows := make([][]string, 0, len(procs))
	for _, p := range procs {
		pid, _ := p.num("pid")
		mem, _ := p.num("mem_kb")
		by := ""
		switch {
		case p.boolean("self"):
			by = "에이전트"
		case p.boolean("started_by_deskhand"):
			by = "시작함"
		}
		cpu := "-"
		if c := p.str("cpu_pct"); c != "" {
			cpu = c + "%"
		}
		started := "-"
		if s := p.str("started"); s != "" {
			started = localTime(s, "2006-01-02 15:04")
		}
		rows = append(rows, []string{strconv.FormatInt(pid, 10), p.str("name"), cpu, humanSize(mem * 1024), dash(p.str("user")), started, dash(by)})
	}
	writeTable(r.Stdout, []string{"PID", "이름", "CPU", "메모리", "사용자", "시작", "Deskhand"}, rows)
	return exitOK
}

func (r *run) procKill(ctx context.Context, args []string) int {
	fs := newFlags("proc kill")
	tree := fs.Bool("tree", false, "자식 프로세스까지 종료")
	started := fs.String("started", "", "proc ls --json의 started 값(그 PID가 다른 프로세스면 종료하지 않음)")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 2 {
		return r.fail(usagef("deskhand proc kill <호스트> <pid> [--tree] [--started T] 형식으로 주세요"))
	}
	pid, perr := strconv.Atoi(pos[1])
	if perr != nil || pid <= 0 {
		return r.fail(usagef("pid %q가 올바르지 않습니다. 양의 정수 PID를 주세요", pos[1]))
	}
	params := map[string]any{"host_id": pos[0], "pid": pid}
	if *tree {
		params["tree"] = true
	}
	if *started != "" {
		params["started"] = *started
	}
	res, err := r.simple(ctx, "proc.kill", params)
	if err != nil {
		return r.failMut(err, pos[0])
	}
	code := r.settle(res, "프로세스 종료")
	if code == exitOK {
		n, _ := res.num("killed")
		r.say("프로세스 %d개를 종료했습니다", n)
	}
	return code
}
