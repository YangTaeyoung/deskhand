package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const diagUsage = "deskhand diag system|services|events|crashes <호스트> 형식으로 주세요"

func (r *run) diag(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return r.fail(usagef("%s", diagUsage))
	}
	switch args[0] {
	case "system":
		return r.diagSystem(ctx, args[1:])
	case "services":
		return r.diagServices(ctx, args[1:])
	case "events":
		return r.diagEvents(ctx, args[1:])
	case "crashes":
		return r.diagCrashes(ctx, args[1:])
	}
	return r.fail(usagef("알 수 없는 diag 명령 %q입니다. system, services, events, crashes 중 하나를 고르세요", args[0]))
}

// diagCall calls method with the host of pos and params, prints the result
// in --json mode and returns it with the exit code of its envelope; print
// writes the human output of a success. A saved result names its artifact
// on stderr.
func (r *run) diagCall(ctx context.Context, method, usage string, pos []string, save bool, params map[string]any, print func(obj)) int {
	if len(pos) != 1 {
		return r.fail(usagef("%s", usage))
	}
	params["host_id"] = pos[0]
	if save {
		params["save"] = true
	}
	res, err := r.simple(ctx, method, params)
	if err != nil {
		return r.fail(err)
	}
	if code := r.settle(res, "진단"); code != exitOK {
		return code
	}
	if !r.asJSON && !r.quiet {
		print(res)
	}
	for _, a := range res.list("artifacts") {
		r.info("결과를 산출물로 저장했습니다: %s", a.str("path"))
	}
	return exitOK
}

func (r *run) diagSystem(ctx context.Context, args []string) int {
	fs := newFlags("diag system")
	save := fs.Bool("save", false, "결과를 산출물로 저장")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	return r.diagCall(ctx, "diag.system", "deskhand diag system <호스트> [--save] 형식으로 주세요", pos, *save, map[string]any{}, r.printSystem)
}

// printSystem prints a diag.system result.
func (r *run) printSystem(res obj) {
	o, cpu, mem := res.sub("os"), res.sub("cpu"), res.sub("memory")
	cores, _ := cpu.num("cores")
	threads, _ := cpu.num("threads")
	total, _ := mem.num("total_bytes")
	used, _ := mem.num("used_bytes")
	uptime, _ := res.num("uptime_s")
	usage := cpu.str("usage_pct")
	rows := [][]string{
		{"컴퓨터", dash(res.str("computer_name"))},
		{"OS", fmt.Sprintf("%s %s (빌드 %s, %s)", o.str("name"), o.str("version"), dash(o.str("build")), dash(o.str("edition")))},
		{"CPU", fmt.Sprintf("%s · %d코어 %d스레드 · 사용률 %s%%", dash(cpu.str("name")), cores, threads, usage)},
		{"메모리", fmt.Sprintf("%s / %s 사용", humanSize(used), humanSize(total))},
		{"부팅", fmt.Sprintf("%s (가동 %s)", localTime(res.str("boot_time"), "2006-01-02 15:04"), uptimeText(uptime))},
	}
	for _, row := range rows {
		fmt.Fprintf(r.Stdout, "%s%s  %s\n", row[0], strings.Repeat(" ", 6-displayWidth(row[0])), row[1])
	}
	if disks := res.list("disks"); len(disks) > 0 {
		fmt.Fprintln(r.Stdout)
		var table [][]string
		for _, d := range disks {
			free, _ := d.num("free_bytes")
			size, _ := d.num("total_bytes")
			table = append(table, []string{d.str("root"), dash(d.str("label")), dash(d.str("fs")), humanSize(free), humanSize(size)})
		}
		writeTable(r.Stdout, []string{"디스크", "이름", "파일 시스템", "남은 공간", "전체"}, table)
	}
	if gpus := res.list("gpus"); len(gpus) > 0 {
		fmt.Fprintln(r.Stdout)
		var table [][]string
		for _, g := range gpus {
			memory := "-"
			if n, ok := g.num("memory_bytes"); ok && n > 0 {
				memory = humanSize(n)
			}
			table = append(table, []string{g.str("name"), dash(g.str("driver_version")), memory})
		}
		writeTable(r.Stdout, []string{"GPU", "드라이버", "전용 메모리"}, table)
	}
}

// uptimeText says s seconds as days, hours and minutes.
func uptimeText(s int64) string {
	d, h, m := s/86400, s%86400/3600, s%3600/60
	switch {
	case d > 0:
		return fmt.Sprintf("%d일 %d시간 %d분", d, h, m)
	case h > 0:
		return fmt.Sprintf("%d시간 %d분", h, m)
	}
	return fmt.Sprintf("%d분", m)
}

// localTime formats an RFC 3339 time in the Mac's time zone with layout,
// or returns s as it is when it is not one.
func localTime(s, layout string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return dash(s)
	}
	return t.Local().Format(layout)
}

var serviceStates = map[string]string{"running": "실행 중", "stopped": "중지됨", "paused": "일시 중지됨", "start_pending": "시작 중", "stop_pending": "중지 중", "unknown": "알 수 없음"}

var startTypes = map[string]string{"auto": "자동", "delayed_auto": "자동(지연된 시작)", "manual": "수동", "disabled": "사용 안 함", "unknown": "알 수 없음"}

// label returns the Korean label of v in labels, or v.
func label(labels map[string]string, v string) string {
	if l, ok := labels[v]; ok {
		return l
	}
	return dash(v)
}

func (r *run) diagServices(ctx context.Context, args []string) int {
	fs := newFlags("diag services")
	name := fs.String("name", "", "이름이나 표시 이름에 들어간 글자")
	save := fs.Bool("save", false, "결과를 산출물로 저장")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	params := map[string]any{}
	if *name != "" {
		params["name"] = *name
	}
	return r.diagCall(ctx, "diag.services", "deskhand diag services <호스트> [--name N] [--save] 형식으로 주세요", pos, *save, params, func(res obj) {
		services := res.list("services")
		if len(services) == 0 {
			fmt.Fprintln(r.Stdout, "일치하는 서비스가 없습니다")
			return
		}
		rows := make([][]string, 0, len(services))
		for _, s := range services {
			pid := "-"
			if n, ok := s.num("pid"); ok && n > 0 {
				pid = strconv.FormatInt(n, 10)
			}
			rows = append(rows, []string{s.str("name"), s.str("display_name"), label(serviceStates, s.str("state")), label(startTypes, s.str("start_type")), pid})
		}
		writeTable(r.Stdout, []string{"이름", "표시 이름", "상태", "시작 유형", "PID"}, rows)
	})
}

// sinceFlag reads a time flag: an RFC 3339 time, or a positive span before
// now such as 30m, 2h or 7d.
func sinceFlag(name, v string, now time.Time) (string, error) {
	if v == "" {
		return "", nil
	}
	if _, err := wire.ParseDiagTime(v); err == nil {
		return v, nil
	}
	var span time.Duration
	var err error
	if days, ok := strings.CutSuffix(v, "d"); ok {
		var n int
		n, err = strconv.Atoi(days)
		span = time.Duration(n) * 24 * time.Hour
	} else {
		span, err = time.ParseDuration(v)
	}
	if err != nil || span <= 0 {
		return "", usagef("--%s %q는 RFC 3339 시각(2026-10-20T09:00:00+09:00)이나 지금부터 거슬러 올라갈 기간(30m, 2h, 7d)이어야 합니다", name, v)
	}
	return wire.FormatDiagTime(now.Add(-span)), nil
}

var eventLevels = map[string]string{"critical": "위험", "error": "오류", "warning": "경고", "information": "정보", "verbose": "자세히"}

// eventMessageCols bounds the message column of diag events.
const eventMessageCols = 80

func (r *run) diagEvents(ctx context.Context, args []string) int {
	fs := newFlags("diag events")
	log := fs.String("log", "", "System(기본) 또는 Application")
	since := fs.String("since", "", "이 시각 이후(RFC 3339 또는 30m·2h·7d, 기본 24h)")
	until := fs.String("until", "", "이 시각까지(RFC 3339 또는 30m·2h·7d)")
	levels := fs.String("level", "", "수준(쉼표로 구분: critical,error,warning,information,verbose)")
	provider := fs.String("provider", "", "공급자 이름(정확히 일치)")
	maxN := fs.String("max", "", "최대 개수(기본 200, 최대 1000)")
	save := fs.Bool("save", false, "결과를 산출물로 저장")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	params := map[string]any{}
	now := time.Now()
	for _, f := range []struct{ name, value string }{{"since", *since}, {"until", *until}} {
		at, err := sinceFlag(f.name, f.value, now)
		if err != nil {
			return r.fail(err)
		}
		if at != "" {
			params[f.name] = at
		}
	}
	if *log != "" {
		params["log"] = *log
	}
	if *levels != "" {
		var list []string
		for _, l := range strings.Split(*levels, ",") {
			if l = strings.TrimSpace(l); l != "" {
				list = append(list, l)
			}
		}
		params["levels"] = list
	}
	if *provider != "" {
		params["provider"] = *provider
	}
	if *maxN != "" {
		n, err := strconv.Atoi(*maxN)
		if err != nil {
			return r.fail(usagef("--max %q는 1부터 %d까지의 정수여야 합니다", *maxN, wire.DiagEventsMaxLimit))
		}
		params["max"] = n
	}
	return r.diagCall(ctx, "diag.events", "deskhand diag events <호스트> [--log L] [--since T] [--until T] [--level L,…] [--provider P] [--max N] [--save] 형식으로 주세요",
		pos, *save, params, func(res obj) {
			events := res.list("events")
			if len(events) == 0 {
				fmt.Fprintln(r.Stdout, "조건에 맞는 이벤트가 없습니다")
				return
			}
			rows := make([][]string, 0, len(events))
			for _, e := range events {
				msg, _, _ := strings.Cut(e.str("message"), "\n")
				rows = append(rows, []string{localTime(e.str("time"), "2006-01-02 15:04:05"), label(eventLevels, e.str("level")), e.str("provider"), e.str("event_id"), cutColumns(msg, eventMessageCols)})
			}
			writeTable(r.Stdout, []string{"시각", "수준", "공급자", "ID", "메시지"}, rows)
			if res.boolean("truncated") {
				r.info("최근 %d개만 보여 줍니다. 더 보려면 --max를 늘리거나(최대 %d) --since·--until로 범위를 좁히세요", len(events), wire.DiagEventsMaxLimit)
			}
		})
}

// cutColumns cuts s to cols terminal columns, ending a cut text with "…".
func cutColumns(s string, cols int) string {
	if displayWidth(s) <= cols {
		return s
	}
	var b strings.Builder
	w := 0
	for _, c := range s {
		if w+runeWidth(c) > cols-1 {
			break
		}
		b.WriteRune(c)
		w += runeWidth(c)
	}
	return b.String() + "…"
}

var dumpKinds = map[string]string{"minidump": "미니 덤프", "full": "전체 덤프", "report": "오류 보고서"}

func (r *run) diagCrashes(ctx context.Context, args []string) int {
	fs := newFlags("diag crashes")
	since := fs.String("since", "", "이 시각 이후(RFC 3339 또는 30m·2h·7d, 기본 30d)")
	save := fs.Bool("save", false, "결과를 산출물로 저장")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	params := map[string]any{}
	at, err := sinceFlag("since", *since, time.Now())
	if err != nil {
		return r.fail(err)
	}
	if at != "" {
		params["since"] = at
	}
	return r.diagCall(ctx, "diag.crashes", "deskhand diag crashes <호스트> [--since T] [--save] 형식으로 주세요", pos, *save, params, func(res obj) {
		dumps := res.list("dumps")
		if len(dumps) == 0 {
			fmt.Fprintln(r.Stdout, "크래시 기록이 없습니다")
			return
		}
		rows := make([][]string, 0, len(dumps))
		for _, d := range dumps {
			size, _ := d.num("size_bytes")
			rows = append(rows, []string{localTime(d.str("time"), "2006-01-02 15:04"), d.str("app"), label(dumpKinds, d.str("kind")), humanSize(size), d.str("path")})
		}
		writeTable(r.Stdout, []string{"시각", "앱", "종류", "크기", "경로"}, rows)
		r.info("파일 받기: deskhand files pull %s:<경로> <로컬 폴더>", pos[0])
	})
}
