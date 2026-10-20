package xfer

import (
	"errors"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
)

func TestLoadPrunesTransfersThatFinishedLongAgo(t *testing.T) {
	m, st := testManager(t)
	long := time.Now().Add(-keepFinished - time.Hour)
	recent := time.Now()
	for id, at := range map[string]time.Time{"t-long": long, "t-recent": recent} {
		if err := st.CreateTransfer(store.Transfer{TransferID: id, HostID: "pc", Direction: Push, Local: "/l", Remote: "/r", OnConflict: "fail",
			State: StateDone, CreatedAt: at, UpdatedAt: at, FinishedAt: &at}); err != nil {
			t.Fatal(err)
		}
	}
	m.Load()
	if _, _, err := st.GetTransfer("t-long"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a transfer that finished %s ago stays: %v", keepFinished, err)
	}
	if _, _, err := st.GetTransfer("t-recent"); err != nil {
		t.Fatalf("a recent transfer was pruned: %v", err)
	}
}
