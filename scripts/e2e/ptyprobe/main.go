// ptyprobe attaches to a remote shell of the Deskhand daemon
// (stream.attach kind pty, docs/local-api.md §4) for the end-to-end
// scripts.
//
//	ptyprobe (-job J | -pty P) [-send TEXT]… [-expect RE] [-timeout 30s] [-hold 0s]
//
// It sends 120×40 after each attach frame, then each -send as input, and
// reads the output with terminal escape sequences removed until it matches
// -expect, the shell ends or -timeout passes. With -hold it goes on reading
// until the stream ends or -hold passes. It prints one JSON line:
// {job, pid, attached, matched, exited, exit_code, closed, output} where
// closed means the stream ended without the shell's exit frame, and exits 0
// when -expect matched (or was not given), 1 otherwise.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/client"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

type sends []string

func (s *sends) String() string     { return strings.Join(*s, ",") }
func (s *sends) Set(v string) error { *s = append(*s, v); return nil }

var escapes = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][0-9A-Za-z]|\x1b[=>78]`)

func main() {
	os.Exit(run())
}

func run() int {
	job := flag.String("job", "", "daemon job_id of the shell")
	ptyID := flag.String("pty", "", "pty_id from pty.open")
	var in sends
	flag.Var(&in, "send", "input to send (repeatable)")
	expect := flag.String("expect", "", "regular expression the output has to match")
	timeout := flag.Duration("timeout", 30*time.Second, "how long to wait for -expect")
	hold := flag.Duration("hold", 0, "how long to keep reading after -expect")
	flag.Parse()
	var re *regexp.Regexp
	if *expect != "" {
		var err error
		if re, err = regexp.Compile(*expect); err != nil {
			fmt.Fprintln(os.Stderr, "ptyprobe:", err)
			return 2
		}
	}
	params := map[string]any{}
	if *job != "" {
		params["job_id"] = *job
	}
	if *ptyID != "" {
		params["pty_id"] = *ptyID
	}
	ctx := context.Background()
	st, err := client.Attach(ctx, client.Options{}, "cli", "e2e-ptyprobe", "pty", params)
	if err != nil {
		out := map[string]any{"error": err.Error()}
		if ae, ok := err.(*client.Error); ok {
			out["code"] = ae.Code
		}
		printJSON(out)
		return 1
	}
	defer st.Close()

	type frameMsg struct {
		kind byte
		data []byte
		err  error
	}
	frames := make(chan frameMsg, 64)
	go func() {
		for {
			k, p, err := st.ReadFrame(wire.MaxFrame)
			frames <- frameMsg{k, p, err}
			if err != nil {
				return
			}
		}
	}()
	res := map[string]any{"job": "", "pid": 0, "attached": 0, "matched": re == nil, "exited": false, "closed": false}
	var text strings.Builder
	sent := false
	deadline := time.After(*timeout)
	var holdEnd <-chan time.Time
	if re == nil && *hold > 0 {
		holdEnd = time.After(*hold)
	}
	done := re == nil && *hold == 0 && len(in) == 0
	finish := func() int {
		out := escapes.ReplaceAllString(text.String(), "")
		out = strings.ReplaceAll(out, "\r", "")
		if len(out) > 6000 {
			out = out[len(out)-6000:]
		}
		res["output"] = out
		printJSON(res)
		if res["matched"] == true {
			return 0
		}
		return 1
	}
	for !done {
		select {
		case f := <-frames:
			if f.err != nil {
				res["closed"] = res["exited"] != true
				return finish()
			}
			switch f.kind {
			case wire.KindPTYAttached:
				var a wire.PTYAttached
				_ = json.Unmarshal(f.data, &a)
				res["job"], res["pid"] = a.Job, a.PID
				res["attached"] = res["attached"].(int) + 1
				b, _ := json.Marshal(wire.PTYResize{Cols: 120, Rows: 40})
				_ = st.WriteFrame(wire.KindPTYResize, b)
				if !sent {
					sent = true
					go func() {
						time.Sleep(300 * time.Millisecond)
						for _, s := range in {
							_ = st.WriteFrame(wire.KindPTYData, []byte(s))
							time.Sleep(200 * time.Millisecond)
						}
					}()
				}
				if re == nil && *hold == 0 {
					go func() {
						time.Sleep(time.Duration(len(in))*200*time.Millisecond + 500*time.Millisecond)
						frames <- frameMsg{kind: 0xff}
					}()
				}
			case wire.KindPTYData:
				text.Write(f.data)
				if re != nil && res["matched"] != true && re.MatchString(strings.ReplaceAll(escapes.ReplaceAllString(text.String(), ""), "\r", "")) {
					res["matched"] = true
					if *hold == 0 {
						return finish()
					}
					holdEnd = time.After(*hold)
				}
			case wire.KindPTYExit:
				var e wire.PTYExit
				_ = json.Unmarshal(f.data, &e)
				res["exited"], res["exit_code"] = true, e.Code
			case 0xff:
				return finish()
			}
		case <-deadline:
			if res["matched"] != true {
				return finish()
			}
		case <-holdEnd:
			return finish()
		}
	}
	return finish()
}

func printJSON(v any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}
