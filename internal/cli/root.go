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
	// PollInterval is the wait between two polls of `jobs watch`.
	PollInterval time.Duration
}

// New returns an App on the process's standard streams.
func New(version string) *App {
	return &App{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Version: version,
		Interactive: stdinIsTerminal, PollInterval: 500 * time.Millisecond}
}

func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// run is the state of one invocation: the App and the global flags.
type run struct {
	*App
	asJSON bool
	quiet  bool
}

const usageText = `사용법: deskhand [--json] [--quiet] <명령> ...

명령:
  host add|list|remove|doctor|repair   PC 등록과 점검
  exec <호스트> [옵션] -- <명령...>      원격 명령 실행
  jobs list|watch|cancel               작업 조회
  files ls|stat|cat|mkdir|mv|rm        원격 파일 (<호스트>:<경로>)
  proc ls|kill                         원격 프로세스
  daemon run|start|stop|status         데몬 관리
  version                              버전 보기

종료 코드: 0 성공, 1 실패, 2 사용법 오류, 3 사람 확인 필요, 4 결과 불명, 5 오프라인·차단
exec는 원격 명령의 종료 코드를 그대로 돌려줍니다.
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
	case "proc":
		code = r.proc(ctx, args[1:])
	case "daemon":
		code = r.daemon(ctx, args[1:])
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

// fail reports err and returns the exit code for it.
func (r *run) fail(err error) int {
	var ue *usageError
	if errors.As(err, &ue) {
		fmt.Fprintln(r.Stderr, "사용법 오류: "+ue.msg)
		return exitUsage
	}
	var ae *apierr.Error
	if errors.As(err, &ae) {
		fmt.Fprintln(r.Stderr, "오류: "+ae.Message)
		if r.asJSON {
			writeJSON(r.Stdout, obj{"status": model.StatusFailed, "error": obj{"code": ae.Code, "message": ae.Message,
				"retryable": ae.Retryable, "next_action": ae.NextAction}})
		}
		switch ae.Code {
		case wire.CodeOffline, wire.CodeAuthRequired:
			return exitOffline
		case wire.CodeExecutionOutcomeUnknown, wire.CodeTimeout:
			return exitUnknown
		}
		return exitFailure
	}
	fmt.Fprintln(r.Stderr, "오류: "+err.Error())
	return exitFailure
}

// settle prints a result envelope in --json mode and returns the exit code
// of its status; human messages go to stderr.
func (r *run) settle(res obj, what string) int {
	if r.asJSON {
		writeJSON(r.Stdout, res)
	}
	return r.statusExit(res, what)
}

func (r *run) statusExit(res obj, what string) int {
	e := res.sub("error")
	switch res.str("status") {
	case model.StatusWaitingForUser:
		msg := e.str("message")
		fmt.Fprintf(r.Stderr, "%s: 사람의 확인이 필요합니다. %s\n", what, dash(msg))
		return exitNeedsUser
	case model.StatusUnknown:
		ids := "요청 " + dash(res.str("request_id"))
		next := "같은 명령을 다시 실행하기 전에 deskhand files stat 등으로 대상 상태를 먼저 확인하세요"
		if j := res.str("job_id"); j != "" {
			ids += ", 작업 " + j
			next = fmt.Sprintf("deskhand jobs watch %s 로 상태를 확인한 뒤 필요하면 다시 실행하세요", j)
		}
		fmt.Fprintf(r.Stderr, "%s: 결과를 알 수 없습니다(%s). %s\n", what, ids, next)
		return exitUnknown
	case model.StatusFailed:
		if e != nil {
			fmt.Fprintf(r.Stderr, "%s: %s\n", what, e.str("message"))
			switch e.str("code") {
			case wire.CodeOffline, wire.CodeAuthRequired:
				return exitOffline
			case wire.CodeExecutionOutcomeUnknown, wire.CodeTimeout:
				return exitUnknown
			}
		} else {
			fmt.Fprintf(r.Stderr, "%s: 실패했습니다\n", what)
		}
		return exitFailure
	}
	return exitOK
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
	if len(p) >= 2 && (p[0] == '\'' || p[0] == '"') && p[len(p)-1] == p[0] {
		p = p[1 : len(p)-1]
	}
	if p == "" {
		return remotePath{}, usagef("%q의 경로가 비었습니다. <호스트>:<경로> 형식으로 주세요", s)
	}
	return remotePath{Host: host, Path: p}, nil
}
