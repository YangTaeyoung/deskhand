package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
)

const (
	defaultExecTimeout = 120 * time.Second
	execCallMargin     = time.Minute
)

func (r *run) exec(ctx context.Context, args []string) int {
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
		return r.fail(usagef("deskhand exec <호스트> [옵션] -- <명령...> 형식으로 주세요"))
	}
	if *timeout < 0 {
		return r.fail(usagef("--timeout은 0 이상이어야 합니다"))
	}
	params := map[string]any{"host_id": pos[0]}
	switch *shell {
	case "", "none":
		params["argv"] = argv
		if *shell == "none" {
			params["shell"] = "none"
		}
	case "powershell", "cmd":
		params["cmd"] = strings.Join(argv, " ")
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
	if *detach {
		res, err := r.call(ctx, c, "exec.start", params, callTimeout)
		if err != nil {
			return r.fail(err)
		}
		code := r.settle(res, "명령 시작")
		if code == exitOK {
			if id := res.str("job_id"); id != "" {
				if r.asJSON {
					return code
				}
				fmt.Fprintln(r.Stdout, id)
				r.info("작업을 시작했습니다. deskhand jobs watch %s 로 출력을 따라가세요", id)
			}
		}
		return code
	}
	if *timeout > 0 {
		params["timeout_ms"] = timeout.Milliseconds()
	}
	res, err := r.call(ctx, c, "exec.run", params, *timeout+execCallMargin)
	if err != nil {
		return r.fail(err)
	}
	return r.finishExec(res)
}

// finishExec prints the outputs of an exec.run result and returns the
// remote command's exit code, or the exit code of a failed envelope.
func (r *run) finishExec(res obj) int {
	if r.asJSON {
		writeJSON(r.Stdout, res)
	}
	status := res.str("status")
	code, hasCode := res.num("code")
	e := res.sub("error")
	if status == model.StatusUnknown || (status == model.StatusFailed && e != nil && !hasCode) {
		return r.statusExit(res, "명령 실행")
	}
	if !r.asJSON {
		r.writeOutput(res, "stdout", "stdout.txt", r.Stdout)
	}
	if !r.asJSON {
		r.writeOutput(res, "stderr", "stderr.txt", r.Stderr)
	}
	if e != nil {
		fmt.Fprintf(r.Stderr, "명령 실행: %s\n", e.str("message"))
		if code == 0 {
			return exitFailure
		}
	}
	return remoteExitCode(code)
}

// writeOutput prints the stream's text: the artifact file when the result
// lists one for it, else the inline field.
func (r *run) writeOutput(res obj, field, artifact string, w io.Writer) {
	for _, a := range res.list("artifacts") {
		if a.str("name") != artifact || a.str("path") == "" {
			continue
		}
		if b, err := os.ReadFile(a.str("path")); err == nil {
			_, _ = w.Write(b)
			return
		}
	}
	_, _ = io.WriteString(w, res.str(field))
}

// remoteExitCode maps a remote exit code to a process exit code.
func remoteExitCode(code int64) int {
	if code == 0 {
		return exitOK
	}
	if c := int(code & 0xff); c != 0 {
		return c
	}
	return exitFailure
}
