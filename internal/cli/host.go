package cli

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/client"
)

const installCallTimeout = 20 * time.Minute

func (r *run) host(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return r.fail(usagef("deskhand host add|list|remove|doctor|repair 중 하나를 고르세요"))
	}
	switch args[0] {
	case "add":
		return r.hostAdd(ctx, args[1:])
	case "list":
		return r.hostList(ctx, args[1:])
	case "remove":
		return r.hostRemove(ctx, args[1:])
	case "doctor":
		return r.hostDoctor(ctx, args[1:])
	case "repair":
		return r.hostRepair(ctx, args[1:])
	}
	return r.fail(usagef("알 수 없는 host 명령 %q입니다. add, list, remove, doctor, repair 중 하나를 고르세요", args[0]))
}

var stepNames = map[string]string{
	"probe": "PC 확인", "copy": "에이전트 복사", "init": "에이전트 초기화", "install": "서비스 설치", "verify": "연결 확인",
}

// printProgress prints host.install.progress events of host name on stderr
// until events closes or stop is closed and the buffered events are drained.
func (r *run) printProgress(events <-chan client.Event, name string, stop <-chan struct{}) {
	show := func(ev client.Event) {
		if n, _ := ev.Params["name"].(string); n != name {
			return
		}
		step, _ := ev.Params["step"].(string)
		status, _ := ev.Params["status"].(string)
		label := stepNames[step]
		if label == "" {
			label = step
		}
		detail, _ := ev.Params["detail"].(string)
		switch status {
		case "active":
			r.info("[진행] %s ...", label)
		case "done":
			r.info("[완료] %s", label)
		case "failed":
			msg := ""
			if e, ok := ev.Params["error"].(map[string]any); ok {
				msg, _ = e["message"].(string)
			}
			r.info("[실패] %s: %s", label, dash(msg))
		default:
			return
		}
		if detail != "" && status != "failed" {
			r.info("       %s", detail)
		}
	}
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			show(ev)
		case <-stop:
			for {
				select {
				case ev, ok := <-events:
					if !ok {
						return
					}
					show(ev)
				default:
					return
				}
			}
		}
	}
}

// install runs host.add or host.repair showing the install steps of name.
func (r *run) install(ctx context.Context, method, name string, params map[string]any, what string) int {
	c, err := r.dial(ctx)
	if err != nil {
		return r.fail(err)
	}
	defer c.Close()
	events, err := c.Subscribe(ctx, []string{"host.install.progress"})
	if err != nil {
		return r.fail(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); r.printProgress(events, name, stop) }()
	res, err := r.call(ctx, c, method, params, installCallTimeout)
	close(stop)
	wg.Wait()
	if err != nil {
		return r.fail(err)
	}
	code := r.settle(res, what)
	if code == exitOK {
		h := res.sub("host")
		r.say("%s 완료: %s (상태 %s)", what, name, dash(h.str("state")))
	}
	return code
}

func (r *run) hostAdd(ctx context.Context, args []string) int {
	fs := newFlags("host add")
	ssh := fs.String("ssh", "", "SSH 별칭")
	address := fs.String("address", "", "PC 주소")
	port := fs.Int("port", 47600, "에이전트 UDP 포트")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 || *ssh == "" {
		return r.fail(usagef("deskhand host add <이름> --ssh <별칭> [--address A] [--port 47600] 형식으로 주세요"))
	}
	params := map[string]any{"name": pos[0], "ssh": *ssh, "port": *port}
	if *address != "" {
		params["address"] = *address
	}
	return r.install(ctx, "host.add", pos[0], params, "PC 등록")
}

func (r *run) hostRepair(ctx context.Context, args []string) int {
	pos, _, err := parseArgs(newFlags("host repair"), args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 {
		return r.fail(usagef("deskhand host repair <이름> 형식으로 주세요"))
	}
	return r.install(ctx, "host.repair", pos[0], map[string]any{"host_id": pos[0]}, "PC 복구")
}

func (r *run) hostList(ctx context.Context, args []string) int {
	pos, _, err := parseArgs(newFlags("host list"), args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 0 {
		return r.fail(usagef("deskhand host list 는 인수를 받지 않습니다"))
	}
	res, err := r.simple(ctx, "host.list", nil)
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	hosts := res.list("hosts")
	if len(hosts) == 0 {
		fmt.Fprintln(r.Stdout, "등록된 PC가 없습니다. deskhand host add <이름> --ssh <별칭> 으로 등록하세요")
		return exitOK
	}
	rows := make([][]string, 0, len(hosts))
	for _, h := range hosts {
		rows = append(rows, []string{h.str("name"), h.str("os") + "/" + h.str("arch"), h.str("address"), h.str("state"),
			h.str("owner"), dash(h.str("last_seen"))})
	}
	writeTable(r.Stdout, []string{"이름", "OS", "주소", "상태", "제어권", "마지막 확인"}, rows)
	return exitOK
}

func (r *run) hostRemove(ctx context.Context, args []string) int {
	fs := newFlags("host remove")
	uninstall := fs.Bool("uninstall", false, "PC에서 에이전트도 제거")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 {
		return r.fail(usagef("deskhand host remove <이름> [--uninstall] 형식으로 주세요"))
	}
	res, err := r.simple(ctx, "host.remove", map[string]any{"host_id": pos[0], "uninstall": *uninstall})
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	if res.boolean("uninstalled") {
		r.say("PC %s를 제거하고 에이전트도 삭제했습니다", pos[0])
	} else {
		r.say("PC %s를 제거했습니다. PC의 에이전트는 그대로입니다", pos[0])
	}
	return exitOK
}

func (r *run) hostDoctor(ctx context.Context, args []string) int {
	pos, _, err := parseArgs(newFlags("host doctor"), args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 {
		return r.fail(usagef("deskhand host doctor <이름> 형식으로 주세요"))
	}
	res, err := r.simple(ctx, "host.doctor", map[string]any{"host_id": pos[0]})
	if err != nil {
		return r.fail(err)
	}
	checks := res.list("checks")
	failed := false
	for _, ch := range checks {
		if ch.str("status") == "fail" {
			failed = true
		}
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
	} else {
		rows := make([][]string, 0, len(checks))
		for _, ch := range checks {
			rows = append(rows, []string{ch.str("id"), checkKorean(ch.str("status")), dash(ch.str("detail")), dash(ch.str("fix"))})
		}
		writeTable(r.Stdout, []string{"항목", "결과", "상세", "조치"}, rows)
	}
	if failed {
		return exitFailure
	}
	return exitOK
}

func checkKorean(s string) string {
	switch s {
	case "pass":
		return "통과"
	case "fail":
		return "실패"
	case "warn":
		return "주의"
	case "skip":
		return "건너뜀"
	}
	return dash(s)
}
