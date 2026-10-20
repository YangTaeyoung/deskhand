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
