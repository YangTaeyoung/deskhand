package server

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestStartedByDeskhandFollowsTheDescendantsOfTheStartedProcesses(t *testing.T) {
	at := func(s int) time.Time { return time.Unix(1000+int64(s), 0).UTC() }
	procs := []procInfo{
		{PID: 4, PPID: 0, Started: at(0)},
		{PID: 10, PPID: 4, Started: at(10)},
		{PID: 11, PPID: 10, Started: at(11)},
		{PID: 12, PPID: 11, Started: at(12)},
		{PID: 13, PPID: 10, Started: at(9)},
		{PID: 20, PPID: 4, Started: at(20)},
		{PID: 21, PPID: 20, Started: at(21)},
		{PID: 31, PPID: 30, Started: at(31)},
		{PID: 41, PPID: 40, Started: at(46)},
		{PID: 51, PPID: 50, Started: at(52)},
		{PID: 60, PPID: 4, Started: at(61)},
		{PID: 70, PPID: 4, Started: at(65)},
		{PID: 80, PPID: 80, Started: at(80)},
		{PID: 90, PPID: 91, Started: at(90)},
		{PID: 91, PPID: 90, Started: at(91)},
		{PID: 100, PPID: 10},
	}
	roots := []startedRoot{
		{pid: 10, created: at(10), notBefore: at(10)},
		{pid: 20, created: at(19), notBefore: at(19)},
		{pid: 30, created: at(30), notBefore: at(30), exited: at(35)},
		{pid: 40, created: at(40), notBefore: at(40), exited: at(45)},
		{pid: 50, notBefore: at(50)},
		{pid: 60, notBefore: at(60)},
		{pid: 70, notBefore: at(70)},
	}
	marked := startedByDeskhand(procs, roots)
	var got []int
	for pid, ok := range marked {
		if ok {
			got = append(got, pid)
		}
	}
	sort.Ints(got)
	if fmt.Sprint(got) != "[10 11 12 31 51 60]" {
		t.Fatalf("marked %v", got)
	}
}

func TestStartedRootsKeepTheNewestAndTheRunning(t *testing.T) {
	var tab startedTable
	base := time.Unix(5000, 0)
	for i := range maxStartedRoots + 10 {
		tab.add(1000+i, time.Time{}, base.Add(time.Duration(i)*time.Second))
		if i != 3 {
			tab.exited(1000+i, base.Add(time.Duration(i)*time.Second+time.Millisecond))
		}
	}
	roots := tab.list()
	if len(roots) != maxStartedRoots {
		t.Fatalf("%d roots kept", len(roots))
	}
	pids := map[int]bool{}
	for _, r := range roots {
		pids[r.pid] = true
	}
	if !pids[1003] || pids[1000] || !pids[1000+maxStartedRoots+9] {
		t.Fatalf("kept the running root %v, the oldest %v, the newest %v", pids[1003], pids[1000], pids[1000+maxStartedRoots+9])
	}
}

func TestCPUPercentIsTheShareOfAllLogicalProcessors(t *testing.T) {
	cases := []struct {
		used, elapsed time.Duration
		cpus          int
		want          float64
	}{
		{250 * time.Millisecond, 500 * time.Millisecond, 4, 12.5},
		{500 * time.Millisecond, 500 * time.Millisecond, 1, 100},
		{900 * time.Millisecond, 500 * time.Millisecond, 1, 100},
		{-time.Millisecond, 500 * time.Millisecond, 2, 0},
		{time.Second, 0, 4, 0},
		{1 * time.Millisecond, 500 * time.Millisecond, 16, 0},
		{4 * time.Millisecond, 500 * time.Millisecond, 16, 0.1},
	}
	for _, c := range cases {
		if got := cpuPercent(c.used, c.elapsed, c.cpus); got != c.want {
			t.Errorf("%s of %s on %d: %v, expected %v", c.used, c.elapsed, c.cpus, got, c.want)
		}
	}
}

func TestATreeKillWouldEndTheAgentWhenTheTreeHoldsIt(t *testing.T) {
	at := func(s int) time.Time { return time.Unix(1000+int64(s), 0) }
	procs := []procEntry{{PID: 1, PPID: 0, Created: at(0)}, {PID: 5, PPID: 1, Created: at(5)}, {PID: 6, PPID: 5, Created: at(6)}, {PID: 7, PPID: 5, Created: at(7)}}
	for root, want := range map[int]bool{1: true, 5: true, 6: true, 7: false, 9: false} {
		if got := treeHolds(procs, root, 6); got != want {
			t.Errorf("tree of %d holds 6: %v", root, got)
		}
	}
	reused := []procEntry{{PID: 5, PPID: 1, Created: at(8)}, {PID: 6, PPID: 5, Created: at(6)}}
	if treeHolds(reused, 5, 6) {
		t.Fatal("a parent created after the agent holds it")
	}
}

// listed returns the proc.list entry of pid, or nil.
func listed(c *rpcClient, pid int) map[string]any {
	procs, _ := c.ok("proc.list", nil)["procs"].([]any)
	for _, p := range procs {
		m := p.(map[string]any)
		if int(m["pid"].(float64)) == pid {
			return m
		}
	}
	return nil
}

// childOf waits for a listed process whose parent is pid and returns it.
func childOf(t *testing.T, c *rpcClient, pid int) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		procs, _ := c.ok("proc.list", nil)["procs"].([]any)
		for _, p := range procs {
			m := p.(map[string]any)
			if int(m["ppid"].(float64)) == pid {
				return m
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no child of pid %d listed", pid)
		}
	}
}

func TestProcListMarksTheAgentAndWhatItStarted(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	start := c.ok("exec.start", shellWithChild())
	job, pid := start.String("job"), start.Int("pid")
	t.Cleanup(func() { c.call("exec.kill", proto.Message{"job": job}) })
	child := childOf(t, c, pid)
	procs, _ := c.ok("proc.list", nil)["procs"].([]any)
	var self, root map[string]any
	for _, p := range procs {
		m := p.(map[string]any)
		started, _ := m["started"].(string)
		if at, err := time.Parse(time.RFC3339Nano, started); started != "" && (err != nil || wire.FormatDiagTime(at) != started) {
			t.Errorf("pid %v started %q", m["pid"], started)
		}
		if cpu, ok := m["cpu_pct"].(float64); !ok || cpu < 0 || cpu > 100 {
			t.Errorf("pid %v cpu_pct %v", m["pid"], m["cpu_pct"])
		}
		switch int(m["pid"].(float64)) {
		case os.Getpid():
			self = m
		case pid:
			root = m
		case int(child["pid"].(float64)):
			if m["started_by_deskhand"] != true || m["self"] != false {
				t.Errorf("child %v", m)
			}
		default:
			if m["self"] != false {
				t.Errorf("pid %v is self", m["pid"])
			}
		}
	}
	if self == nil || self["self"] != true || self["started_by_deskhand"] != false || self["started"] == nil || self["user"] == nil {
		t.Fatalf("self %v", self)
	}
	if root == nil || root["started_by_deskhand"] != true || root["started"] == nil {
		t.Fatalf("job process %v", root)
	}
	if other := listed(c, unrelatedPID()); other == nil || other["started_by_deskhand"] != false {
		t.Fatalf("pid %d %v", unrelatedPID(), other)
	}
}

func TestProcKillChecksTheStartTimeOfThePID(t *testing.T) {
	for _, guarded := range []bool{false, true} {
		t.Run(fmt.Sprintf("writer_guarded=%t", guarded), func(t *testing.T) {
			c, _ := newRPCClient(t, Options{})
			request := func(params proto.Message) proto.Message {
				if guarded {
					params["gen"] = 0
				}
				return params
			}
			start := c.ok("exec.start", longCommand())
			job, pid := start.String("job"), start.Int("pid")
			t.Cleanup(func() { c.call("exec.kill", proto.Message{"job": job}) })
			entry := listed(c, pid)
			if entry == nil {
				t.Fatalf("pid %d is not listed", pid)
			}
			res := c.call("proc.kill", request(proto.Message{"pid": pid, "started": "2001-01-01T00:00:00.000Z"}))
			if res["ok"] != false || res.String("code") != wire.CodeStaleTarget || !strings.Contains(res.String("error"), "proc.list") {
				t.Fatalf("stale kill %v", res)
			}
			if listed(c, pid) == nil {
				t.Fatalf("pid %d ended after a stale kill", pid)
			}
			for _, bad := range []proto.Message{{"pid": pid, "started": "yesterday"}, {"name": "x", "started": entry["started"]}} {
				if res := c.call("proc.kill", request(bad)); res.String("code") != wire.CodeInvalidArgument {
					t.Fatalf("%v: %v", bad, res)
				}
			}
			if res := c.ok("proc.kill", request(proto.Message{"pid": pid, "started": entry["started"]})); res.Int("killed") != 1 {
				t.Fatalf("kill %v", res)
			}
			waitJobExited(t, c, job)
			if res := c.call("proc.kill", request(proto.Message{"pid": pid, "started": entry["started"]})); res.String("code") != wire.CodeStaleTarget {
				t.Fatalf("kill of an ended process %v", res)
			}
		})
	}
}

func TestProcKillNeverEndsTheAgent(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	for _, req := range []proto.Message{{"pid": os.Getpid()}, {"pid": os.Getpid(), "tree": true}, {"pid": os.Getppid(), "tree": true}} {
		res := c.call("proc.kill", req)
		if res["ok"] != false || res.String("code") != wire.CodePermissionDenied || res["retryable"] != false {
			t.Fatalf("%v: %v", req, res)
		}
	}
}

func TestProcListCPU(t *testing.T) {
	c, _ := newRPCClient(t, Options{})
	start := c.ok("exec.start", busyCommand())
	job := start.String("job")
	t.Cleanup(func() { c.call("exec.kill", proto.Message{"job": job}) })
	busy := start.Int("pid")
	floor := 50.0 / float64(runtime.NumCPU())
	var cpu float64
	for range 3 {
		m := listed(c, busy)
		if m == nil {
			t.Fatalf("pid %d is not listed", busy)
		}
		if cpu = m["cpu_pct"].(float64); cpu >= floor {
			break
		}
	}
	if cpu < floor || cpu > 100 {
		t.Fatalf("the busy process uses %v%%, expected at least %v%% of %d logical processors", cpu, floor, runtime.NumCPU())
	}
	procs, _ := c.ok("proc.list", nil)["procs"].([]any)
	sum := 0.0
	for _, p := range procs {
		sum += p.(map[string]any)["cpu_pct"].(float64)
	}
	if sum > 100+0.05*float64(len(procs)) {
		t.Fatalf("the processes use %v%% in all", sum)
	}
	t.Logf("busy process %d: %v%%; %d processes: %v%% in all", busy, cpu, len(procs), sum)
}

func TestTheTreeOfAPIDListsItsDescendantsChildrenFirst(t *testing.T) {
	at := func(s int) time.Time { return time.Unix(1000+int64(s), 0) }
	procs := []procEntry{
		{PID: 1, PPID: 0, Created: at(0)},
		{PID: 5, PPID: 1, Name: "root.exe"},
		{PID: 6, PPID: 5, Name: "child.exe", Created: at(6)},
		{PID: 7, PPID: 6, Name: "grandchild.exe", Created: at(7)},
		{PID: 8, PPID: 5, Name: "older.exe", Created: at(4)},
		{PID: 9, PPID: 5, Name: "agent.exe", Created: at(9)},
		{PID: 10, PPID: 9, Name: "job.exe", Created: at(10)},
		{PID: 11, PPID: 5, Name: "unknown.exe"},
	}
	got := treeTargets(procs, 5, at(5), 9)
	if fmt.Sprint(targetPIDs(got)) != "[7 6]" || !got[0].Created.Equal(at(7)) {
		t.Fatalf("targets %+v", got)
	}
	if got := treeTargets(procs, 6, at(6), 9); fmt.Sprint(targetPIDs(got)) != "[7]" {
		t.Fatalf("targets of 6 %v", targetPIDs(got))
	}
}
