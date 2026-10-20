package jobs

import (
	"path/filepath"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
)

func TestStartupLeavesTransferJobsToTheirManager(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	before := New(st, events.NewBus(st, nil), conn.NewManager(conn.Options{}))
	tj, _ := before.Create("pc", KindTransfer, "", "보내기", map[string]any{"transfer_id": "t-1"})
	r := New(st, events.NewBus(st, nil), conn.NewManager(conn.Options{}))
	if n := r.SettleInterrupted(); n != 0 {
		t.Fatalf("settled %d jobs", n)
	}
	if j, _ := r.current(tj.JobID); j.Status != model.StatusRunning || j.FinishedAt != "" {
		t.Fatalf("transfer job after the restart %+v", j)
	}
}

func TestMirrorSetsTheStateOfAJobAgainAndAgain(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	bus := events.NewBus(st, nil)
	sub := bus.Subscribe()
	defer sub.Close()
	r := New(st, bus, conn.NewManager(conn.Options{}))
	j, _ := r.Create("pc", KindTransfer, "", "보내기", nil)
	for _, step := range []struct {
		status   string
		finished bool
	}{{model.StatusFailed, true}, {model.StatusRunning, false}, {model.StatusSucceeded, true}} {
		got, err := r.Mirror(j.JobID, step.status, map[string]any{"transfer_id": "t-1"}, step.finished)
		if err != nil || got.Status != step.status || (got.FinishedAt != "") != step.finished || resultOf(t, got)["transfer_id"] != "t-1" {
			t.Fatalf("mirror %s: %+v, %v", step.status, got, err)
		}
	}
	n := 0
	for len(sub.C) > 0 {
		if ev := <-sub.C; ev.Kind == events.JobUpdated {
			n++
		}
	}
	if n != 4 {
		t.Fatalf("%d job.updated events, want 4", n)
	}
}
