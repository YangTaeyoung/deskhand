package xfer_test

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/xfer"
)

// jobOf returns the job jobID as stored and its result.
func (h *harness) jobOf(jobID string) (string, map[string]any, bool) {
	h.t.Helper()
	h.mu.Lock()
	d := h.d
	h.mu.Unlock()
	j, err := d.st.GetJob(jobID)
	if err != nil {
		h.t.Fatal(err)
	}
	res := map[string]any{}
	if len(j.Result) > 0 {
		_ = json.Unmarshal(j.Result, &res)
	}
	return j.Status, res, j.FinishedAt != nil
}

func TestATransferIsMirroredByAJob(t *testing.T) {
	g := newGate()
	h := newHarness(t, xfer.Hooks{Wrote: func(_, rel string, done int64) {
		if rel == "j.bin" && done >= 4*mib {
			g.wait()
		}
	}})
	t.Cleanup(g.release)
	src, dst := filepath.Join(h.mac, "j.bin"), filepath.Join(h.agent.home, "j.bin")
	writeFile(t, src, randomBytes(t, 12*mib))
	ctx := t.Context()
	res, err := h.manager().Start(ctx, xfer.StartParams{HostID: "pc", Direction: "push", Local: src, Remote: dst}, human)
	if err != nil || res.Err != nil {
		t.Fatalf("start: %+v, %v", res, err)
	}
	id, job := res.Transfer.TransferID, res.JobID
	if status, result, fin := h.jobOf(job); status != "running" || fin || result["transfer_id"] != nil && result["transfer_id"] != id {
		t.Fatalf("job of a started transfer: %s %v %v", status, result, fin)
	}
	<-g.reached
	paused, err := within(t, 10*time.Second, "pause", func() (model.TransferDetail, error) {
		go g.release()
		return h.manager().Pause(id, human)
	})
	if err != nil || paused.State != "paused" {
		t.Fatalf("pause: %s, %v", describe(paused), err)
	}
	if status, result, fin := h.jobOf(job); status != "running" || fin || result["paused"] != true || result["state"] != "paused" {
		t.Fatalf("job of a paused transfer: %s %v %v", status, result, fin)
	}
	if _, err := h.manager().Resume(id, human); err != nil {
		t.Fatal(err)
	}
	checkDone(t, h.waitFinished(id), "written")
	if status, result, fin := h.jobOf(job); status != "succeeded" || !fin || result["outcome"] != "written" || result["transfer_id"] != id {
		t.Fatalf("job of a done transfer: %s %v %v", status, result, fin)
	}

	back := filepath.Join(h.mac, "exists.bin")
	writeFile(t, back, []byte("here"))
	res, err = h.manager().Start(ctx, xfer.StartParams{HostID: "pc", Direction: "pull", Local: back, Remote: dst}, human)
	if err != nil || res.Err != nil {
		t.Fatalf("start: %+v, %v", res, err)
	}
	h.waitFinished(res.Transfer.TransferID)
	if status, result, fin := h.jobOf(res.JobID); status != "failed" || !fin || result["error"] == nil {
		t.Fatalf("job of a failed transfer: %s %v %v", status, result, fin)
	}
	if tr, err := h.manager().Cancel(res.Transfer.TransferID, human); err != nil || tr.State != "cancelled" {
		t.Fatalf("cancel of a failed transfer: %s, %v", describe(tr), err)
	}
	if status, _, fin := h.jobOf(res.JobID); status != "cancelled" || !fin {
		t.Fatalf("job of a cancelled transfer: %s %v", status, fin)
	}
}

func TestEventsAndListCarryCountsAndOnlyChangedFiles(t *testing.T) {
	h := newHarness(t, xfer.Hooks{})
	src := filepath.Join(h.mac, "three")
	for _, name := range []string{"a.txt", "b.txt", "c/d.txt"} {
		writeFile(t, filepath.Join(src, name), []byte(name))
	}
	id := h.start("push", src, filepath.Join(h.agent.home, "three"), "").TransferID
	checkDone(t, h.waitFinished(id), "written")
	done := map[string]bool{}
	h.mu.Lock()
	for _, ev := range h.events {
		if ev.Kind != events.TransferUpdated || ev.Fields["transfer"].(model.Transfer).TransferID != id {
			continue
		}
		b, _ := json.Marshal(ev.Params())
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if _, ok := m["transfer"].(map[string]any)["files"]; ok {
			t.Errorf("transfer.updated carries the files: %s", b)
		}
		if changed, ok := ev.Fields["changed_files"].([]model.TransferFile); ok {
			for _, f := range changed {
				if f.State == "done" {
					done[f.Rel] = true
				}
			}
		}
	}
	h.mu.Unlock()
	if len(done) != 3 || !done["a.txt"] || !done["c/d.txt"] {
		t.Fatalf("changed_files reported done files %v", done)
	}
	list, err := h.manager().List("pc")
	if err != nil || len(list) != 1 {
		t.Fatalf("list %+v, %v", list, err)
	}
	if got := list[0]; got.FilesTotal != 3 || got.FilesDone != 3 || got.FilesFailed != 0 || got.BytesDone != got.BytesTotal || got.BytesTotal != 17 {
		t.Fatalf("listed transfer %+v", got)
	}
	b, _ := json.Marshal(list)
	var raw []map[string]any
	_ = json.Unmarshal(b, &raw)
	if _, ok := raw[0]["files"]; ok {
		t.Fatalf("transfer.list carries the files: %s", b)
	}
	if detail := h.get(id); len(detail.Files) != 3 {
		t.Fatalf("transfer.get has %d files", len(detail.Files))
	}
}
