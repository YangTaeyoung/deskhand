package jobs

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func newRegistry(t *testing.T) (*Registry, *events.Sub) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	bus := events.NewBus(st, nil)
	sub := bus.Subscribe()
	mgr := conn.NewManager(conn.Options{})
	t.Cleanup(mgr.Close)
	return New(st, bus, mgr), sub
}

func lastJobEvent(t *testing.T, sub *events.Sub) model.Job {
	t.Helper()
	var last model.Job
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind == events.JobUpdated {
				last = ev.Fields["job"].(model.Job)
			}
		default:
			return last
		}
	}
}

func TestExitEventFinishesRemoteJob(t *testing.T) {
	r, sub := newRegistry(t)
	j, err := r.Create("pc", KindExec, "r-1", "sleep 1", map[string]any{"cmd": "sleep 1"})
	if err != nil || j.Status != model.StatusRunning || j.FinishedAt != "" {
		t.Fatalf("created %+v, %v", j, err)
	}
	if _, err := r.SetRemote(j.JobID, "3", map[string]any{"job": "3", "pid": 77}); err != nil {
		t.Fatal(err)
	}
	if pids := r.StartedPIDs("pc"); !pids[77] {
		t.Fatalf("started pids %v", pids)
	}
	r.HandleAgentEvent("pc", proto.Message{"name": "job.exited", "job": "3", "code": float64(2)})
	got, err := r.Get(context.Background(), j.JobID)
	if err != nil || got.Status != model.StatusFailed || got.FinishedAt == "" {
		t.Fatalf("job after exit %+v, %v", got, err)
	}
	var res map[string]any
	if err := json.Unmarshal(got.Result, &res); err != nil || res["code"] != float64(2) || res["pid"] != float64(77) {
		t.Fatalf("result %s", got.Result)
	}
	if ev := lastJobEvent(t, sub); ev.JobID != j.JobID || ev.Status != model.StatusFailed {
		t.Fatalf("last job.updated %+v", ev)
	}
	if len(r.StartedPIDs("pc")) != 0 {
		t.Fatal("a finished job still counts as started")
	}
}

func TestExitBeforeRemoteIDIsApplied(t *testing.T) {
	r, _ := newRegistry(t)
	j, _ := r.Create("pc", KindExec, "r-1", "true", nil)
	r.HandleAgentEvent("pc", proto.Message{"name": "job.exited", "job": "5", "code": float64(0)})
	got, err := r.SetRemote(j.JobID, "5", map[string]any{"job": "5", "pid": 9})
	if err != nil || got.Status != model.StatusSucceeded {
		t.Fatalf("job %+v, %v", got, err)
	}
}

func TestCancelFinishedJobRefused(t *testing.T) {
	r, _ := newRegistry(t)
	j, _ := r.Create("pc", KindExec, "r-1", "echo", nil)
	if _, err := r.Finish(j.JobID, model.StatusSucceeded, map[string]any{"code": 0, "stdout": "hi\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Cancel(context.Background(), j.JobID, nil); apierr.CodeOf(err) != wire.CodeInvalidArgument {
		t.Fatalf("cancel of a finished job: %v", err)
	}
	if _, err := r.Cancel(context.Background(), "j-none", nil); apierr.CodeOf(err) != wire.CodeNotFound {
		t.Fatalf("cancel of a missing job: %v", err)
	}
	out, err := r.Output(context.Background(), j.JobID, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	chunks := out["chunks"].([]map[string]any)
	if len(chunks) != 1 || chunks[0]["data"] != "hi\n" || out["next_cursor"] != "end" {
		t.Fatalf("output %v", out)
	}
	again, _ := r.Output(context.Background(), j.JobID, "end", 0)
	if len(again["chunks"].([]map[string]any)) != 0 {
		t.Fatalf("output after the end %v", again)
	}
}
