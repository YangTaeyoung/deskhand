package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/client"
	"github.com/YangTaeyoung/deskhand/internal/daemon"
	"github.com/YangTaeyoung/deskhand/internal/daemon/paths"
)

func (r *run) daemon(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return r.fail(usagef("deskhand daemon run|start|stop|status 중 하나를 고르세요"))
	}
	extra, _, err := parseArgs(newFlags("daemon "+args[0]), args[1:])
	if err != nil {
		return r.fail(err)
	}
	if len(extra) > 0 {
		return r.fail(usagef("deskhand daemon %s 는 인수를 받지 않습니다", args[0]))
	}
	switch args[0] {
	case "run":
		return r.daemonRun(ctx)
	case "start":
		return r.daemonStart(ctx)
	case "stop":
		return r.daemonStop(ctx)
	case "status":
		return r.daemonStatus(ctx)
	}
	return r.fail(usagef("알 수 없는 daemon 명령 %q입니다. run, start, stop, status 중 하나를 고르세요", args[0]))
}

// daemonRun runs the daemon in this process until ctx ends.
func (r *run) daemonRun(ctx context.Context) int {
	if err := daemon.Run(ctx, daemon.Options{Version: r.Version}); err != nil {
		if errors.Is(err, daemon.ErrAlreadyRunning) {
			fmt.Fprintln(r.Stderr, err.Error())
			return exitFailure
		}
		return r.fail(err)
	}
	return exitOK
}

func (r *run) daemonStart(ctx context.Context) int {
	if client.Running(ctx, "") {
		r.say("데몬이 이미 실행 중입니다")
		return r.daemonStatus(ctx)
	}
	if err := client.Start(ctx); err != nil {
		return r.fail(err)
	}
	r.info("데몬을 시작했습니다")
	return r.daemonStatus(ctx)
}

func (r *run) daemonStop(ctx context.Context) int {
	if !client.Running(ctx, "") {
		r.say("데몬이 실행 중이 아닙니다")
		if r.asJSON {
			writeJSON(r.Stdout, obj{"running": false, "socket": paths.Socket()})
		}
		return exitOK
	}
	if err := client.Stop(ctx); err != nil {
		return r.fail(err)
	}
	r.say("데몬을 멈췄습니다")
	if r.asJSON {
		writeJSON(r.Stdout, obj{"running": false, "socket": paths.Socket()})
	}
	return exitOK
}

func (r *run) daemonStatus(ctx context.Context) int {
	st := obj{"running": false, "socket": paths.Socket()}
	if client.Running(ctx, "") {
		st["running"] = true
		if b, err := os.ReadFile(paths.Lock()); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				st["pid"] = pid
			}
		}
		dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if c, err := client.DialOptions(dctx, "cli", "deskhand", client.Options{Version: r.Version,
			Start: func(context.Context) error { return errors.New("데몬이 실행 중이 아닙니다") }}); err == nil {
			st["version"] = c.DaemonVersion
			c.Close()
		}
	}
	if r.asJSON {
		writeJSON(r.Stdout, st)
	} else if st["running"] == true {
		fmt.Fprintf(r.Stdout, "데몬 실행 중 (PID %v, 버전 %v)\n소켓: %s\n", st["pid"], st["version"], paths.Socket())
	} else {
		fmt.Fprintf(r.Stdout, "데몬이 실행 중이 아닙니다. deskhand daemon start 로 시작하세요\n")
	}
	if st["running"] != true {
		return exitFailure
	}
	return exitOK
}
