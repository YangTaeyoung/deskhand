package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/client"
	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const shellHelp = `사용법: deskhand shell <호스트> [--shell powershell|pwsh|cmd]
       deskhand shell --attach <job-id>

PC에서 대화형 셸을 열고 이 터미널을 그 셸에 연결합니다. 셸은 PC에서 실행되고
창 크기는 이 터미널을 따라갑니다.

옵션:
  --shell S        열 셸. 기본은 PC가 알려 준 첫 셸(Windows는 powershell)입니다.
  --attach JOB     이미 열린 셸(deskhand jobs list --kind pty)에 다시 연결합니다.

Ctrl-] 를 누르면 분리합니다. 셸은 PC에서 계속 실행되고 --attach로 다시 연결하면
그동안의 출력(최대 1 MiB)부터 보입니다. PC 연결이 끊기면 다시 연결될 때까지(최대 2분)
기다렸다가 이어서 붙습니다. 셸을 끝내려면 셸에서 exit를 입력하거나 deskhand jobs cancel <job-id>를 쓰세요.
표준 입력이 터미널이 아니면 입력을 그대로 보내고 Ctrl-] 분리는 없으며, 입력이 끝나도 셸이 끝날 때까지
기다립니다. 마지막 줄에 exit를 보내세요.

종료 코드: 셸의 종료 코드(1-255는 그대로, 범위 밖은 1). 분리하면 0입니다.
`

// detachKey is the byte of Ctrl-], which detaches deskhand shell.
const detachKey = 0x1d

const (
	// shellReattachFor bounds how long deskhand shell tries to attach again
	// after its stream dropped.
	shellReattachFor = 2 * time.Minute
	// shellBackoffMax bounds the wait between two attach attempts.
	shellBackoffMax = 5 * time.Second
)

// Terminal is the local terminal of deskhand shell.
type Terminal interface {
	// Size returns the columns and rows of the terminal; ok is false when
	// stdin is not a terminal.
	Size() (cols, rows int, ok bool)
	// Raw puts the terminal into raw mode and returns the function that
	// restores its mode.
	Raw() (restore func(), err error)
	// Resizes returns a channel that receives a value after each change of
	// the terminal's size, and the function that stops it.
	Resizes() (<-chan struct{}, func())
}

func (r *run) shell(ctx context.Context, args []string) int {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "-help" {
			fmt.Fprint(r.Stdout, shellHelp)
			return exitOK
		}
	}
	fs := newFlags("shell")
	shellName := fs.String("shell", "", "powershell|pwsh|cmd")
	attachJob := fs.String("attach", "", "다시 연결할 셸의 job id")
	pos, after, err := parseArgs(fs, args)
	if err != nil {
		return r.fail(err)
	}
	switch {
	case len(after) > 0:
		return r.fail(usagef("deskhand shell은 -- 뒤의 인수를 받지 않습니다. 셸을 연 뒤 명령을 입력하세요"))
	case r.asJSON:
		return r.fail(usagef("deskhand shell은 --json을 지원하지 않습니다. 명령 결과가 필요하면 deskhand exec --json을 쓰세요"))
	case *attachJob != "" && (len(pos) > 0 || *shellName != ""):
		return r.fail(usagef("--attach에는 호스트나 --shell을 함께 줄 수 없습니다(deskhand shell --attach <job-id>)"))
	case *attachJob == "" && len(pos) != 1:
		return r.fail(usagef("deskhand shell <호스트> [--shell powershell|pwsh|cmd] 형식으로 주세요(deskhand shell --help)"))
	}
	term := r.Term
	cols, rows, isTerm := term.Size()
	if !isTerm || cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	jobID := *attachJob
	target := map[string]any{"job_id": jobID}
	if jobID == "" {
		host := pos[0]
		c, err := r.dial(ctx)
		if err != nil {
			return r.fail(err)
		}
		params := map[string]any{"host_id": host, "cols": cols, "rows": rows}
		if *shellName != "" {
			params["shell"] = *shellName
		}
		res, err := r.call(ctx, c, "pty.open", params, callTimeout)
		c.Close()
		if err != nil {
			return r.failMut(err, host)
		}
		r.reqID, r.jobID = res.str("request_id"), res.str("job_id")
		if res.str("status") != model.StatusRunning {
			return r.statusExit(res, "터미널 열기", 0)
		}
		jobID = res.str("job_id")
		target = map[string]any{"pty_id": res.str("pty_id")}
		if isTerm {
			r.info("%s을 열었습니다(job %s). Ctrl-] 로 분리합니다.", res.sub("job").str("summary"), jobID)
		}
	}
	r.jobID = jobID
	return r.relayShell(ctx, term, isTerm, jobID, target)
}

// shellEnd is how one attached stream of deskhand shell ended.
type shellEnd int

const (
	shellExited shellEnd = iota
	shellDetached
	shellDropped
	shellInterrupted
)

// relayShell attaches to target and relays this terminal to the shell
// until it exits or is detached, attaching again by job id while the
// stream drops within shellReattachFor.
func (r *run) relayShell(ctx context.Context, term Terminal, isTerm bool, jobID string, target map[string]any) int {
	input := make(chan []byte)
	go readInput(r.Stdin, input)
	var resizes <-chan struct{}
	if isTerm {
		var stop func()
		resizes, stop = term.Resizes()
		defer stop()
	}
	undo := func() {}
	restore := func() {
		undo()
		undo = func() {}
	}
	defer restore()
	attached := false
	var giveUp time.Time
	wait := 500 * time.Millisecond
	detached := func() int {
		restore()
		r.info("\r\n분리했습니다. 셸은 PC에서 계속 실행됩니다. 다시 연결하려면 deskhand shell --attach %s 를 실행하세요", jobID)
		return exitOK
	}
	for {
		st, quit, err := r.attachShell(ctx, target, isTerm && attached, input)
		if quit {
			return detached()
		}
		if err != nil {
			if !attached {
				code := r.fail(err)
				if _, opened := target["pty_id"]; opened {
					fmt.Fprintf(r.Stderr, "셸 job %s는 PC에서 계속 실행 중일 수 있습니다. deskhand shell --attach %s 로 다시 연결하거나 deskhand jobs cancel %s 로 끝내세요\n", jobID, jobID, jobID)
				}
				return code
			}
			if apierr.CodeOf(err) == wire.CodeNotFound {
				restore()
				return r.endedShell(ctx, jobID)
			}
			if ctx.Err() != nil || time.Now().After(giveUp) {
				restore()
				return r.fail(err)
			}
			if r.pause(ctx, wait, isTerm, input) {
				return detached()
			}
			wait = min(2*wait, shellBackoffMax)
			continue
		}
		target = map[string]any{"job_id": jobID}
		if !attached && isTerm {
			if u, err := term.Raw(); err == nil {
				undo = u
			}
		}
		attached = true
		end, code := r.pumpShell(ctx, st, term, isTerm, input, resizes)
		st.Close()
		switch end {
		case shellExited:
			restore()
			return remoteExitCode(int64(code))
		case shellDetached, shellInterrupted:
			return detached()
		}
		r.info("\r\nPC와의 연결이 끊겼습니다. 다시 연결하는 중입니다… (Ctrl-] 로 분리)\r")
		giveUp = time.Now().Add(shellReattachFor)
		wait = 500 * time.Millisecond
	}
}

// attachShell attaches a stream to target. While watchInput is set, input
// that arrives meanwhile is dropped and Ctrl-] in it gives up the attach:
// quit is then true.
func (r *run) attachShell(ctx context.Context, target map[string]any, watchInput bool, input <-chan []byte) (*client.Stream, bool, error) {
	type result struct {
		st  *client.Stream
		err error
	}
	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan result, 1)
	go func() {
		st, err := client.Attach(actx, client.Options{Version: r.Version}, "cli", "deskhand", "pty", target)
		done <- result{st, err}
	}()
	if !watchInput {
		input = nil
	}
	for {
		select {
		case res := <-done:
			return res.st, false, res.err
		case b, ok := <-input:
			if !ok {
				input = nil
				continue
			}
			if bytes.IndexByte(b, detachKey) >= 0 {
				cancel()
				if res := <-done; res.st != nil {
					res.st.Close()
				}
				return nil, true, nil
			}
		}
	}
}

// pause waits d or until ctx ends; on a terminal, input that arrives
// meanwhile is dropped and Ctrl-] in it ends the wait with true.
func (r *run) pause(ctx context.Context, d time.Duration, isTerm bool, input <-chan []byte) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	if !isTerm {
		input = nil
	}
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		case b, ok := <-input:
			if !ok {
				input = nil
				continue
			}
			if bytes.IndexByte(b, detachKey) >= 0 {
				return true
			}
		}
	}
}

// readInput sends what r reads to input until r ends, then closes input.
func readInput(r io.Reader, input chan<- []byte) {
	defer close(input)
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			input <- bytes.Clone(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// pumpShell relays one attached stream: data and the exit from the
// stream to stdout, input to the stream, and on a terminal the size after
// each attached frame and size change. Ctrl-] on a terminal detaches.
func (r *run) pumpShell(ctx context.Context, st *client.Stream, term Terminal, isTerm bool, input <-chan []byte, resizes <-chan struct{}) (shellEnd, int) {
	type frame struct {
		kind    byte
		payload []byte
	}
	frames := make(chan frame, 64)
	go func() {
		defer close(frames)
		for {
			kind, payload, err := st.ReadFrame(wire.MaxFrame)
			if err != nil {
				return
			}
			frames <- frame{kind, payload}
		}
	}()
	sendSize := func() bool {
		if !isTerm {
			return true
		}
		cols, rows, ok := term.Size()
		if !ok || cols <= 0 || rows <= 0 {
			return true
		}
		b, _ := json.Marshal(wire.PTYResize{Cols: cols, Rows: rows})
		return st.WriteFrame(wire.KindPTYResize, b) == nil
	}
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				return shellDropped, 0
			}
			switch f.kind {
			case wire.KindPTYAttached:
				if !sendSize() {
					return shellDropped, 0
				}
			case wire.KindPTYData:
				if _, err := r.Stdout.Write(f.payload); err != nil {
					return shellInterrupted, 0
				}
			case wire.KindPTYExit:
				var e wire.PTYExit
				_ = json.Unmarshal(f.payload, &e)
				return shellExited, e.Code
			}
		case b, ok := <-input:
			if !ok {
				input = nil
				continue
			}
			detach := false
			if isTerm {
				if i := bytes.IndexByte(b, detachKey); i >= 0 {
					b, detach = b[:i], true
				}
			}
			if len(b) > 0 && st.WriteFrame(wire.KindPTYData, b) != nil {
				return shellDropped, 0
			}
			if detach {
				return shellDetached, 0
			}
		case <-resizes:
			if !sendSize() {
				return shellDropped, 0
			}
		case <-ctx.Done():
			return shellInterrupted, 0
		}
	}
}

// endedShell reports the end of a shell that ended while deskhand shell
// was not attached, from its job, and returns the exit code for it.
func (r *run) endedShell(ctx context.Context, jobID string) int {
	c, err := r.dial(ctx)
	if err != nil {
		return r.fail(err)
	}
	defer c.Close()
	job, err := r.call(ctx, c, "jobs.get", map[string]any{"job_id": jobID}, callTimeout)
	if err != nil {
		var ae *apierr.Error
		if errors.As(err, &ae) && ae.Code == wire.CodeNotFound {
			fmt.Fprintf(r.Stderr, "셸 job %s가 없습니다. deskhand jobs list --kind pty 로 확인하세요\n", jobID)
			return exitFailure
		}
		return r.fail(err)
	}
	res := job.sub("result")
	if code, ok := res.num("code"); ok {
		r.info("연결이 끊긴 동안 셸이 종료 코드 %d로 끝났습니다", code)
		return remoteExitCode(code)
	}
	if job.str("status") == model.StatusCancelled {
		fmt.Fprintf(r.Stderr, "셸 job %s가 취소됐습니다. 새 셸은 deskhand shell <호스트> 로 여세요\n", jobID)
		return exitFailure
	}
	fmt.Fprintf(r.Stderr, "셸 job %s가 %s 상태로 끝났습니다. %s\n", jobID, job.str("status"), dash(res.sub("error").str("message")))
	if job.str("status") == model.StatusUnknown {
		return exitUnknown
	}
	return exitFailure
}
