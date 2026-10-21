package control

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

var bg = context.Background()

func TestGenIncrementsOnEveryChange(t *testing.T) {
	r := newRig(t, time.Minute)
	m := r.m
	steps := []struct {
		name string
		do   func() (uint64, error)
		gen  uint64
	}{
		{"human acquires", func() (uint64, error) { s, err := m.Acquire(bg, "pc", OwnerHuman, human, false); return s.Gen, err }, 1},
		{"another person acquires while a person holds", func() (uint64, error) {
			s, err := m.Acquire(bg, "pc", OwnerHuman, human2, false)
			return s.Gen, err
		}, 1},
		{"human releases", func() (uint64, error) { s, err := m.Release(bg, "pc", human); return s.Gen, err }, 2},
		{"release without an owner", func() (uint64, error) { s, err := m.Release(bg, "pc", human); return s.Gen, err }, 2},
		{"AI input takes control", func() (uint64, error) { return m.CheckAI("pc", aiA) }, 3},
		{"AI input renews the lease", func() (uint64, error) { return m.CheckAI("pc", aiA) }, 3},
		{"AI acquires what it holds", func() (uint64, error) { s, err := m.Acquire(bg, "pc", OwnerAI, aiA, false); return s.Gen, err }, 3},
		{"emergency stop", func() (uint64, error) { s, err := m.Stop(bg, "pc", human); return s.Gen, err }, 4},
		{"emergency stop again", func() (uint64, error) { s, err := m.Stop(bg, "pc", human); return s.Gen, err }, 4},
		{"human releases while AI is paused", func() (uint64, error) { s, err := m.Release(bg, "pc", human); return s.Gen, err }, 5},
		{"resume AI", func() (uint64, error) { s, err := m.ResumeAI(bg, "pc", human); return s.Gen, err }, 6},
		{"AI acquires", func() (uint64, error) { s, err := m.Acquire(bg, "pc", OwnerAI, aiA, false); return s.Gen, err }, 7},
		{"human takes over with force", func() (uint64, error) {
			s, err := m.Acquire(bg, "pc", OwnerHuman, human, true)
			return s.Gen, err
		}, 8},
	}
	for _, st := range steps {
		gen, err := st.do()
		if err != nil || gen != st.gen {
			t.Fatalf("%s: gen %d, %v; want %d", st.name, gen, err, st.gen)
		}
		if got := m.Get("pc").Gen; got != st.gen {
			t.Fatalf("%s: Get gen %d, want %d", st.name, got, st.gen)
		}
	}
	if _, err := m.CheckAI("pc", aiA); err == nil {
		t.Fatal("AI input passed while a person holds control")
	}
	var gens []uint64
	for _, c := range r.changes() {
		gens = append(gens, c.Gen)
	}
	if want := []uint64{1, 2, 3, 4, 5, 6, 7, 8}; !slices.Equal(gens, want) {
		t.Fatalf("control.changed gens %v, want %v", gens, want)
	}
	row, err := r.st.GetControl("pc")
	if err != nil || row.Gen != 8 || row.Owner != "human" || row.OwnerClient != human.ClientID {
		t.Fatalf("stored %+v, %v", row, err)
	}
	eventually(t, "the agent's writer generation 8", func() bool { return r.agent.writerGen() == 8 })
	tl, err := r.st.ListTimeline(store.TimelineFilter{HostID: "pc", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	control := 0
	for _, e := range tl {
		if e.Kind == model.TimelineControl && e.Outcome == model.TimelineOK {
			control++
		}
	}
	if control != 8 {
		t.Fatalf("%d control timeline entries, want 8: %+v", control, tl)
	}

	r.close()
	r.open()
	if s := r.m.Get("pc"); s.Gen != 8 || s.Owner != "human" {
		t.Fatalf("state after the restart %+v", s)
	}
	if s, err := r.m.Release(bg, "pc", human); err != nil || s.Gen != 9 {
		t.Fatalf("first change after the restart: %+v, %v", s, err)
	}
}

func TestAILeaseExpires(t *testing.T) {
	r := newRig(t, 300*time.Millisecond)
	gen, err := r.m.CheckAI("pc", aiA)
	if err != nil || gen != 1 {
		t.Fatalf("CheckAI %d, %v", gen, err)
	}
	if c, ok := r.nextChange(time.Second); !ok || c.Owner != "ai" || c.OwnerClientID != aiA.ClientID || c.OwnerClient != aiA.ClientName {
		t.Fatalf("change %+v %v", c, ok)
	}
	for range 4 {
		time.Sleep(150 * time.Millisecond)
		if gen, err := r.m.CheckAI("pc", aiA); err != nil || gen != 1 {
			t.Fatalf("renewal: %d, %v", gen, err)
		}
	}
	if s := r.m.Get("pc"); s.Owner != "ai" || s.Gen != 1 {
		t.Fatalf("a renewed lease ended: %+v", s)
	}
	c, ok := r.nextChange(2 * time.Second)
	if !ok || c.Owner != "none" || c.Gen != 2 || c.OwnerClientID != "" {
		t.Fatalf("expiry %+v %v", c, ok)
	}
	if s := r.m.Get("pc"); s.Owner != "none" || s.Gen != 2 {
		t.Fatalf("state after the expiry %+v", s)
	}
	eventually(t, "writer generation 2 at the agent", func() bool { return r.agent.writerGen() == 2 })
	tl, _ := r.st.ListTimeline(store.TimelineFilter{HostID: "pc", Limit: 1})
	if len(tl) != 1 || tl[0].Actor != model.ActorSystem || !strings.Contains(tl[0].Summary, "만료") {
		t.Fatalf("timeline %+v", tl)
	}
	if gen, err := r.m.CheckAI("pc", aiB); err != nil || gen != 3 {
		t.Fatalf("another AI after the expiry: %d, %v", gen, err)
	}
}

func TestHumanTakeoverCutsInFlightAIInput(t *testing.T) {
	r := newRig(t, time.Minute)
	if acks, err := r.m.SendAI(bg, "pc", aiA, []wire.InputEvent{{K: "key", SC: 0x2a, Down: true}}); err != nil || len(acks) != 1 || !acks[0].OK {
		t.Fatalf("shift down: %+v, %v", acks, err)
	}
	if held := r.agent.heldKeys(); len(held) != 1 {
		t.Fatalf("held %v", held)
	}
	long := strings.Repeat("가나다라마바사아자차", 200)
	type sent struct {
		acks []wire.InputAck
		err  error
	}
	done := make(chan sent, 1)
	go func() {
		acks, err := r.m.SendAI(bg, "pc", aiA, []wire.InputEvent{{K: "text", Text: long, Method: "unicode"}})
		done <- sent{acks, err}
	}()
	eventually(t, "the text to start", func() bool {
		_, _, typed, _ := r.agent.stream(0)
		return len([]rune(typed)) >= 10
	})
	st, err := r.m.Acquire(bg, "pc", OwnerHuman, human, true)
	if err != nil || st.Owner != "human" {
		t.Fatalf("takeover: %+v, %v", st, err)
	}
	var res sent
	select {
	case res = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the AI text did not stop")
	}
	if code, _ := codeOf(res.err); code != wire.CodeControlPreempted || len(res.acks) != 1 || res.acks[0].OK {
		t.Fatalf("the cut text returned %+v, %v", res.acks, res.err)
	}
	eventually(t, "the agent to apply the release_all of the cut", func() bool {
		_, _, _, releases := r.agent.stream(0)
		return len(releases) > 0
	})
	_, evs, typed, releases := r.agent.stream(0)
	if n := len([]rune(typed)); n >= len([]rune(long)) {
		t.Fatalf("the whole text was typed (%d runes)", n)
	}
	if !slices.Equal(releases, []uint64{st.Gen}) {
		t.Fatalf("release_all at gens %v, want [%d]", releases, st.Gen)
	}
	if last := evs[len(evs)-1]; last.K != "release_all" || last.Gen != st.Gen {
		t.Fatalf("the last event on the stream %+v", last)
	}
	if held := r.agent.heldKeys(); len(held) != 0 {
		t.Fatalf("keys still held: %v", held)
	}
	if r.agent.writerGen() != st.Gen || !slices.Contains(r.agent.pushes(), st.Gen) {
		t.Fatalf("agent writer %d, set_writer %v, want %d", r.agent.writerGen(), r.agent.pushes(), st.Gen)
	}
	typedAtCut := typed
	time.Sleep(50 * time.Millisecond)
	if _, _, typed, _ := r.agent.stream(0); typed != typedAtCut {
		t.Fatal("typing went on after the cut")
	}

	streams := r.agent.streamCount()
	_, err = r.m.SendAI(bg, "pc", aiA, []wire.InputEvent{{K: "text", Text: "more"}})
	if code, next := codeOf(err); code != wire.CodeControlHeldByHuman || next != apierr.NextHumanRelease {
		t.Fatalf("AI input after the takeover: %v", err)
	}
	if r.agent.streamCount() != streams {
		t.Fatal("AI input after the takeover reached the agent")
	}
	if _, evs2, _, _ := r.agent.stream(0); len(evs2) != len(evs) {
		t.Fatal("AI input after the takeover went out on the old stream")
	}
}

func TestSecondAIClientRejectedWhileFirstHoldsLease(t *testing.T) {
	r := newRig(t, time.Minute)
	if _, err := r.m.SendAI(bg, "pc", aiA, []wire.InputEvent{{K: "move", X: 1, Y: 1}}); err != nil {
		t.Fatal(err)
	}
	before := r.m.Get("pc")
	_, err := r.m.SendAI(bg, "pc", aiB, []wire.InputEvent{{K: "move", X: 9, Y: 9}})
	code, next := codeOf(err)
	var e *apierr.Error
	if code != wire.CodeControlPreempted || next != apierr.NextRetry {
		t.Fatalf("second AI input: %v", err)
	}
	if e = apierrOf(err); !e.Retryable || !strings.Contains(e.Message, aiA.ClientName) {
		t.Fatalf("second AI error %+v", e)
	}
	if _, err := r.m.Acquire(bg, "pc", OwnerAI, aiB, false); err == nil {
		t.Fatal("the second AI acquired control")
	}
	if _, err := r.m.Release(bg, "pc", aiB); err == nil {
		t.Fatal("the second AI released the first AI's control")
	}
	if after := r.m.Get("pc"); after != before {
		t.Fatalf("state changed from %+v to %+v", before, after)
	}
	_, evs, _, _ := r.agent.stream(0)
	if len(evs) != 1 || evs[0].X != 1 || r.agent.streamCount() != 1 {
		t.Fatalf("the second AI's input reached the agent: %+v", evs)
	}
	if _, err := r.m.Acquire(bg, "pc", OwnerAI, aiA, false); err != nil {
		t.Fatalf("the first AI renewing: %v", err)
	}
	if _, err := r.m.Release(bg, "pc", aiA); err != nil {
		t.Fatal(err)
	}
	gen, err := r.m.CheckAI("pc", aiB)
	if err != nil || gen != before.Gen+2 {
		t.Fatalf("second AI after the release: %d, %v", gen, err)
	}
}

func apierrOf(err error) *apierr.Error { return apierr.As(err) }

func TestAIInputCarriesTheWriterGen(t *testing.T) {
	r := newRig(t, time.Minute)
	if _, err := r.m.Acquire(bg, "pc", OwnerHuman, human, false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.Release(bg, "pc", human); err != nil {
		t.Fatal(err)
	}
	acks, err := r.m.SendAI(bg, "pc", aiA, []wire.InputEvent{{K: "move", X: 5, Y: 6}, {K: "button", B: "left", Down: true}, {K: "button", B: "left"}})
	if err != nil || len(acks) != 3 {
		t.Fatalf("SendAI %+v, %v", acks, err)
	}
	hdr, evs, _, _ := r.agent.stream(0)
	if hdr != 3 || len(evs) != 3 {
		t.Fatalf("header gen %d, events %+v", hdr, evs)
	}
	for i, ev := range evs {
		if ev.Gen != 3 || ev.Seq != acks[i].Seq || ev.Seq == 0 {
			t.Fatalf("event %d %+v, ack %+v", i, ev, acks[i])
		}
	}
	if _, err := r.m.SendAI(bg, "pc", aiA, []wire.InputEvent{{K: "wheel", DY: 120}}); err != nil {
		t.Fatal(err)
	}
	if r.agent.streamCount() != 1 {
		t.Fatalf("a second send of the same writer opened %d streams", r.agent.streamCount())
	}
	if _, err := r.m.Stop(bg, "pc", human); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.ResumeAI(bg, "pc", human); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.SendAI(bg, "pc", aiA, []wire.InputEvent{{K: "move", X: 7, Y: 8}}); err != nil {
		t.Fatal(err)
	}
	hdr, evs, _, _ = r.agent.stream(1)
	if hdr != 6 || len(evs) != 1 || evs[0].Gen != 6 {
		t.Fatalf("after the new writer: header %d events %+v", hdr, evs)
	}
	if gen, err := r.m.WriterGen("pc", aiA); err != nil || gen != 6 {
		t.Fatalf("WriterGen of the AI owner %d, %v", gen, err)
	}
	if gen, err := r.m.WriterGen("pc", human); err != nil || gen != 6 || r.m.HumanGen("pc") != 6 {
		t.Fatalf("WriterGen of a person %d, %v", gen, err)
	}
	if _, err := r.m.Stop(bg, "pc", human); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.WriterGen("pc", aiA); err == nil {
		t.Fatal("WriterGen of an AI passed the emergency stop")
	}
	if gen, err := r.m.WriterGen("pc", human); err != nil || gen != 7 {
		t.Fatalf("WriterGen of a person during the stop %d, %v", gen, err)
	}
}

func TestRequestsTheStateRefuses(t *testing.T) {
	r := newRig(t, time.Minute)
	if _, err := r.m.Acquire(bg, "pc", OwnerHuman, aiA, true); err == nil || apierrOf(err).Code != wire.CodePermissionDenied {
		t.Fatalf("AI acquiring as a person: %v", err)
	}
	if _, err := r.m.Acquire(bg, "pc", OwnerAI, aiA, true); err == nil || apierrOf(err).Code != wire.CodeInvalidArgument {
		t.Fatalf("AI acquiring with force: %v", err)
	}
	if _, err := r.m.Acquire(bg, "pc", Owner("robot"), human, false); err == nil || apierrOf(err).Code != wire.CodeInvalidArgument {
		t.Fatalf("unknown owner: %v", err)
	}
	if _, err := r.m.Acquire(bg, "pc", OwnerAI, aiA, false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.Acquire(bg, "pc", OwnerHuman, human, false); err == nil || apierrOf(err).Code != wire.CodeInvalidArgument ||
		!strings.Contains(apierrOf(err).Message, "force") {
		t.Fatalf("a person without force while an AI holds: %v", err)
	}
	if _, err := r.m.Acquire(bg, "pc", OwnerHuman, human, true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.Release(bg, "pc", aiA); err == nil || apierrOf(err).Code != wire.CodeControlHeldByHuman {
		t.Fatalf("AI releasing a person's control: %v", err)
	}
	if _, err := r.m.Acquire(bg, "pc", OwnerAI, aiA, false); err == nil || apierrOf(err).NextAction != apierr.NextHumanRelease {
		t.Fatalf("AI acquiring a person's control: %v", err)
	}
	if _, err := r.m.Stop(bg, "pc", human); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.ResumeAI(bg, "pc", aiA); err == nil || apierrOf(err).Code != wire.CodePermissionDenied {
		t.Fatalf("AI resuming itself: %v", err)
	}
	_, err := r.m.Acquire(bg, "pc", OwnerAI, aiA, false)
	if code, next := codeOf(err); code != wire.CodeControlHeldByHuman || next != apierr.NextHumanResumeAI {
		t.Fatalf("AI acquiring while paused: %v", err)
	}
	if _, err := r.m.CheckHuman("pc", human2); err != nil {
		t.Fatalf("a person's input while a person holds: %v", err)
	}
	if s := r.m.Get("pc"); s.Owner != "human" || !s.AIPaused || s.OwnerClientID != human.ClientID {
		t.Fatalf("state %+v", s)
	}
	tl, _ := r.st.ListTimeline(store.TimelineFilter{HostID: "pc", Limit: 100})
	denied := 0
	for _, e := range tl {
		if e.Outcome == model.TimelineDenied {
			denied++
		}
	}
	if denied != 3 {
		t.Fatalf("%d denied entries, want 3", denied)
	}
}

func TestHumanInputTakesFreeControlAndYieldsToAnAILease(t *testing.T) {
	r := newRig(t, time.Minute)
	gen, err := r.m.CheckHuman("pc", human)
	if err != nil || gen != 1 || r.m.Get("pc").Owner != "human" {
		t.Fatalf("human input on a free host: %d, %v", gen, err)
	}
	if _, err := r.m.Release(bg, "pc", human); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.CheckAI("pc", aiA); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.CheckHuman("pc", human); err == nil || apierrOf(err).Code != wire.CodeControlPreempted {
		t.Fatalf("human input while an AI holds: %v", err)
	}
	if s := r.m.Get("pc"); s.Owner != "ai" {
		t.Fatalf("state %+v", s)
	}
}

func TestClientClosedReleasesItsAILease(t *testing.T) {
	r := newRig(t, time.Minute)
	if _, err := r.m.CheckAI("pc", aiA); err != nil {
		t.Fatal(err)
	}
	r.m.ClientClosed(aiB.ClientID)
	if s := r.m.Get("pc"); s.Owner != "ai" {
		t.Fatalf("another client's close released the lease: %+v", s)
	}
	r.m.ClientClosed(aiA.ClientID)
	if s := r.m.Get("pc"); s.Owner != "none" || s.Gen != 2 {
		t.Fatalf("state after the owner closed %+v", s)
	}
	if _, err := r.m.Acquire(bg, "pc", OwnerHuman, human, false); err != nil {
		t.Fatal(err)
	}
	r.m.ClientClosed(human.ClientID)
	if s := r.m.Get("pc"); s.Owner != "human" {
		t.Fatalf("a person's control ended with their connection: %+v", s)
	}
}

func TestRestartKeepsTheStateAndReleasesAnAILease(t *testing.T) {
	r := newRig(t, time.Minute)
	if _, err := r.m.Stop(bg, "pc", human); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.ResumeAI(bg, "pc", human); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.CheckAI("pc", aiA); err != nil {
		t.Fatal(err)
	}
	r.close()
	r.open()
	if s := r.m.Get("pc"); s.Owner != "none" || s.Gen != 4 || s.AIPaused {
		t.Fatalf("state after the restart %+v", s)
	}
	if gen, err := r.m.CheckAI("pc", aiB); err != nil || gen != 5 {
		t.Fatalf("a new AI client after the restart: %d, %v", gen, err)
	}
}

func TestPreparedConnectionGetsTheWriterGenAndAHigherOneIsTaken(t *testing.T) {
	r := newRig(t, time.Minute)
	r.agent.setState(conn.StateReconnecting)
	st, err := r.m.Stop(bg, "pc", human)
	if err != nil || st.Gen != 1 || !st.AIPaused {
		t.Fatalf("stop while offline: %+v, %v", st, err)
	}
	if p := r.agent.pushes(); len(p) != 0 {
		t.Fatalf("set_writer went to an offline agent: %v", p)
	}
	r.agent.restart()
	if err := r.m.PrepareConn(bg, "pc", r.agent.call); err != nil {
		t.Fatal(err)
	}
	if r.agent.writerGen() != 1 || !slices.Equal(r.agent.pushes(), []uint64{1}) {
		t.Fatalf("after prepare: writer %d, set_writer %v", r.agent.writerGen(), r.agent.pushes())
	}
	r.agent.setState(conn.StateReady)
	r.m.HostReady("pc")
	if _, err := r.m.CheckAI("pc", aiA); err == nil {
		t.Fatal("AI input passed the stop after the reconnect")
	}

	r.agent.mu.Lock()
	r.agent.writer = 40
	r.agent.mu.Unlock()
	r.changes()
	if err := r.m.PrepareConn(bg, "pc", r.agent.call); err != nil {
		t.Fatal(err)
	}
	if s := r.m.Get("pc"); s.Gen != 40 || !s.AIPaused {
		t.Fatalf("state after a higher agent gen %+v", s)
	}
	if c := r.changes(); len(c) != 1 || c[0].Gen != 40 {
		t.Fatalf("changes %+v", c)
	}
	if row, _ := r.st.GetControl("pc"); row.Gen != 40 {
		t.Fatalf("stored gen %d", row.Gen)
	}
	if s, err := r.m.ResumeAI(bg, "pc", human); err != nil || s.Gen != 41 || r.agent.writerGen() != 41 {
		t.Fatalf("resume after taking the agent gen: %+v, %v, agent %d", s, err, r.agent.writerGen())
	}

	r.agent.mu.Lock()
	r.agent.failSetWriter = 100
	r.agent.mu.Unlock()
	if err := r.m.PrepareConn(bg, "pc", r.agent.call); err == nil {
		t.Fatal("a prepare whose set_writer failed passed")
	}
}

func TestPushRetriesUntilTheAgentTakesTheGen(t *testing.T) {
	r := newRig(t, time.Minute)
	r.agent.mu.Lock()
	r.agent.failSetWriter = 3
	r.agent.mu.Unlock()
	st, err := r.m.Stop(bg, "pc", human)
	if err != nil {
		t.Fatal(err)
	}
	if r.agent.writerGen() != st.Gen {
		t.Fatalf("stop answered before the agent took gen %d (agent %d)", st.Gen, r.agent.writerGen())
	}
	if n := r.agent.setWriterCalls(); n != 4 {
		t.Fatalf("%d set_writer calls, want 4", n)
	}
}

func TestAIInputTakesTheGenOfAnAgentAhead(t *testing.T) {
	r := newRig(t, time.Minute)
	r.agent.mu.Lock()
	r.agent.writer = 30
	r.agent.mu.Unlock()
	acks, err := r.m.SendAI(bg, "pc", aiA, []wire.InputEvent{{K: "move", X: 3, Y: 4}})
	if err != nil || len(acks) != 1 || !acks[0].OK {
		t.Fatalf("AI input below the agent's generation: %+v, %v", acks, err)
	}
	if s := r.m.Get("pc"); s.Gen != 30 || s.Owner != "ai" || s.OwnerClientID != aiA.ClientID {
		t.Fatalf("state %+v", s)
	}
	if hdr, evs, _, _ := r.agent.stream(0); hdr != 30 || len(evs) != 1 || evs[0].Gen != 30 {
		t.Fatalf("stream header %d events %+v", hdr, evs)
	}
}

func TestAIStopPausesWithoutTakingHumanControl(t *testing.T) {
	r := newRig(t, time.Minute)
	if _, err := r.m.CheckAI("pc", aiA); err != nil {
		t.Fatal(err)
	}
	st, err := r.m.Stop(bg, "pc", aiA)
	if err != nil || st.Owner != "none" || !st.AIPaused || st.OwnerClient != "" || st.OwnerClientID != "" || st.Gen != 2 {
		t.Fatalf("AI stop while the AI holds: %+v, %v", st, err)
	}
	tl, _ := r.st.ListTimeline(store.TimelineFilter{HostID: "pc", Limit: 1})
	if len(tl) != 1 || tl[0].Actor != model.ActorAI || tl[0].ClientID != aiA.ClientID {
		t.Fatalf("timeline %+v", tl)
	}
	if _, err := r.m.ResumeAI(bg, "pc", human); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.Acquire(bg, "pc", OwnerHuman, human, false); err != nil {
		t.Fatal(err)
	}
	st, err = r.m.Stop(bg, "pc", aiB)
	if err != nil || st.Owner != "human" || st.OwnerClientID != human.ClientID || !st.AIPaused {
		t.Fatalf("AI stop while a person holds: %+v, %v", st, err)
	}
}

func TestStopReachesTheAgentWhileTheCutWaits(t *testing.T) {
	r := newRig(t, time.Minute)
	done := make(chan error, 1)
	go func() {
		_, err := r.m.SendAI(bg, "pc", aiA, []wire.InputEvent{{K: "text", Text: strings.Repeat("a", 3000)}})
		done <- err
	}()
	eventually(t, "the text to start", func() bool {
		_, _, typed, _ := r.agent.stream(0)
		return len(typed) >= 5
	})
	stall := make(chan struct{})
	r.agent.mu.Lock()
	r.agent.stall = stall
	r.agent.mu.Unlock()
	stopped := make(chan model.ControlState, 1)
	go func() {
		st, _ := r.m.Stop(bg, "pc", human)
		stopped <- st
	}()
	eventually(t, "the stop's writer generation at the agent while the cut waits", func() bool { return r.agent.writerGen() == 2 })
	select {
	case <-stopped:
		t.Fatal("the stop answered before its cut was written")
	default:
	}
	r.agent.mu.Lock()
	r.agent.stall = nil
	r.agent.mu.Unlock()
	close(stall)
	if st := <-stopped; st.Gen != 2 {
		t.Fatalf("stop %+v", st)
	}
	if err := <-done; err == nil {
		t.Fatal("the cut text succeeded")
	}
}

func TestWriterCheckRefusesAChangedWriter(t *testing.T) {
	r := newRig(t, time.Minute)
	gen, err := r.m.WriterGen("pc", aiA)
	if err != nil || r.m.WriterCheck("pc", aiA, gen) != nil || r.m.StillAI("pc", aiA, gen) != nil {
		t.Fatalf("check of the AI owner's gen %d: %v", gen, err)
	}
	if err := r.m.WriterCheck("pc", aiB, gen); err == nil {
		t.Fatal("another AI client passed the check")
	}
	hgen, _ := r.m.WriterGen("pc", human)
	if err := r.m.WriterCheck("pc", human, hgen); err != nil {
		t.Fatalf("a person's current gen: %v", err)
	}
	if _, err := r.m.Stop(bg, "pc", human); err != nil {
		t.Fatal(err)
	}
	if code, next := codeOf(r.m.WriterCheck("pc", aiA, gen)); code != wire.CodeControlHeldByHuman || next != apierr.NextHumanResumeAI {
		t.Fatalf("AI check after the stop: %s %s", code, next)
	}
	if code, _ := codeOf(r.m.WriterCheck("pc", human, hgen)); code != wire.CodeControlPreempted {
		t.Fatalf("a person's check of an old gen: %s", code)
	}
}

func TestConcurrentChangesKeepGenAndEventsInStep(t *testing.T) {
	r := newRig(t, time.Minute)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if i%2 == 0 {
					_, _ = r.m.Acquire(bg, "pc", OwnerHuman, human, true)
					_, _ = r.m.Release(bg, "pc", human)
				} else {
					_, _ = r.m.CheckAI("pc", aiA)
					_, _ = r.m.Release(bg, "pc", aiA)
				}
			}
		}()
	}
	wg.Wait()
	final := r.m.Get("pc").Gen
	var last uint64
	for _, c := range r.changes() {
		if c.Gen != last+1 {
			t.Fatalf("control.changed gen %d after %d", c.Gen, last)
		}
		last = c.Gen
	}
	if last != final {
		t.Fatalf("last event gen %d, state gen %d", last, final)
	}
	if row, _ := r.st.GetControl("pc"); row.Gen != final {
		t.Fatalf("stored gen %d, state gen %d", row.Gen, final)
	}
	eventually(t, "the final gen at the agent", func() bool { return r.agent.writerGen() == final })
}
