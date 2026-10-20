// Package cli implements the deskhand command line over the daemon's local
// API (docs/local-api.md).
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/client"
	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// callTimeout bounds a call that answers at once.
const callTimeout = 60 * time.Second

// App is one command line process.
type App struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Version is the build version shown by `deskhand version`.
	Version string
	// Interactive reports whether a person can answer a prompt on stdin.
	Interactive func() bool
	// PollInterval is the wait between two polls of `jobs watch` and of a
	// followed transfer.
	PollInterval time.Duration
	// Progress reports whether stderr is a terminal that can show a
	// progress bar.
	Progress func() bool
	// Term is the terminal deskhand shell connects to the remote shell.
	Term Terminal
}

// New returns an App on the process's standard streams.
func New(version string) *App {
	return &App{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Version: version,
		Interactive: stdinIsTerminal, PollInterval: 500 * time.Millisecond,
		Progress: func() bool { return fileIsTerminal(os.Stderr) }, Term: stdinTerminal{}}
}

// run is the state of one invocation: the App and the global flags.
type run struct {
	*App
	asJSON bool
	quiet  bool
	// reqID and jobID are the ids of the operation in progress, once known.
	reqID, jobID string
	// unknownOnErr makes a failed call exit 4 (outcome unknown) unless the
	// request itself was invalid.
	unknownOnErr bool
	// steps are next-step sentences of this invocation that replace the
	// general ones of nextSteps.
	steps map[string]string
}

const usageText = `사용법: deskhand [--json] [--quiet] <명령> ...

명령:
  host add|list|remove|doctor|repair   PC 등록과 점검
  exec <호스트> [옵션] -- <명령...>      원격 명령 실행
  jobs list|watch|cancel               작업 조회
  files ls|stat|cat|mkdir|mv|rm|cp|trash|hash
                                       원격 파일 (<호스트>:<경로>)
  files push|pull|move                 Mac과 PC 사이 파일 전송
  transfer list|get|watch|pause|resume|cancel
                                       전송 조회와 제어
  shell <호스트> [--shell S]             원격 대화형 셸 (Ctrl-]로 분리, --attach <job>으로 다시 연결)
  tunnel <호스트> <포트> [--local P]     PC의 루프백 포트를 이 Mac의 127.0.0.1로 연결 (Ctrl-C로 닫음)
  tunnel list|close                    열린 터널 조회와 닫기
  proc ls|kill                         원격 프로세스
  diag system|services|events|crashes  원격 PC 진단 (--save로 결과를 산출물로 저장)
  daemon run|start|stop|status         데몬 관리
  mcp                                  MCP 서버(표준 입출력, AI 에이전트용)
  version                              버전 보기

종료 코드: 0 성공, 1 실패, 2 사용법 오류, 3 사람 확인 필요, 4 결과 불명, 5 오프라인·차단
exec와 shell은 원격 명령·셸의 종료 코드를 그대로 돌려줍니다.
`

// Run executes args (without the program name) and returns the exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	r := &run{App: a}
	args = r.globals(args)
	if len(args) == 0 {
		fmt.Fprint(a.Stderr, usageText)
		return exitUsage
	}
	var code int
	switch args[0] {
	case "host":
		code = r.host(ctx, args[1:])
	case "exec":
		code = r.exec(ctx, args[1:])
	case "jobs":
		code = r.jobs(ctx, args[1:])
	case "files":
		code = r.files(ctx, args[1:])
	case "transfer":
		code = r.transfer(ctx, args[1:])
	case "proc":
		code = r.proc(ctx, args[1:])
	case "diag":
		code = r.diag(ctx, args[1:])
	case "tunnel":
		code = r.tunnel(ctx, args[1:])
	case "shell":
		code = r.shell(ctx, args[1:])
	case "daemon":
		code = r.daemon(ctx, args[1:])
	case "mcp":
		code = r.mcp(ctx, args[1:])
	case "version":
		if r.asJSON {
			writeJSON(a.Stdout, obj{"version": a.Version})
		} else {
			fmt.Fprintln(a.Stdout, "deskhand", a.Version)
		}
		code = exitOK
	case "help", "-h", "--help":
		fmt.Fprint(a.Stdout, usageText)
		code = exitOK
	default:
		code = r.fail(usagef("알 수 없는 명령 %q입니다. deskhand help로 명령 목록을 확인하세요", args[0]))
	}
	return code
}

// globals removes --json, --quiet and -q ahead of a lone "--".
func (r *run) globals(args []string) []string {
	out := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		switch a {
		case "--json", "-json":
			r.asJSON = true
		case "--quiet", "-quiet", "-q":
			r.quiet = true
		default:
			out = append(out, a)
		}
	}
	return out
}

// info prints a progress or diagnostic line on stderr unless --quiet.
func (r *run) info(format string, a ...any) {
	if !r.quiet {
		fmt.Fprintf(r.Stderr, format+"\n", a...)
	}
}

// say prints a result line on stdout unless --quiet or --json.
func (r *run) say(format string, a ...any) {
	if !r.quiet && !r.asJSON {
		fmt.Fprintf(r.Stdout, format+"\n", a...)
	}
}

// dial connects to the daemon, starting it when it does not answer.
func (r *run) dial(ctx context.Context) (*client.Client, error) {
	return client.DialOptions(ctx, "cli", "deskhand", client.Options{Version: r.Version})
}

// call sends method and returns its result object.
func (r *run) call(ctx context.Context, c *client.Client, method string, params any, timeout time.Duration) (obj, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var raw json.RawMessage
	if err := c.Call(cctx, method, params, &raw); err != nil {
		return nil, err
	}
	return decodeObj(raw)
}

// simple dials, calls one method and returns its result.
func (r *run) simple(ctx context.Context, method string, params any) (obj, error) {
	c, err := r.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return r.call(ctx, c, method, params, callTimeout)
}

// nextSteps are the sentences for the daemon's next_action values.
var nextSteps = map[string]string{
	"check_state":    "deskhand jobs list 로 상태를 확인하세요",
	"host_repair":    "deskhand host repair <이름> 을 실행하세요",
	"host_add_force": "deskhand host add <이름> --ssh <별칭> --force 를 실행하세요",
	"host_connect":   "PC가 켜져 있는지 확인하고 deskhand host doctor <이름> 으로 점검하세요",
	"host_doctor":    "deskhand host doctor <이름> 으로 점검하세요",
	"retry":          "잠시 뒤 같은 명령을 다시 실행하세요",
	"fix_params":     "입력을 고쳐 다시 실행하세요",
	"human_release":  "사람이 제어권을 놓은 뒤 다시 실행하세요",
}

// nextStep returns the sentence of the next action: the one of r.steps, or
// the general one.
func (r *run) nextStep(action string) string {
	if s, ok := r.steps[action]; ok {
		return s
	}
	return nextStep(action)
}

func nextStep(action string) string {
	if s, ok := nextSteps[action]; ok {
		return s
	}
	if action != "" {
		return action
	}
	return ""
}

// fail reports err and returns the exit code for it.
func (r *run) fail(err error) int { return r.failCall(err, false, "") }

// failMut reports the error of a call that changes the remote PC: a lost
// connection or an unanswered call leaves the outcome unknown (exit 4).
func (r *run) failMut(err error, host string) int { return r.failCall(err, true, host) }

func (r *run) failCall(err error, mutating bool, host string) int {
	var ue *usageError
	if errors.As(err, &ue) {
		fmt.Fprintln(r.Stderr, "사용법 오류: "+ue.msg)
		return exitUsage
	}
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		fmt.Fprintln(r.Stderr, "오류: "+err.Error())
		return exitFailure
	}
	code := exitFailure
	switch ae.Code {
	case wire.CodeOffline, wire.CodeAuthRequired:
		code = exitOffline
	case wire.CodeExecutionOutcomeUnknown, wire.CodeTimeout:
		code = exitUnknown
	}
	if r.unknownOnErr && r.jobID != "" && ae.Code != wire.CodeInvalidArgument {
		code = exitUnknown
	}
	if mutating && client.IsConnectionLost(err) {
		code = exitUnknown
	}
	fmt.Fprintln(r.Stderr, "오류: "+ae.Message)
	if code == exitUnknown {
		if r.jobID != "" {
			fmt.Fprintf(r.Stderr, "작업 %s의 결과를 알 수 없습니다. deskhand jobs get %s 로 상태를 확인한 뒤 필요하면 다시 실행하세요\n", r.jobID, r.jobID)
		} else {
			list := "deskhand jobs list"
			if host != "" {
				list += " --host " + host
			}
			fmt.Fprintf(r.Stderr, "결과를 알 수 없습니다. %s 로 최근 작업을 확인하고 deskhand files stat 또는 deskhand proc ls 로 PC 상태를 직접 살핀 뒤 필요하면 다시 실행하세요\n", list)
		}
	} else if n := r.nextStep(ae.NextAction); n != "" {
		fmt.Fprintln(r.Stderr, "다음 단계: "+n)
	}
	if r.asJSON {
		o := obj{"status": model.StatusFailed, "error": obj{"code": ae.Code, "message": ae.Message,
			"retryable": ae.Retryable, "next_action": ae.NextAction}}
		if r.reqID != "" {
			o["request_id"] = r.reqID
		}
		if r.jobID != "" {
			o["job_id"] = r.jobID
		}
		if code == exitUnknown {
			o["status"] = model.StatusUnknown
		}
		writeJSON(r.Stdout, o)
	}
	return code
}

// settle prints a result envelope in --json mode and returns the exit code
// of its status; human messages go to stderr.
func (r *run) settle(res obj, what string) int {
	if r.asJSON {
		writeJSON(r.Stdout, res)
	}
	return r.statusExit(res, what, 0)
}

// Statuses a caller accepts besides succeeded.
const (
	allowRunning = 1 << iota
	allowCancelled
)

// statusExit returns the exit code of an envelope status. A status that is
// not final for the call (running unless allowed) or not known is an
// unknown outcome.
func (r *run) statusExit(res obj, what string, allow int) int {
	e := res.sub("error")
	next := r.nextStep(firstNonEmpty(res.str("next_action"), e.str("next_action")))
	hint := func() {
		if next != "" {
			fmt.Fprintln(r.Stderr, "다음 단계: "+next)
		}
	}
	switch res.str("status") {
	case model.StatusWaitingForUser:
		fmt.Fprintf(r.Stderr, "%s: 사람의 확인이 필요합니다. %s\n", what, dash(e.str("message")))
		fmt.Fprintln(r.Stderr, "다음 단계: PC 화면에서 확인을 마친 뒤 같은 명령을 다시 실행하세요")
		return exitNeedsUser
	case model.StatusUnknown:
		return r.unknownExit(res, what)
	case model.StatusFailed:
		if e == nil {
			fmt.Fprintf(r.Stderr, "%s: 실패했습니다\n", what)
			return exitFailure
		}
		switch e.str("code") {
		case wire.CodeExecutionOutcomeUnknown:
			return r.unknownExit(res, what)
		case wire.CodeOffline, wire.CodeAuthRequired:
			fmt.Fprintf(r.Stderr, "%s: %s\n", what, e.str("message"))
			hint()
			return exitOffline
		}
		fmt.Fprintf(r.Stderr, "%s: %s\n", what, e.str("message"))
		hint()
		return exitFailure
	case model.StatusCancelled:
		if allow&allowCancelled != 0 {
			return exitOK
		}
		fmt.Fprintf(r.Stderr, "%s: 작업이 취소됐습니다. 필요하면 같은 명령을 다시 실행하세요\n", what)
		return exitFailure
	case model.StatusRunning:
		if allow&allowRunning != 0 {
			return exitOK
		}
	case model.StatusSucceeded:
		return exitOK
	}
	return r.unknownExitWhy(res, what, fmt.Sprintf("예상하지 못한 상태(%s)입니다", dash(res.str("status"))))
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// unknownExit explains an unknown outcome with its ids and the next step.
func (r *run) unknownExit(res obj, what string) int {
	return r.unknownExitWhy(res, what, "결과를 알 수 없습니다")
}

func (r *run) unknownExitWhy(res obj, what, why string) int {
	ids := "요청 " + dash(res.str("request_id"))
	step := "같은 명령을 다시 실행하기 전에 deskhand files stat 등으로 대상 상태를 먼저 확인하세요"
	if j := res.str("job_id"); j != "" {
		ids += ", 작업 " + j
		step = fmt.Sprintf("deskhand jobs get %s 로 상태를 확인한 뒤 필요하면 다시 실행하세요", j)
	}
	fmt.Fprintf(r.Stderr, "%s: %s(%s). %s\n", what, why, ids, step)
	return exitUnknown
}

// newFlags returns a flag set that reports its mistakes through errors.
func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parseArgs parses flags anywhere among args and returns the positional
// arguments and the arguments after a lone "--".
func parseArgs(fs *flag.FlagSet, args []string) (pos, after []string, err error) {
	for i, a := range args {
		if a == "--" {
			args, after = args[:i], args[i+1:]
			break
		}
	}
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, nil, usagef("deskhand %s 옵션: deskhand help로 사용법을 확인하세요", fs.Name())
			}
			return nil, nil, usagef("%v. deskhand help로 사용법을 확인하세요", err)
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
	return pos, after, nil
}

// remotePath is a <host>:<path> argument.
type remotePath struct{ Host, Path string }

// parseRemote splits <host>:<path> at the first colon; a single letter
// before that colon is a drive, not a host.
func parseRemote(s string) (remotePath, error) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return remotePath{}, usagef("%q에 호스트가 없습니다. <호스트>:<경로> 형식으로 주세요(예: windows-dev:C:\\x)", s)
	}
	host, p := s[:i], s[i+1:]
	if len(host) == 1 && (host[0]|0x20) >= 'a' && (host[0]|0x20) <= 'z' {
		return remotePath{}, usagef("%q는 드라이브 경로입니다. 앞에 호스트 이름을 붙여 <호스트>:%s 형식으로 주세요", s, s)
	}
	if host == "" {
		return remotePath{}, usagef("%q의 호스트 이름이 비었습니다. <호스트>:<경로> 형식으로 주세요", s)
	}
	if strings.ContainsAny(host, "/\\ \t\n") {
		return remotePath{}, usagef("%q의 호스트 이름에 / \\ 나 공백이 있습니다. 경로 앞에 호스트 이름을 붙여 <호스트>:<경로> 형식으로 주세요", s)
	}
	if len(p) >= 2 && (p[0] == '\'' || p[0] == '"') && p[len(p)-1] == p[0] {
		p = p[1 : len(p)-1]
	}
	if p == "" {
		return remotePath{}, usagef("%q의 경로가 비었습니다. <호스트>:<경로> 형식으로 주세요", s)
	}
	return remotePath{Host: host, Path: p}, nil
}
