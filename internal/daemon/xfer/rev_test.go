package xfer

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
)

func TestATransferWhoseChangeIsNotStoredStaysLiveAndItsRevKeepsRising(t *testing.T) {
	m, st := testManager(t)
	sub := m.opt.Bus.Subscribe()
	defer sub.Close()
	var failing atomic.Bool
	m.save = func(tr store.Transfer, files []store.TransferFile, replace bool) error {
		if failing.Load() {
			return errors.New("disk I/O error")
		}
		return st.SaveTransfer(tr, files, replace)
	}
	now := time.Now()
	row := store.Transfer{TransferID: "t-rev", HostID: "pc", Direction: Push, Local: "/l", Remote: "/r", OnConflict: "fail", Sep: "/",
		State: StateQueued, Planned: true, Rev: 4, CreatedAt: now, UpdatedAt: now}
	if err := st.CreateTransfer(row); err != nil {
		t.Fatal(err)
	}
	caller := model.Caller{Actor: model.ActorHuman}
	failing.Store(true)
	if tr, err := m.Pause("t-rev", caller); err != nil || tr.State != StatePaused {
		t.Fatalf("pause: %+v, %v", tr, err)
	}
	m.mu.Lock()
	_, live := m.live["t-rev"]
	m.mu.Unlock()
	if !live {
		t.Fatal("a transfer whose change is not stored stopped being live")
	}
	failing.Store(false)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		stored, _, err := st.GetTransfer("t-rev")
		if err == nil && stored.State == StatePaused {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the paused state was not stored: %+v, %v", stored, err)
		}
	}
	if tr, err := m.Cancel("t-rev", caller); err != nil || tr.State != StateCancelled {
		t.Fatalf("cancel: %+v, %v", tr, err)
	}
	stored, _, err := st.GetTransfer("t-rev")
	if err != nil || stored.State != StateCancelled {
		t.Fatalf("stored %+v, %v", stored, err)
	}
	last := row.Rev
	for {
		select {
		case ev := <-sub.C:
			if ev.Kind != events.TransferUpdated {
				continue
			}
			rev := ev.Fields["transfer"].(model.Transfer).Rev
			if rev <= last {
				t.Fatalf("transfer.updated rev %d after %d", rev, last)
			}
			last = rev
			continue
		default:
		}
		break
	}
	if last != stored.Rev {
		t.Fatalf("the last event has rev %d, the store %d", last, stored.Rev)
	}
}
