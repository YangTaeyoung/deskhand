package events

import (
	"path/filepath"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
)

func newBus(t *testing.T) (*Bus, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewBus(st, nil), st
}

func TestPublishOrdersAndFlattens(t *testing.T) {
	b, _ := newBus(t)
	sub := b.Subscribe()
	defer sub.Close()
	first := b.Publish("pc", "", ConnectionState, map[string]any{"state": "ready", "connection_generation": 1})
	second := b.Publish("", "", HostRemoved, map[string]any{})
	if second.Seq != first.Seq+1 {
		t.Fatalf("seq %d then %d", first.Seq, second.Seq)
	}
	p := (<-sub.C).Params()
	if p["seq"] != first.Seq || p["kind"] != ConnectionState || p["host_id"] != "pc" || p["state"] != "ready" || p["ts"] == "" {
		t.Fatalf("params %v", p)
	}
	if p := (<-sub.C).Params(); p["host_id"] != nil {
		t.Fatalf("params without a host %v", p)
	}
}

func TestSlowSubscriberDropsAndCounts(t *testing.T) {
	b, _ := newBus(t)
	sub := b.Subscribe()
	defer sub.Close()
	for i := 0; i < subBuffer+5; i++ {
		b.Publish("pc", "", "x", nil)
	}
	if n := sub.TakeDropped(); n != 5 {
		t.Fatalf("dropped %d", n)
	}
	if n := sub.TakeDropped(); n != 0 {
		t.Fatalf("dropped after taking %d", n)
	}
}

func TestRecordPublishesTimelineEntry(t *testing.T) {
	b, st := newBus(t)
	sub := b.Subscribe()
	defer sub.Close()
	c := model.Caller{Actor: model.ActorAI, ClientID: "c-1", ClientName: "claude"}
	entry, err := b.Record(c.Entry("pc", model.TimelineExec, "명령 실행: hostname", model.TimelineOK, "j-1"))
	if err != nil {
		t.Fatal(err)
	}
	ev := <-sub.C
	got := ev.Fields["entry"].(model.TimelineEntry)
	if ev.Kind != TimelineAppended || got.Seq != entry.Seq || got.Actor != "ai" || got.JobID != "j-1" || got.Artifacts == nil {
		t.Fatalf("event %+v", ev)
	}
	rows, err := st.ListTimeline(store.TimelineFilter{HostID: "pc"})
	if err != nil || len(rows) != 1 || rows[0].ClientName != "claude" {
		t.Fatalf("rows %+v, %v", rows, err)
	}
}
