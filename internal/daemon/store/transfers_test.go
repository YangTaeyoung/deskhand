package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestTransfersRoundTripAndKeepTheirRevAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deskhand.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	tr := Transfer{TransferID: "t-1", HostID: "pc", Direction: "push", Local: "/Users/a/x", Remote: `C:\qa\x`, OnConflict: "fail", Sep: `\`,
		State: "queued", Rev: 1, Actor: "human", ClientID: "c-1", ClientName: "app", CreatedAt: now, UpdatedAt: now}
	if err := s.CreateTransfer(tr); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTransfer(tr); err == nil {
		t.Fatal("a second transfer with the same id was stored")
	}
	files := []TransferFile{
		{TransferID: "t-1", Seq: 0, Rel: "sub", Dir: true, State: "queued"},
		{TransferID: "t-1", Seq: 1, Rel: "sub/a.txt", Size: 5, MTime: "2026-10-20T01:02:03.123456789Z", SHA256: "ab", State: "queued"},
		{TransferID: "t-1", Seq: 2, Rel: "b.bin", Size: 9, State: "queued"},
	}
	tr.Planned, tr.Folder, tr.State, tr.Rev = true, true, "running", 2
	if err := s.SaveTransfer(tr, files, true); err != nil {
		t.Fatal(err)
	}
	files[2].BytesDone, files[2].State, files[2].Outcome, files[2].Path = 9, "done", "renamed", `C:\qa\x\b (1).bin`
	files[1].CommitRID, files[1].Part, files[1].Refresh = "r-9", true, true
	files[1].Error = json.RawMessage(`{"code":"TRANSFER_CONFLICT","message":"m","retryable":false}`)
	tr.Rev, tr.Outcome, tr.Error = 3, "partial", json.RawMessage(`{"code":"X","message":"y","retryable":false}`)
	fin := now.Add(time.Second)
	tr.FinishedAt = &fin
	if err := s.SaveTransfer(tr, files[1:], false); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, gotFiles, err := s.GetTransfer("t-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Rev != 3 || got.State != "running" || !got.Planned || !got.Folder || got.Outcome != "partial" || got.Sep != `\` ||
		got.FinishedAt == nil || !got.FinishedAt.Equal(fin) || string(got.Error) != string(tr.Error) || got.ClientName != "app" {
		t.Fatalf("transfer after reopening %+v", got)
	}
	if !reflect.DeepEqual(gotFiles, files) {
		t.Fatalf("files after reopening\n%+v\nwant\n%+v", gotFiles, files)
	}
	if ids, err := s.TransfersWithParts("pc", "running"); err != nil || len(ids) != 1 || ids[0] != "t-1" {
		t.Fatalf("transfers with part files %v, %v", ids, err)
	}
	if ids, err := s.TransfersWithParts("other", "running"); err != nil || len(ids) != 0 {
		t.Fatalf("transfers of another host with part files %v, %v", ids, err)
	}
	if err := s.SaveTransfer(tr, files[:1], true); err != nil {
		t.Fatal(err)
	}
	if _, left, _ := s.GetTransfer("t-1"); len(left) != 1 {
		t.Fatalf("replacing the files left %d rows", len(left))
	}
	if _, _, err := s.GetTransfer("t-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing transfer: %v", err)
	}
}

func TestListTransfersNewestFirstByHost(t *testing.T) {
	s := openTemp(t)
	base := time.Now().UTC()
	for i, h := range []string{"pc", "other", "pc"} {
		at := base.Add(time.Duration(i) * time.Second)
		if err := s.CreateTransfer(Transfer{TransferID: "t-" + string(rune('a'+i)), HostID: h, Direction: "pull", Local: "/l", Remote: "/r",
			OnConflict: "fail", State: "done", CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.ListTransfers(TransferFilter{})
	if err != nil || len(all) != 3 || all[0].TransferID != "t-c" || all[2].TransferID != "t-a" {
		t.Fatalf("all transfers %+v, %v", all, err)
	}
	pc, err := s.ListTransfers(TransferFilter{HostID: "pc"})
	if err != nil || len(pc) != 2 || pc[0].TransferID != "t-c" || pc[1].TransferID != "t-a" {
		t.Fatalf("transfers of pc %+v, %v", pc, err)
	}
	open, err := s.ListTransfers(TransferFilter{States: []string{"queued", "running"}})
	if err != nil || len(open) != 0 {
		t.Fatalf("unfinished transfers %+v, %v", open, err)
	}
	if first, err := s.ListTransfers(TransferFilter{Limit: 1}); err != nil || len(first) != 1 {
		t.Fatalf("one transfer %+v, %v", first, err)
	}
	if every, err := s.ListTransfers(TransferFilter{Limit: -1, States: []string{"done"}}); err != nil || len(every) != 3 {
		t.Fatalf("every done transfer %+v, %v", every, err)
	}
}

func TestTransferTotalsAndPruneOfFinishedTransfers(t *testing.T) {
	s := openTemp(t)
	old := time.Now().Add(-40 * 24 * time.Hour)
	for i, tc := range []struct {
		id, state string
		finished  *time.Time
		part      bool
	}{
		{"t-old", "done", &old, false},
		{"t-old-part", "cancelled", &old, true},
		{"t-recent", "failed", nil, false},
		{"t-paused", "paused", nil, false},
	} {
		at := old.Add(time.Duration(i) * time.Second)
		job := Job{JobID: "j-" + tc.id, HostID: "pc", Kind: "transfer", Status: "succeeded", CreatedAt: at, UpdatedAt: at, FinishedAt: tc.finished}
		if err := s.CreateJob(job); err != nil {
			t.Fatal(err)
		}
		tr := Transfer{TransferID: tc.id, HostID: "pc", Direction: "push", Local: "/l", Remote: "/r", OnConflict: "fail", State: tc.state,
			JobID: job.JobID, CreatedAt: at, UpdatedAt: at, FinishedAt: tc.finished}
		if tc.finished == nil && tc.state == "failed" {
			now := time.Now()
			tr.FinishedAt = &now
		}
		if err := s.CreateTransfer(tr); err != nil {
			t.Fatal(err)
		}
		files := []TransferFile{
			{Seq: 0, Rel: "d", Dir: true, State: "done"},
			{Seq: 1, Rel: "d/a", Size: 10, BytesDone: 10, State: "done"},
			{Seq: 2, Rel: "d/b", Size: 5, BytesDone: 2, State: "failed", Part: tc.part},
			{Seq: 3, Rel: "c", Size: 7, State: "queued"},
		}
		if err := s.SaveTransfer(tr, files, true); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.TransferTotals([]string{"t-old", "t-recent", "t-none"})
	if err != nil {
		t.Fatal(err)
	}
	if want := (TransferTotal{Files: 3, Done: 1, Failed: 1, Bytes: 22, BytesDone: 12}); got["t-old"] != want || got["t-recent"] != want || len(got) != 2 {
		t.Fatalf("totals %+v", got)
	}
	if tr, _, err := s.GetTransfer("t-old"); err != nil || tr.JobID != "j-t-old" {
		t.Fatalf("job of a transfer %+v, %v", tr, err)
	}
	n, err := s.PruneTransfers(time.Now().Add(-30 * 24 * time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	if _, _, err := s.GetTransfer("t-old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the old transfer stays: %v", err)
	}
	if _, err := s.GetJob("j-t-old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the job of the old transfer stays: %v", err)
	}
	if files, _ := s.TransferFiles("t-old"); len(files) != 0 {
		t.Fatalf("the files of the old transfer stay: %v", files)
	}
	for _, id := range []string{"t-old-part", "t-recent", "t-paused"} {
		if _, _, err := s.GetTransfer(id); err != nil {
			t.Fatalf("%s was pruned: %v", id, err)
		}
	}
}

func TestSetJobStateReopensAFinishedJob(t *testing.T) {
	s := openTemp(t)
	now := time.Now()
	if err := s.CreateJob(Job{JobID: "j-1", HostID: "pc", Kind: "transfer", Status: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobState("j-1", "failed", json.RawMessage(`{"state":"failed"}`), true, now); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.GetJob("j-1"); j.Status != "failed" || j.FinishedAt == nil {
		t.Fatalf("finished job %+v", j)
	}
	if err := s.SetJobState("j-1", "running", json.RawMessage(`{"state":"queued"}`), false, now); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.GetJob("j-1"); j.Status != "running" || j.FinishedAt != nil || string(j.Result) != `{"state":"queued"}` {
		t.Fatalf("reopened job %+v", j)
	}
	if err := s.SetJobState("j-missing", "running", nil, false, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing job: %v", err)
	}
}

func TestOpenAddsTheJobColumnToAnOlderTransfersTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deskhand.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE transfers DROP COLUMN job_id`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	have := false
	for _, c := range columns(t, s, "transfers") {
		have = have || c == "job_id"
	}
	if !have {
		t.Fatalf("transfers has no job_id after opening: %v", columns(t, s, "transfers"))
	}
}
