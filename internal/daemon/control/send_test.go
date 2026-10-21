package control

import (
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestSendRunsCheckWithTheStreamConnectionBeforeAnyEvent(t *testing.T) {
	r := newRig(t, time.Minute)
	stale := apierr.New(wire.CodeStaleTarget, "the capture is of another connection", false, "screen_capture")
	var seen []uint64
	_, err := r.m.Send(bg, "pc", aiA, []wire.InputEvent{{K: "move", X: 1, Y: 2}}, SendOpts{Check: func(connGen uint64) error {
		seen = append(seen, connGen)
		return stale
	}})
	if apierr.CodeOf(err) != wire.CodeStaleTarget || len(seen) != 1 || seen[0] != r.agent.Generation() {
		t.Fatalf("Send %v, check saw %v (agent connection %d)", err, seen, r.agent.Generation())
	}
	if _, evs, _, _ := r.agent.stream(0); len(evs) != 0 {
		t.Fatalf("events reached the agent after the check refused: %+v", evs)
	}

	acks, err := r.m.Send(bg, "pc", aiA, []wire.InputEvent{{K: "move", X: 1, Y: 2}}, SendOpts{Check: func(connGen uint64) error {
		seen = append(seen, connGen)
		return nil
	}})
	if err != nil || len(acks) != 1 || !acks[0].OK || len(seen) != 2 {
		t.Fatalf("Send %+v, %v", acks, err)
	}
	if _, evs, _, _ := r.agent.stream(0); len(evs) != 1 || evs[0].K != "move" || evs[0].Gen != r.m.Get("pc").Gen {
		t.Fatalf("events %+v", evs)
	}
	if s := r.m.Get("pc"); s.Owner != "ai" || s.OwnerClientID != aiA.ClientID {
		t.Fatalf("an AI send holds no lease: %+v", s)
	}
}

func TestSendFromAPersonTakesFreeControlAndYieldsToAnAILease(t *testing.T) {
	r := newRig(t, time.Minute)
	acks, err := r.m.Send(bg, "pc", human, []wire.InputEvent{{K: "key", SC: 0x1c, Down: true}, {K: "key", SC: 0x1c}}, SendOpts{})
	if err != nil || len(acks) != 2 {
		t.Fatalf("Send of a person %+v, %v", acks, err)
	}
	s := r.m.Get("pc")
	if s.Owner != "human" || s.OwnerClientID != human.ClientID {
		t.Fatalf("state after a person's send %+v", s)
	}
	if hdr, evs, _, _ := r.agent.stream(0); hdr != s.Gen || len(evs) != 2 || evs[0].Gen != s.Gen {
		t.Fatalf("header gen %d events %+v, want gen %d", hdr, evs, s.Gen)
	}
	if _, err := r.m.Send(bg, "pc", human2, []wire.InputEvent{{K: "wheel", DY: 120}}, SendOpts{}); err != nil {
		t.Fatalf("another person while a person holds: %v", err)
	}
	if _, err := r.m.Release(bg, "pc", human); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.CheckAI("pc", aiA); err != nil {
		t.Fatal(err)
	}
	streams := r.agent.streamCount()
	_, err = r.m.Send(bg, "pc", human, []wire.InputEvent{{K: "move", X: 3, Y: 4}}, SendOpts{})
	if apierr.CodeOf(err) != wire.CodeControlPreempted {
		t.Fatalf("a person's send while an AI holds the lease: %v", err)
	}
	if r.agent.streamCount() != streams {
		t.Fatal("a refused send reached the agent")
	}
	if s := r.m.Get("pc"); s.Owner != "ai" {
		t.Fatalf("a refused send changed the state %+v", s)
	}
}
