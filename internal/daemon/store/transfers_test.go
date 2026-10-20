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
