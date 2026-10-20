//go:build !windows

package server

import (
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
)

func TestProcListAndKill(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	start := c.ok("exec.start", proto.Message{"cmd": "sleep 30"})
	job, jobPID := start.String("job"), start.Int("pid")
	t.Cleanup(func() { c.call("exec.kill", proto.Message{"job": job}) })

	var target map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for target == nil {
		procs, _ := c.ok("proc.list", proto.Message{"name": "SLEEP"})["procs"].([]any)
		for _, p := range procs {
			m := p.(map[string]any)
			pid, ppid := int(m["pid"].(float64)), int(m["ppid"].(float64))
			if m["name"] == "sleep" && (pid == jobPID || ppid == jobPID) {
				target = m
			}
		}
		if target == nil {
			if time.Now().After(deadline) {
				t.Fatalf("sleep under job pid %d not listed", jobPID)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	if _, ok := target["mem_kb"]; !ok {
		t.Fatalf("proc without mem_kb: %v", target)
	}

	res := c.ok("proc.kill", proto.Message{"pid": int(target["pid"].(float64))})
	if res.Int("killed") != 1 {
		t.Fatalf("killed %v", res)
	}
	waitJobExited(t, c, job)
}

func TestProcKillByNameTree(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	job := c.ok("exec.start", proto.Message{"cmd": "exec -a deskhand-victim sleep 30"}).String("job")
	t.Cleanup(func() { c.call("exec.kill", proto.Message{"job": job}) })

	res := proto.Message{}
	deadline := time.Now().Add(5 * time.Second)
	for {
		procs, _ := c.ok("proc.list", proto.Message{"name": "deskhand-victim"})["procs"].([]any)
		if len(procs) > 0 {
			res = c.ok("proc.kill", proto.Message{"name": "deskhand-victim", "tree": true})
			break
		}
		if time.Now().After(deadline) {
			t.Skip("shell does not support exec -a")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if res.Int("killed") < 1 {
		t.Fatalf("res %v", res)
	}
	waitJobExited(t, c, job)
}

// shellWithChild is an exec.start request whose shell runs a child process
// for 30 s.
func shellWithChild() proto.Message { return proto.Message{"cmd": "sleep 30; echo done"} }

// longCommand is an exec.start request of a process that runs for 30 s.
func longCommand() proto.Message { return proto.Message{"argv": []any{"sleep", "30"}} }

// busyCommand is an exec.start request whose process keeps a processor busy.
func busyCommand() proto.Message { return proto.Message{"cmd": "while :; do :; done"} }

// unrelatedPID is a process the agent did not start.
func unrelatedPID() int { return 1 }
