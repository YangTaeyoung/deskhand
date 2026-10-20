package xfer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
)

// testManager returns a Manager over a new store, without connections.
func testManager(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "deskhand.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m := New(Options{Store: st, Bus: events.NewBus(st, nil)})
	t.Cleanup(m.Close)
	return m, st
}

func TestAReplanKeepsTheCommitAndPartOfUnfinishedFiles(t *testing.T) {
	m, st := testManager(t)
	root := t.TempDir()
	for name, data := range map[string]string{"done.txt": "done", "sent.txt": "sent", "half.txt": "half", "new.txt": "new"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	row := store.Transfer{TransferID: "t-replan", HostID: "pc", Direction: Push, Local: root, Remote: "/r", OnConflict: "fail", Sep: "/",
		State: StateRunning, Folder: true, CreatedAt: now, UpdatedAt: now}
	if err := st.CreateTransfer(row); err != nil {
		t.Fatal(err)
	}
	first := &transfer{m: m, row: row}
	files, err := first.planPush(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := range files {
		switch f := &files[i]; f.Rel {
		case "done.txt":
			f.State, f.Outcome, f.BytesDone = StateDone, OutcomeWritten, f.Size
		case "sent.txt":
			f.State, f.CommitRID, f.Part, f.BytesDone = StateQueued, "r-sent", true, f.Size
		case "half.txt":
			f.State, f.Part, f.BytesDone = StateQueued, true, 2
		}
	}
	files = append(files[:len(files):len(files)], store.TransferFile{TransferID: "t-replan", Seq: len(files), Rel: "gone", Dir: true, State: StateFailed})
	tr := newTransfer(m, row, files)
	if err := tr.plan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	got := map[string]store.TransferFile{}
	for _, f := range tr.files {
		got[f.Rel] = f
	}
	if f := got["done.txt"]; f.State != StateDone {
		t.Errorf("done.txt %+v", f)
	}
	if f := got["sent.txt"]; f.CommitRID != "r-sent" || !f.Part || f.State != StateQueued {
		t.Errorf("sent.txt lost its commit: %+v", f)
	}
	if f := got["half.txt"]; !f.Part || f.BytesDone != 2 {
		t.Errorf("half.txt lost its part: %+v", f)
	}
	if f := got["new.txt"]; f.Part || f.CommitRID != "" || f.State != StateQueued {
		t.Errorf("new.txt %+v", f)
	}
	if _, ok := got["gone"]; ok {
		t.Error("the folder that is gone stays in the manifest")
	}
}
