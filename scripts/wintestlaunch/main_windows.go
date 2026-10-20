//go:build windows

// Command wintestlaunch runs one test binary for scripts/wintest.sh without
// a console window. It reads run.txt in its own folder, starts the binary
// there with CREATE_NO_WINDOW and its output in out.txt, writes
// "<launcher pid> <test pid>" to pid.txt once the binary runs, and writes
// the binary's exit code as a signed decimal to exit.txt when it ends.
//
// run.txt has one item per line: "exe <file in the folder>", "arg <argument>"
// (repeated, in order), "env <NAME=value>" (repeated) and "limit <seconds>".
// A binary still running after limit seconds is killed and the exit code is
// 124.
//
// Build it with -ldflags=-H=windowsgui so that it has no console either.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// createNoWindow is CREATE_NO_WINDOW: the binary gets a console without a
// window, which its own console children share.
const createNoWindow = 0x08000000

// exitTimeout is the exit code of a binary killed at its limit.
const exitTimeout = 124

type plan struct {
	exe   string
	args  []string
	env   []string
	limit time.Duration
}

func main() {
	self, err := os.Executable()
	if err != nil {
		os.Exit(1)
	}
	dir := filepath.Dir(self)
	code := run(dir)
	tmp := filepath.Join(dir, "exit.tmp")
	if os.WriteFile(tmp, []byte(strconv.Itoa(int(code))+"\n"), 0o644) == nil {
		_ = os.Rename(tmp, filepath.Join(dir, "exit.txt"))
	}
}

// run starts the binary of dir's run.txt and returns its signed exit code.
func run(dir string) int32 {
	out, err := os.Create(filepath.Join(dir, "out.txt"))
	if err != nil {
		return 1
	}
	defer out.Close()
	p, err := readPlan(filepath.Join(dir, "run.txt"))
	if err != nil {
		fmt.Fprintf(out, "wintest: %v\n", err)
		return 1
	}
	cmd := exec.Command(filepath.Join(dir, p.exe), p.args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), p.env...)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(out, "wintest: %v\n", err)
		return 1
	}
	_ = os.WriteFile(filepath.Join(dir, "pid.txt"), []byte(fmt.Sprintf("%d %d\n", os.Getpid(), cmd.Process.Pid)), 0o644)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var ee *exec.ExitError
		switch {
		case err == nil:
			return 0
		case errors.As(err, &ee):
			return int32(uint32(ee.ExitCode()))
		}
		fmt.Fprintf(out, "wintest: %v\n", err)
		return 1
	case <-time.After(p.limit):
		_ = cmd.Process.Kill()
		<-done
		fmt.Fprintf(out, "\nwintest: 테스트가 %d초 안에 끝나지 않아 종료했습니다.\n", int(p.limit/time.Second))
		return exitTimeout
	}
}

// readPlan reads run.txt.
func readPlan(path string) (plan, error) {
	f, err := os.Open(path)
	if err != nil {
		return plan{}, err
	}
	defer f.Close()
	p := plan{limit: 480 * time.Second}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		key, value, _ := strings.Cut(strings.TrimRight(sc.Text(), "\r"), " ")
		switch key {
		case "exe":
			p.exe = value
		case "arg":
			p.args = append(p.args, value)
		case "env":
			p.env = append(p.env, value)
		case "limit":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 {
				return plan{}, fmt.Errorf("run.txt limit %q is not a number of seconds", value)
			}
			p.limit = time.Duration(n) * time.Second
		case "":
		default:
			return plan{}, fmt.Errorf("run.txt has an unknown line %q", key)
		}
	}
	if err := sc.Err(); err != nil {
		return plan{}, err
	}
	if p.exe == "" {
		return plan{}, errors.New("run.txt names no exe")
	}
	return p, nil
}
