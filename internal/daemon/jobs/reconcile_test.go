package jobs

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func resultOf(t *testing.T, j model.Job) map[string]any {
	t.Helper()
	m := map[string]any{}
	if len(j.Result) > 0 {
		if err := json.Unmarshal(j.Result, &m); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func TestStartupSettlesJobsThatHadNoRemoteJobAsUnknown(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	before := New(st, events.NewBus(st, nil), conn.NewManager(conn.Options{}))
	run, _ := before.Create("pc", KindExec, "r-1", "exec.run in flight", nil)
	install, _ := before.Create("pc", KindInstall, "", "PC 추가: pc", nil)
	remote, _ := before.Create("pc", KindExec, "r-2", "exec.start", nil)
	before.SetRemote(remote.JobID, "j1", map[string]any{"job": "j1"})
	done, _ := before.Create("pc", KindExec, "r-3", "done", nil)
	before.Finish(done.JobID, model.StatusSucceeded, map[string]any{"code": 0})

	bus := events.NewBus(st, nil)
	sub := bus.Subscribe()
	r := New(st, bus, conn.NewManager(conn.Options{}))
	if n := r.SettleInterrupted(); n != 2 {
		t.Fatalf("settled %d jobs, want 2", n)
	}
	for _, id := range []string{run.JobID, install.JobID} {
		j, _ := r.current(id)
		res := resultOf(t, j)
		e, _ := res["error"].(map[string]any)
		if j.Status != model.StatusUnknown || j.FinishedAt == "" || e["code"] != wire.CodeExecutionOutcomeUnknown || e["next_action"] != "check_state" {
			t.Fatalf("job %s after the restart %+v %v", id, j, res)
		}
	}
	if j, _ := r.current(remote.JobID); j.Status != model.StatusRunning {
		t.Fatalf("remote job %+v", j)
	}
	if j, _ := r.current(done.JobID); j.Status != model.StatusSucceeded {
		t.Fatalf("finished job %+v", j)
	}
	updated := map[string]bool{}
	timeline := map[string]model.TimelineEntry{}
	for {
		select {
		case ev := <-sub.C:
			switch ev.Kind {
			case events.JobUpdated:
				if j := ev.Fields["job"].(model.Job); j.Status == model.StatusUnknown {
					updated[j.JobID] = true
				}
			case events.TimelineAppended:
				e := ev.Fields["entry"].(model.TimelineEntry)
				timeline[e.JobID] = e
			}
			continue
		default:
		}
		break
	}
	if !updated[run.JobID] || !updated[install.JobID] || len(updated) != 2 {
		t.Fatalf("job.updated for %v", updated)
	}
	if e := timeline[run.JobID]; e.Actor != model.ActorSystem || e.Kind != model.TimelineExec || e.Outcome != model.TimelineUnknown {
		t.Fatalf("timeline of the exec job %+v", e)
	}
	if e := timeline[install.JobID]; e.Kind != model.TimelineInstall || e.Outcome != model.TimelineUnknown {
		t.Fatalf("timeline of the install job %+v", e)
	}
}

func waitStatus(t *testing.T, r *Registry, jobID, want string) model.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, err := r.current(jobID)
		if err == nil && j.Status == want {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s is %+v, want %s", jobID, j, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAReadyHostFinishesTheJobsItsAgentEnded(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	before := New(st, events.NewBus(st, nil), conn.NewManager(conn.Options{}))
	ids := map[string]string{}
	for _, remote := range []string{"j1", "j2", "j3", "j4"} {
		j, _ := before.Create("pc", KindExec, "r-"+remote, remote, nil)
		before.SetRemote(j.JobID, remote, map[string]any{"job": remote, "pid": 5})
		ids[remote] = j.JobID
	}
	other, _ := before.Create("other-pc", KindExec, "r-o", "other", nil)
	before.SetRemote(other.JobID, "j1", map[string]any{"job": "j1"})
	agent := &scriptAgent{handle: func(req proto.Message) []proto.Message {
		if req.String("op") != "exec.list" {
			return nil
		}
		return []proto.Message{ok(proto.Message{"jobs": []any{
			map[string]any{"job": "j1", "state": "exited", "code": 3},
			map[string]any{"job": "j2", "state": "lost"},
			map[string]any{"job": "j3", "state": "running"},
		}})}
	}}
	e := connectAgentEnv(t, st, agent)
	if j := waitStatus(t, e.r, ids["j1"], model.StatusFailed); resultOf(t, j)["code"] != float64(3) {
		t.Fatalf("exited job %+v", j)
	}
	if j := waitStatus(t, e.r, ids["j2"], model.StatusUnknown); resultOf(t, j)["state"] != "lost" {
		t.Fatalf("lost job %+v", j)
	}
	if j := waitStatus(t, e.r, ids["j4"], model.StatusUnknown); resultOf(t, j)["state"] != "lost" {
		t.Fatalf("job the agent does not list %+v", j)
	}
	if j, _ := e.r.current(ids["j3"]); j.Status != model.StatusRunning {
		t.Fatalf("running job %+v", j)
	}
	if j, _ := e.r.current(other.JobID); j.Status != model.StatusRunning {
		t.Fatalf("job of another host %+v", j)
	}
	n := 0
	for _, op := range agent.requests() {
		if op == "exec.list" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("exec.list ran %d times", n)
	}
	e.host.Disconnect()
	e.host.Connect()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e.host.WaitState(ctx, func(s conn.ConnState) bool { return s == conn.StateReady })
	for deadline := time.Now().Add(5 * time.Second); ; {
		n = 0
		for _, op := range agent.requests() {
			if op == "exec.list" {
				n++
			}
		}
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("exec.list ran %d times after the reconnect", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
