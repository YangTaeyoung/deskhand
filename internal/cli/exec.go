package cli

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	defaultExecTimeout = 120 * time.Second
	execCallMargin     = time.Minute
)

const execHelp = `사용법: deskhand exec <호스트> [옵션] -- <명령...>

옵션:
  --cwd D                  원격 작업 폴더
  --shell powershell|cmd|none
                           none(기본): -- 뒤의 인수를 그대로 argv로 실행합니다.
                           powershell|cmd: 명령 전체를 따옴표 하나로 묶은 문자열 하나를 셸로 실행합니다.
  --timeout 120s           명령 제한 시간. 넘으면 작업을 취소하고 종료 코드 124로 끝납니다.
                           0이면 제한 없이 끝날 때까지 기다립니다. --detach와는 함께 쓸 수 없습니다.
                           출력은 실시간으로 보여 주며, Ctrl-C는 종료 코드 4와 job id를 남깁니다.
  --detach                 시작만 하고 job id를 출력합니다.

종료 코드: 원격 명령의 종료 코드 1-255는 그대로 돌려줍니다. 범위 밖의 코드(0 아님)는 1입니다.
시간 초과는 124입니다. deskhand 자체 오류는 2(사용법), 3(사람 확인), 4(결과 불명), 5(오프라인)이고
원격 코드와 겹칠 수 있으므로 스크립트는 --json의 status와 code로 구분하세요.
--json 출력은 시작 봉투에 status, code, stdout, stderr를 더한 결과 객체 한 줄입니다.
`

// exitTimeout is the exit code of a command that hit its time limit.
const exitTimeout = 124

func (r *run) exec(ctx context.Context, args []string) int {
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "-h" || a == "--help" || a == "-help" {
			fmt.Fprint(r.Stdout, execHelp)
			return exitOK
		}
	}
	fs := newFlags("exec")
	cwd := fs.String("cwd", "", "작업 폴더")
	shell := fs.String("shell", "", "powershell|cmd|none")
	timeout := fs.Duration("timeout", defaultExecTimeout, "명령 제한 시간")
	detach := fs.Bool("detach", false, "시작만 하고 job id를 출력")
	pos, argv, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	if len(pos) != 1 || len(argv) == 0 {
		return r.fail(usagef("deskhand exec <호스트> [옵션] -- <명령...> 형식으로 주세요(deskhand exec --help)"))
	}
	if *timeout < 0 {
		return r.fail(usagef("--timeout은 0 이상이어야 합니다"))
	}
	timeoutSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "timeout" {
			timeoutSet = true
		}
	})
	if *detach && timeoutSet {
		return r.fail(usagef("--detach에는 --timeout을 쓸 수 없습니다. 오래 걸리는 작업은 deskhand jobs cancel <job-id>로 끝내세요"))
	}
	host := pos[0]
	params := map[string]any{"host_id": host}
	switch *shell {
	case "", "none":
		params["argv"] = argv
		if *shell == "none" {
			params["shell"] = "none"
		}
	case "powershell", "cmd":
		if len(argv) != 1 {
			return r.fail(usagef("--shell %s에는 명령 전체를 따옴표 하나로 묶어 주세요(예: -- \"Get-Date -Format o\")", *shell))
		}
		params["cmd"] = argv[0]
		params["shell"] = *shell
	default:
		return r.fail(usagef("--shell은 powershell, cmd, none 중 하나여야 합니다"))
	}
	if *cwd != "" {
		params["cwd"] = *cwd
	}
	c, err := r.dial(ctx)
	if err != nil {
		return r.fail(err)
	}
	defer c.Close()
	if !*detach && *timeout > 0 {
		params["timeout_ms"] = timeout.Milliseconds()
	}
	start, err := r.call(ctx, c, "exec.start", params, callTimeout)
	if err != nil {
		return r.failMut(err, host)
	}
	r.reqID, r.jobID = start.str("request_id"), start.str("job_id")
	if *detach {
		if r.asJSON {
			writeJSON(r.Stdout, start)
		}
		code := r.statusExit(start, "명령 시작", allowRunning)
		if code == exitOK && r.jobID != "" && !r.asJSON {
			fmt.Fprintln(r.Stdout, r.jobID)
			r.info("작업을 시작했습니다. deskhand jobs watch %s 로 출력을 따라가세요", r.jobID)
		}
		return code
	}
	if code := r.statusExit(start, "명령 시작", allowRunning); code != exitOK {
		if r.asJSON {
			writeJSON(r.Stdout, start)
		}
		return code
	}
	if r.jobID == "" {
		return r.unknownExitWhy(start, "명령 시작", "작업 id를 받지 못했습니다")
	}
	opts := followOpts{stdout: r.Stdout, stderr: r.Stderr, timeout: *timeout}
	var outBuf, errBuf bytes.Buffer
	if r.asJSON {
		opts.stdout, opts.stderr = &outBuf, &errBuf
	}
	f := r.follow(ctx, c, r.jobID, opts)
	if r.asJSON && !f.printed {
		r.writeExecJSON(start, f, outBuf.String(), errBuf.String())
	}
	return f.exit
}

// writeExecJSON prints the start envelope completed with the end of the
// command: its status, exit code and collected output.
func (r *run) writeExecJSON(start obj, f followResult, stdout, stderr string) {
	res := start
	if f.status != "" {
		res["status"] = f.status
	}
	if f.hasCode {
		res["code"] = f.code
	}
	res["stdout"], res["stderr"], res["timed_out"] = stdout, stderr, f.timedOut
	if f.timedOut {
		res["status"] = model.StatusFailed
		res["error"] = obj{"code": wire.CodeTimeout, "message": "명령이 제한 시간 안에 끝나지 않아 종료했습니다"}
	}
	switch f.exit {
	case exitUnknown:
		res["status"] = model.StatusUnknown
		res["next_action"] = "check_state"
	case exitNeedsUser:
		res["status"] = model.StatusWaitingForUser
	}
	writeJSON(r.Stdout, res)
}

// remoteExitCode maps a remote exit code to a process exit code: 0 stays
// 0, 1-255 pass through, any other value is 1.
func remoteExitCode(code int64) int {
	switch {
	case code == 0:
		return exitOK
	case code >= 1 && code <= 255:
		return int(code)
	}
	return exitFailure
}
