package jobs

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// killableAgent runs remote job "j1" until exec.kill, which ends it with
// code 1 and the job.exited event; exec.read returns "partial" and, once
// the job was killed, eof.
type killableAgent struct {
	mu     sync.Mutex
	killed bool
	agent  *scriptAgent
}

func newKillableAgent() *killableAgent {
	k := &killableAgent{}
	k.agent = &scriptAgent{handle: func(req proto.Message) []proto.Message {
		k.mu.Lock()
		defer k.mu.Unlock()
		state := "running"
		if k.killed {
			state = "exited"
		}
		switch req.String("op") {
		case "exec.kill":
			k.killed = true
			return []proto.Message{{"t": "ev", "name": "job.exited", "job": "j1", "code": 1}, ok(proto.Message{"state": "exited"})}
		case "exec.read":
			out := ""
			if req.Int("out_offset") == 0 {
				out = "partial"
			}
			res := proto.Message{"state": state, "out": out, "err": "", "out_next": 7, "err_next": 0, "dropped": false, "eof": k.killed}
			if k.killed {
				res["code"] = 1
			}
			return []proto.Message{ok(res)}
		case "exec.list":
			return []proto.Message{ok(proto.Message{"jobs": []any{map[string]any{"job": "j1", "state": state}}})}
		}
		return nil
	}}
	return k
}

func (k *killableAgent) kills() int {
	n := 0
	for _, op := range k.agent.requests() {
		if op == "exec.kill" {
			n++
		}
	}
	return n
}

func startTimed(t *testing.T, r *Registry, timeoutMS int) model.Job {
	t.Helper()
	j, err := r.Create("pc", KindExec, "r-1", "slow", map[string]any{"cmd": "slow", "timeout_ms": timeoutMS})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetRemote(j.JobID, "j1", map[string]any{"job": "j1", "pid": 4}); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestExecStartTimeoutKillsTheJobAndEndsItFailedWithTimeout(t *testing.T) {
	k := newKillableAgent()
	e := newAgentEnv(t, k.agent)
	j := startTimed(t, e.r, 80)
	e.r.ArmTimeout(j.JobID)
	got := waitStatus(t, e.r, j.JobID, model.StatusFailed)
	res := resultOf(t, got)
	errObj, _ := res["error"].(map[string]any)
	if res["timed_out"] != true || errObj["code"] != wire.CodeTimeout || res["pid"] != float64(4) {
		t.Fatalf("result %v", res)
	}
	if n := k.kills(); n != 1 {
		t.Fatalf("exec.kill sent %d times", n)
	}
	out, err := e.r.Output(context.Background(), j.JobID, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	chunks := out["chunks"].([]map[string]any)
	if len(chunks) != 1 || chunks[0]["data"] != "partial" || out["next_cursor"] != "end" || out["status"] != model.StatusFailed ||
		out["timed_out"] != true {
		t.Fatalf("output %v", out)
	}
	if e, _ := out["error"].(map[string]any); e["code"] != wire.CodeTimeout {
		t.Fatalf("output error %v", out["error"])
	}
	for {
		select {
		case ev := <-e.sub.C:
			if ev.Kind == events.JobUpdated && ev.Fields["job"].(model.Job).Status == model.StatusSucceeded {
				t.Fatal("the timed out job was reported succeeded")
			}
			continue
		default:
		}
		break
	}
}

func TestTimeoutLeavesAJobThatEndedFirst(t *testing.T) {
	k := newKillableAgent()
	e := newAgentEnv(t, k.agent)
	j := startTimed(t, e.r, 50)
	e.r.ArmTimeout(j.JobID)
	e.r.HandleAgentEvent("pc", proto.Message{"name": "job.exited", "job": "j1", "code": float64(0)})
	time.Sleep(150 * time.Millisecond)
	if got, _ := e.r.current(j.JobID); got.Status != model.StatusSucceeded || k.kills() != 0 {
		t.Fatalf("job %+v, %d kills", got, k.kills())
	}
}

func TestCancelOfATimedJobStaysCancelled(t *testing.T) {
	k := newKillableAgent()
	e := newAgentEnv(t, k.agent)
	j := startTimed(t, e.r, 100)
	e.r.ArmTimeout(j.JobID)
	if got, err := e.r.Cancel(context.Background(), j.JobID, nil); err != nil || got.Status != model.StatusCancelled {
		t.Fatalf("cancel %+v, %v", got, err)
	}
	time.Sleep(200 * time.Millisecond)
	if got, _ := e.r.current(j.JobID); got.Status != model.StatusCancelled || k.kills() != 1 {
		t.Fatalf("job %+v, %d kills", got, k.kills())
	}
}

func TestAnExpiredTimeoutIsEnforcedWhenTheHostBecomesReady(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	old := time.Now().Add(-time.Hour)
	if err := st.CreateJob(store.Job{JobID: "j-old", HostID: "pc", Kind: KindExec, Status: model.StatusRunning, Summary: "slow",
		Params: []byte(`{"cmd":"slow","timeout_ms":1000}`), RemoteJob: "j1", CreatedAt: old, UpdatedAt: old}); err != nil {
		t.Fatal(err)
	}
	k := newKillableAgent()
	e := connectAgentEnv(t, st, k.agent)
	got := waitStatus(t, e.r, "j-old", model.StatusFailed)
	if res := resultOf(t, got); res["timed_out"] != true {
		t.Fatalf("result %v", res)
	}
}

func TestResumeTimeoutsArmsTheRunningTimedJobs(t *testing.T) {
	k := newKillableAgent()
	e := newAgentEnv(t, k.agent)
	j := startTimed(t, e.r, 100)
	plain, _ := e.r.Create("pc", KindExec, "r-2", "untimed", map[string]any{"cmd": "x"})
	e.r.SetRemote(plain.JobID, "j2", map[string]any{"job": "j2"})
	if n := e.r.ResumeTimeouts(); n != 1 {
		t.Fatalf("armed %d timeouts, want 1", n)
	}
	waitStatus(t, e.r, j.JobID, model.StatusFailed)
	if got, _ := e.r.current(plain.JobID); got.Status != model.StatusRunning {
		t.Fatalf("untimed job %+v", got)
	}
}

func TestCloseStopsTheTimers(t *testing.T) {
	k := newKillableAgent()
	e := newAgentEnv(t, k.agent)
	j := startTimed(t, e.r, 50)
	e.r.ArmTimeout(j.JobID)
	e.r.Close()
	time.Sleep(150 * time.Millisecond)
	if got, _ := e.r.current(j.JobID); got.Status != model.StatusRunning || k.kills() != 0 {
		t.Fatalf("job %+v, %d kills after Close", got, k.kills())
	}
}

func TestTimeoutRetriesAFailedKillWithoutAReconnect(t *testing.T) {
	k := newKillableAgent()
	handle := k.agent.handle
	kills := 0
	k.agent.handle = func(req proto.Message) []proto.Message {
		if req.String("op") == "exec.kill" {
			kills++
			if kills == 1 {
				return []proto.Message{{"t": "res", "ok": false, "code": wire.CodeInternal, "error": "temporary termination failure", "retryable": true}}
			}
		}
		return handle(req)
	}
	e := newAgentEnv(t, k.agent)
	j := startTimed(t, e.r, 50)
	e.r.ArmTimeout(j.JobID)
	got := waitStatus(t, e.r, j.JobID, model.StatusFailed)
	if res := resultOf(t, got); res["timed_out"] != true {
		t.Fatalf("result %v", res)
	}
	if n := k.kills(); n != 2 {
		t.Fatalf("exec.kill sent %d times, want failed attempt and retry", n)
	}
}

func TestTimeoutRetryStopsAfterCloseOrExit(t *testing.T) {
	for _, end := range []string{"close", "exit"} {
		t.Run(end, func(t *testing.T) {
			firstKill := make(chan struct{})
			var first sync.Once
			agent := &scriptAgent{handle: func(req proto.Message) []proto.Message {
				if req.String("op") == "exec.kill" {
					first.Do(func() { close(firstKill) })
					return []proto.Message{{"t": "res", "ok": false, "code": wire.CodeInternal, "error": "temporary termination failure", "retryable": true}}
				}
				return nil
			}}
			e := newAgentEnv(t, agent)
			j := startTimed(t, e.r, 50)
			e.r.ArmTimeout(j.JobID)
			select {
			case <-firstKill:
			case <-time.After(5 * time.Second):
				t.Fatal("timeout never attempted the kill")
			}
			if end == "close" {
				e.r.Close()
			} else {
				e.r.HandleAgentEvent("pc", proto.Message{"name": "job.exited", "job": "j1", "code": float64(0)})
			}
			time.Sleep(1500 * time.Millisecond)
			kills := 0
			for _, op := range agent.requests() {
				if op == "exec.kill" {
					kills++
				}
			}
			if kills != 1 {
				t.Fatalf("exec.kill sent %d times after %s, want only the initial failed kill", kills, end)
			}
		})
	}
}

func TestAnExitBetweenTimeoutKillAttemptsStillFailsWithTimeout(t *testing.T) {
	firstKill := make(chan struct{})
	var first sync.Once
	agent := &scriptAgent{handle: func(req proto.Message) []proto.Message {
		if req.String("op") == "exec.kill" {
			first.Do(func() { close(firstKill) })
			return []proto.Message{{"t": "res", "ok": false, "code": wire.CodeInternal, "error": "temporary termination failure", "retryable": true}}
		}
		return nil
	}}
	e := newAgentEnv(t, agent)
	j := startTimed(t, e.r, 30)
	e.r.ArmTimeout(j.JobID)
	select {
	case <-firstKill:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout never attempted the kill")
	}
	// Wait until the failed request is no longer in flight, before the
	// next attempt. The command now exits normally between the attempts.
	until := time.Now().Add(5 * time.Second)
	for {
		e.r.mu.Lock()
		pending := e.r.cancelling[j.JobID] != ""
		e.r.mu.Unlock()
		if !pending {
			break
		}
		if time.Now().After(until) {
			t.Fatal("failed kill stayed in flight")
		}
		time.Sleep(time.Millisecond)
	}
	e.r.HandleAgentEvent("pc", proto.Message{"name": "job.exited", "job": "j1", "code": float64(0)})
	got, _ := e.r.current(j.JobID)
	res := resultOf(t, got)
	errObj, _ := res["error"].(map[string]any)
	if got.Status != model.StatusFailed || res["timed_out"] != true || errObj["code"] != wire.CodeTimeout {
		t.Fatalf("exit after the timeout attempt: %+v result %v", got, res)
	}
}

func TestTimeoutDecisionSurvivesRegistryRestartBeforeTheExit(t *testing.T) {
	firstKill := make(chan struct{})
	var first sync.Once
	var mu sync.Mutex
	exited := false
	agent := &scriptAgent{handle: func(req proto.Message) []proto.Message {
		mu.Lock()
		defer mu.Unlock()
		switch req.String("op") {
		case "exec.kill":
			first.Do(func() { close(firstKill) })
			return []proto.Message{{"t": "res", "ok": false, "code": wire.CodeInternal, "error": "temporary termination failure", "retryable": true}}
		case "exec.list":
			state := "running"
			if exited {
				state = "exited"
			}
			return []proto.Message{ok(proto.Message{"jobs": []any{map[string]any{"job": "j1", "state": state, "code": 0}}})}
		}
		return nil
	}}
	e := newAgentEnv(t, agent)
	j := startTimed(t, e.r, 30)
	e.r.ArmTimeout(j.JobID)
	select {
	case <-firstKill:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout never attempted the kill")
	}
	e.r.Close()
	mu.Lock()
	exited = true
	mu.Unlock()
	restarted := New(e.st, events.NewBus(e.st, nil), e.mgr)
	defer restarted.Close()
	got, err := restarted.Get(context.Background(), j.JobID)
	if err != nil || got.Status != model.StatusFailed || resultOf(t, got)["timed_out"] != true {
		t.Fatalf("exit reconciled after registry restart: %+v, %v", got, err)
	}
}

func TestPastDeadlineWithoutTimeoutAttemptKeepsTheExitCode(t *testing.T) {
	r, _ := newRegistry(t)
	j := startTimed(t, r, 1)
	time.Sleep(10 * time.Millisecond)
	r.HandleAgentEvent("pc", proto.Message{"name": "job.exited", "job": "j1", "code": float64(0)})
	got, _ := r.current(j.JobID)
	if got.Status != model.StatusSucceeded || resultOf(t, got)["timed_out"] == true {
		t.Fatalf("late exit without an attempted timeout: %+v", got)
	}
}
