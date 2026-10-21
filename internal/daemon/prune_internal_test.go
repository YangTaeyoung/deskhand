package daemon

import (
	"context"
	"errors"
	"io"
	"log"
	"path/filepath"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
)

func TestPruneEveryRemovesOldEventsOnEachTick(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go pruneEvery(ctx, st, log.New(io.Discard, "", 0), 10*time.Millisecond, done)
	defer func() {
		cancel()
		<-done
	}()
	old, err := st.AppendEvent(time.Now().Add(-eventKeep-time.Hour), "pc", "", "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	recent, err := st.AppendEvent(time.Now(), "pc", "", "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := st.GetEvent(old); errors.Is(err, store.ErrNotFound) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("an event older than %s was kept after the prune ticks", eventKeep)
		}
	}
	if _, err := st.GetEvent(recent); err != nil {
		t.Fatalf("a recent event was pruned: %v", err)
	}
}
