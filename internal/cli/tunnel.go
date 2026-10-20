package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const tunnelUsage = "deskhand tunnel <호스트> <원격포트> [--local 포트] | deskhand tunnel list [호스트] | deskhand tunnel close <로컬포트|터널ID> 형식으로 주세요"

func (r *run) tunnel(ctx context.Context, args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "list":
			return r.tunnelList(ctx, args[1:])
		case "close":
			return r.tunnelClose(ctx, args[1:])
		}
	}
	return r.tunnelOpen(ctx, args)
}

// parsePort parses a TCP port number, 1 to 65535.
func parsePort(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 1 && n <= 65535
}

// tunnelOpen opens a tunnel, prints its local address and keeps running
// until it is interrupted or the daemon no longer has the tunnel; it then
// closes the tunnel.
func (r *run) tunnelOpen(ctx context.Context, args []string) int {
	fs := newFlags("tunnel")
	local := fs.Int("local", 0, "이 Mac의 포트(기본: 빈 포트)")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 2 {
		return r.fail(usagef("%s", tunnelUsage))
	}
	remote, valid := parsePort(pos[1])
	if !valid {
		return r.fail(usagef("원격 포트 %q는 1~65535 사이의 숫자여야 합니다", pos[1]))
	}
	if *local != 0 {
		if _, valid := parsePort(strconv.Itoa(*local)); !valid {
			return r.fail(usagef("--local %d는 1~65535 사이여야 합니다", *local))
		}
	}
	c, err := r.dial(ctx)
	if err != nil {
		return r.fail(err)
	}
	defer c.Close()
	params := map[string]any{"host_id": pos[0], "remote_port": remote}
	if *local != 0 {
		params["local_port"] = *local
	}
	res, err := r.call(ctx, c, "tunnel.open", params, callTimeout)
	if err != nil {
		return r.fail(err)
	}
	id := res.str("tunnel_id")
	if r.asJSON {
		writeJSON(r.Stdout, res)
	} else {
		r.say("터널을 열었습니다: %s -> %s의 127.0.0.1:%d (Ctrl-C로 닫습니다)", res.str("local_address"), pos[0], remote)
	}
	wait := r.PollInterval
	if wait < time.Millisecond {
		wait = time.Millisecond
	}
	code := exitOK
	for going := true; going; {
		select {
		case <-ctx.Done():
			going = false
		case <-time.After(wait):
			list, err := r.call(ctx, c, "tunnel.list", map[string]any{}, callTimeout)
			if ctx.Err() != nil {
				going = false
				break
			}
			if err != nil {
				code = r.fail(fmt.Errorf("데몬과 연결이 끊겨 터널 상태를 알 수 없습니다. deskhand daemon status로 확인하세요: %w", err))
				going = false
				break
			}
			if !hasTunnel(list.list("tunnels"), id) {
				code = r.fail(fmt.Errorf("터널 %s가 닫혔습니다(PC를 제거했거나 데몬이 다시 시작됐습니다). 같은 명령으로 다시 여세요", id))
				going = false
			}
		}
	}
	if code != exitOK {
		return code
	}
	cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := r.call(cctx, c, "tunnel.close", map[string]any{"tunnel_id": id}, 10*time.Second); err != nil {
		return r.fail(err)
	}
	r.info("터널을 닫았습니다: %s", res.str("local_address"))
	return exitOK
}

func hasTunnel(list []obj, id string) bool {
	for _, t := range list {
		if t.str("tunnel_id") == id {
			return true
		}
	}
	return false
}

func (r *run) tunnelList(ctx context.Context, args []string) int {
	fs := newFlags("tunnel list")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) > 1 {
		return r.fail(usagef("deskhand tunnel list [호스트] 형식으로 주세요"))
	}
	params := map[string]any{}
	if len(pos) == 1 {
		params["host_id"] = pos[0]
	}
	res, err := r.simple(ctx, "tunnel.list", params)
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	tunnels := res.list("tunnels")
	if len(tunnels) == 0 {
		fmt.Fprintln(r.Stdout, "열려 있는 터널이 없습니다")
		return exitOK
	}
	rows := make([][]string, 0, len(tunnels))
	for _, t := range tunnels {
		remote, _ := t.num("remote_port")
		conns, _ := t.num("connections")
		total, _ := t.num("total_connections")
		failed, _ := t.num("failed_connections")
		up, _ := t.num("bytes_to_remote")
		down, _ := t.num("bytes_from_remote")
		rows = append(rows, []string{t.str("tunnel_id"), t.str("host_id"), t.str("local_address"), strconv.FormatInt(remote, 10), t.str("owner"),
			fmt.Sprintf("%d (누적 %d, 실패 %d)", conns, total, failed), humanSize(up), humanSize(down), dash(t.str("last_error"))})
	}
	writeTable(r.Stdout, []string{"ID", "PC", "이 Mac", "PC 포트", "소유", "연결", "보냄", "받음", "마지막 오류"}, rows)
	return exitOK
}

func (r *run) tunnelClose(ctx context.Context, args []string) int {
	fs := newFlags("tunnel close")
	pos, _, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 {
		return r.fail(usagef("deskhand tunnel close <로컬포트|터널ID> 형식으로 주세요"))
	}
	params := map[string]any{}
	if n, valid := parsePort(pos[0]); valid {
		params["local_port"] = n
	} else if strings.HasPrefix(pos[0], "tn-") {
		params["tunnel_id"] = pos[0]
	} else {
		return r.fail(usagef("%q는 로컬 포트(1~65535)도 터널 ID(tn-…)도 아닙니다. deskhand tunnel list로 확인하세요", pos[0]))
	}
	res, err := r.simple(ctx, "tunnel.close", params)
	if err != nil {
		return r.fail(err)
	}
	if r.asJSON {
		writeJSON(r.Stdout, res)
		return exitOK
	}
	r.say("터널 %s를 닫았습니다", res.str("tunnel_id"))
	return exitOK
}
