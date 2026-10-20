package conn

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/transport"
	"github.com/YangTaeyoung/deskhand/internal/wire"
	"github.com/quic-go/quic-go"
)

var target = Target{HostID: "windows-dev", Name: "사무실 PC", Address: "127.0.0.1", Port: 47600, Fingerprint: "fp"}

func connected(t *testing.T, m *Manager) *Host {
	t.Helper()
	h := m.Add(target)
	h.Connect()
	waitState(t, h, StateReady)
	return h
}

var ridPattern = regexp.MustCompile(`^r-[0-9A-HJKMNP-TV-Z]{26}$`)

func TestCallAssignsRidForMutatingOps(t *testing.T) {
	f := newFakeAgent(t)
	m, l, _ := testManager(t, f, nil)
	h := connected(t, m)
	ctx := context.Background()

	var info CallInfo
	res, err := h.Call(ctx, "exec.run", proto.Message{"argv": []string{"echo", "hi"}}, CallOpts{Info: &info})
	if err != nil {
		t.Fatal(err)
	}
	if res.String("stdout") != "hi\n" {
		t.Fatalf("result %v", res)
	}
	runs := f.requests("exec.run")
	if len(runs) != 1 {
		t.Fatalf("exec.run requests %v", runs)
	}
	rid := runs[0].String("rid")
	if !ridPattern.MatchString(rid) || info.RID != rid || info.Generation != 1 {
		t.Fatalf("rid %q, info %+v", rid, info)
	}
	if got := l.history(rid); !reflect.DeepEqual(got, []string{"sent", "done"}) {
		t.Fatalf("request log of %s = %v", rid, got)
	}

	if _, err := h.Call(ctx, "fs.list", proto.Message{"path": "C:\\"}, CallOpts{}); err != nil {
		t.Fatal(err)
	}
	if lists := f.requests("fs.list"); len(lists) != 1 || lists[0].Has("rid") {
		t.Fatalf("fs.list requests %v", lists)
	}

	if _, err := h.Call(ctx, "exec.run", proto.Message{"argv": []string{"x"}}, CallOpts{RID: "r-given"}); err != nil {
		t.Fatal(err)
	}
	if runs := f.requests("exec.run"); runs[1].String("rid") != "r-given" {
		t.Fatalf("explicit rid replaced: %v", runs[1])
	}
	for _, op := range []string{"exec.run", "exec.start", "exec.write", "exec.kill", "proc.kill", "fs.write", "fs.rename",
		"fs.mkdir", "fs.delete", "app.launch", "clipboard.set", "win.action", "ui.act"} {
		if !Journaled(op) {
			t.Errorf("%s is not journaled", op)
		}
	}
	for _, op := range []string{"fs.list", "fs.read", "proc.list", "exec.read", "ping", "req.status"} {
		if Journaled(op) {
			t.Errorf("%s is journaled", op)
		}
	}
}

func TestOutcomeResolvedAfterReconnect(t *testing.T) {
	f := newFakeAgent(t)
	var once sync.Once
	f.dropBefore = func(req proto.Message) bool {
		drop := false
		once.Do(func() { drop = true })
		return drop
	}
	m, l, _ := testManager(t, f, nil)
	h := connected(t, m)

	var info CallInfo
	res, err := h.Call(context.Background(), "exec.run", proto.Message{"argv": []string{"echo", "hi"}}, CallOpts{Info: &info})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res["ok"] != nil || res.String("stdout") != "hi\n" || res.Has("state") {
		t.Fatalf("resolved result %v", res)
	}
	if n := f.execCount(); n != 1 {
		t.Fatalf("the agent ran exec.run %d times", n)
	}
	if runs := f.requests("exec.run"); len(runs) != 1 {
		t.Fatalf("exec.run was sent %d times", len(runs))
	}
	st := f.requests("req.status")
	if len(st) == 0 || st[0].String("rid") != info.RID {
		t.Fatalf("req.status requests %v, rid %s", st, info.RID)
	}
	if !info.Sent || !info.Interrupted || !info.Resolved || !info.Answered {
		t.Fatalf("call info %+v", info)
	}
	if got := l.history(info.RID); !reflect.DeepEqual(got, []string{"sent", "unknown", "done"}) {
		t.Fatalf("request log = %v", got)
	}
	if g := h.Generation(); g != 2 {
		t.Fatalf("generation after the reconnect = %d", g)
	}
}

func TestOutcomeUnknownWhenAgentLostIt(t *testing.T) {
	f := newFakeAgent(t)
	f.forget = true
	var once sync.Once
	f.dropBefore = func(proto.Message) bool {
		drop := false
		once.Do(func() { drop = true })
		return drop
	}
	m, l, _ := testManager(t, f, nil)
	h := connected(t, m)

	var info CallInfo
	_, err := h.Call(context.Background(), "exec.run", proto.Message{"argv": []string{"echo", "hi"}}, CallOpts{Info: &info})
	e := apierr.As(err)
	if e == nil || e.Code != wire.CodeExecutionOutcomeUnknown || e.Retryable || e.NextAction != "check_state" {
		t.Fatalf("error %#v", err)
	}
	if !strings.Contains(e.Message, info.RID) {
		t.Fatalf("message %q does not name the request %s", e.Message, info.RID)
	}
	if !info.Sent || info.Answered {
		t.Fatalf("call info %+v", info)
	}
	if n := f.execCount(); n != 1 {
		t.Fatalf("the agent ran exec.run %d times", n)
	}
	if got := l.history(info.RID); !reflect.DeepEqual(got, []string{"sent", "unknown"}) {
		t.Fatalf("request log = %v", got)
	}
}

func TestFingerprintMismatchStopsRetry(t *testing.T) {
	f := newFakeAgent(t)
	f.dialErr = func(int) error { return fmt.Errorf("%w (got abc)", transport.ErrPeerMismatch) }
	var sleeps int
	var mu sync.Mutex
	m, _, evs := testManager(t, f, func(o *Options) {
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			mu.Lock()
			sleeps++
			mu.Unlock()
			return sleepCtx(ctx, d)
		}
	})
	h := m.Add(target)
	h.Connect()
	waitState(t, h, StateBlocked)
	time.Sleep(200 * time.Millisecond)
	if n := f.dialCount(); n != 1 {
		t.Fatalf("dialed %d times while blocked", n)
	}
	mu.Lock()
	if sleeps != 0 {
		t.Fatalf("waited %d times for a retry while blocked", sleeps)
	}
	mu.Unlock()
	want := "사무실 PC의 인증서가 페어링 때와 다릅니다. 재설치했다면 deskhand host repair 사무실 PC을 실행하세요"
	e := apierr.As(h.LastError())
	if e == nil || e.Message != want || e.NextAction != "host_repair" {
		t.Fatalf("last error %#v", h.LastError())
	}
	_, err := h.Call(context.Background(), "ping", nil, CallOpts{})
	if ce := apierr.As(err); ce == nil || ce.Message != want {
		t.Fatalf("call while blocked: %v", err)
	}
	if got := evs.states(); got[len(got)-1] != "blocked" {
		t.Fatalf("state events %v", got)
	}

	h.Connect()
	waitState(t, h, StateBlocked)
	time.Sleep(100 * time.Millisecond)
	if n := f.dialCount(); n != 2 {
		t.Fatalf("Connect while blocked dialed %d times in all, want 2", n)
	}
}

func TestAgentRejectionBlocks(t *testing.T) {
	f := newFakeAgent(t)
	f.dialErr = func(int) error {
		return fmt.Errorf("dial: %w", &quic.TransportError{Remote: true, ErrorCode: quic.TransportErrorCode(0x100 + 42)})
	}
	m, _, _ := testManager(t, f, nil)
	h := m.Add(target)
	h.Connect()
	waitState(t, h, StateBlocked)
	want := "사무실 PC PC가 이 Mac을 더 이상 신뢰하지 않습니다. deskhand host repair 사무실 PC을 실행하세요"
	if e := apierr.As(h.LastError()); e == nil || e.Message != want {
		t.Fatalf("last error %v", h.LastError())
	}
	time.Sleep(100 * time.Millisecond)
	if n := f.dialCount(); n != 1 {
		t.Fatalf("dialed %d times", n)
	}
}

func TestReconnectBackoffIncreases(t *testing.T) {
	f := newFakeAgent(t)
	f.dialErr = func(int) error { return errors.New("network is unreachable") }
	var mu sync.Mutex
	var delays []time.Duration
	enough := make(chan struct{})
	m, _, _ := testManager(t, f, func(o *Options) {
		o.Backoff = BackoffConfig{Initial: 500 * time.Millisecond, Max: 10 * time.Second, Jitter: -1}
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			mu.Lock()
			delays = append(delays, d)
			if len(delays) == 8 {
				close(enough)
			}
			mu.Unlock()
			if len(delays) >= 8 {
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}
	})
	h := m.Add(target)
	h.Connect()
	select {
	case <-enough:
	case <-time.After(5 * time.Second):
		t.Fatal("the host did not retry 8 times")
	}
	h.Disconnect()
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		10 * time.Second, 10 * time.Second, 10 * time.Second}
	mu.Lock()
	got := append([]time.Duration(nil), delays[:8]...)
	mu.Unlock()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("delays %v, want %v", got, want)
	}
	if h.State() != StateOffline {
		t.Fatalf("state after Disconnect %s", h.State())
	}

	b := newBackoff(BackoffConfig{Initial: 500 * time.Millisecond, Max: 10 * time.Second, Jitter: 0.2})
	base := 500 * time.Millisecond
	for i := 0; i < 12; i++ {
		d := b.Next()
		lo, hi := time.Duration(float64(base)*0.8), time.Duration(math.Min(float64(base)*1.2, float64(10*time.Second)))
		if d < lo || d > hi {
			t.Fatalf("delay %d = %s, want within [%s, %s]", i, d, lo, hi)
		}
		base = min(2*base, 10*time.Second)
	}
	b.Reset()
	if d := b.Next(); d > 600*time.Millisecond {
		t.Fatalf("first delay after Reset = %s", d)
	}
}

func TestStateEventsAndGeneration(t *testing.T) {
	f := newFakeAgent(t)
	var once sync.Once
	f.dropBefore = func(proto.Message) bool {
		drop := false
		once.Do(func() { drop = true })
		return drop
	}
	m, _, evs := testManager(t, f, nil)
	h := connected(t, m)
	if h.Generation() != 1 {
		t.Fatalf("generation %d", h.Generation())
	}
	hello := h.Hello()
	if hello.Agent != "fake-1" || hello.OS != "windows" || hello.Caps["exec"].State != "ready" || hello.BootID != "b1" {
		t.Fatalf("hello %+v", hello)
	}
	if _, err := h.Call(context.Background(), "exec.run", proto.Message{"argv": []string{"x"}}, CallOpts{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"connecting", "ready", "reconnecting", "ready"}
	if got := evs.states(); !reflect.DeepEqual(got, want) {
		t.Fatalf("state events %v, want %v", got, want)
	}
	h.Disconnect()
	if h.State() != StateOffline {
		t.Fatalf("state %s", h.State())
	}
	if _, err := h.Call(context.Background(), "ping", nil, CallOpts{}); apierr.CodeOf(err) != wire.CodeOffline {
		t.Fatalf("call while offline: %v", err)
	}
}

func TestAgentErrorBecomesCodedError(t *testing.T) {
	f := newFakeAgent(t)
	m, _, _ := testManager(t, f, nil)
	h := connected(t, m)
	_, err := h.Call(context.Background(), "fail.coded", nil, CallOpts{})
	if e := apierr.As(err); e == nil || e.Code != wire.CodeStaleTarget || e.Message != "element e1 no longer exists; call ui.tree again" {
		t.Fatalf("error %#v", err)
	}
	_, err = h.Call(context.Background(), "nope", nil, CallOpts{})
	if e := apierr.As(err); e == nil || e.Code != wire.CodeCapabilityUnsupported {
		t.Fatalf("unknown op error %#v", err)
	}
}

func TestDropAfterSendHook(t *testing.T) {
	f := newFakeAgent(t)
	m, _, _ := testManager(t, f, func(o *Options) { o.DropAfterSend = "exec.run" })
	h := connected(t, m)
	var first, second CallInfo
	if _, err := h.Call(context.Background(), "exec.run", proto.Message{"argv": []string{"x"}}, CallOpts{Info: &first}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Call(context.Background(), "exec.run", proto.Message{"argv": []string{"y"}}, CallOpts{Info: &second}); err != nil {
		t.Fatal(err)
	}
	if !first.Interrupted || !first.Resolved || second.Interrupted {
		t.Fatalf("first %+v, second %+v", first, second)
	}
	if f.execCount() != 2 || h.Generation() != 2 {
		t.Fatalf("execs %d, generation %d", f.execCount(), h.Generation())
	}
}

func TestDegradedWithoutPong(t *testing.T) {
	f := newFakeAgent(t)
	m, _, _ := testManager(t, f, func(o *Options) {
		o.PingInterval = 20 * time.Millisecond
		o.PingTimeout = 30 * time.Millisecond
	})
	h := connected(t, m)
	f.mu.Lock()
	f.noPong = true
	f.mu.Unlock()
	waitState(t, h, StateDegraded)
	f.mu.Lock()
	f.noPong = false
	f.mu.Unlock()
	waitState(t, h, StateReady)
}

func TestBlockReason(t *testing.T) {
	f := newFakeAgent(t)
	f.dialErr = func(int) error { return transport.ErrPeerMismatch }
	m, _, _ := testManager(t, f, nil)
	h := m.Add(target)
	if h.BlockReason() != "" {
		t.Fatal("block reason before connecting")
	}
	h.Connect()
	waitState(t, h, StateBlocked)
	if h.BlockReason() != BlockFingerprint {
		t.Fatalf("block reason %q", h.BlockReason())
	}
}

func dropFirst() func(proto.Message) bool {
	var once sync.Once
	return func(proto.Message) bool {
		drop := false
		once.Do(func() { drop = true })
		return drop
	}
}

func TestOversizedRequestIsNotSent(t *testing.T) {
	f := newFakeAgent(t)
	m, l, _ := testManager(t, f, nil)
	h := connected(t, m)
	var info CallInfo
	_, err := h.Call(context.Background(), "fs.write", proto.Message{"path": "C:\\a", "data": strings.Repeat("A", 2<<20)}, CallOpts{Info: &info})
	if !IsRequestError(err) || apierr.CodeOf(err) != wire.CodeInvalidArgument {
		t.Fatalf("error %#v", err)
	}
	if info.Sent || info.RID == "" {
		t.Fatalf("call info %+v", info)
	}
	if got := l.history(info.RID); len(got) != 0 {
		t.Fatalf("request log of a request never sent %v", got)
	}
	if got := f.requests("fs.write"); len(got) != 0 {
		t.Fatalf("the agent received %v", got)
	}
	if err := CheckRequest("fs.write", proto.Message{"data": strings.Repeat("A", 2<<20)}, "r-x"); !IsRequestError(err) {
		t.Fatalf("CheckRequest %v", err)
	}
	if err := CheckRequest("fs.write", proto.Message{"data": "AAAA"}, "r-x"); err != nil {
		t.Fatalf("CheckRequest of a small request %v", err)
	}
}

func TestAgentEventsFollowReady(t *testing.T) {
	for i := 0; i < 20; i++ {
		f := newFakeAgent(t)
		f.capsAfterWelcome = true
		m, _, evs := testManager(t, f, nil)
		h := connected(t, m)
		deadline := time.Now().Add(5 * time.Second)
		for h.Hello().Caps["exec"].State != "unavailable" {
			if time.Now().After(deadline) {
				t.Fatalf("caps after caps.changed %+v", h.Hello().Caps)
			}
			time.Sleep(time.Millisecond)
		}
		evs.mu.Lock()
		ready, agent := -1, -1
		for j, ev := range evs.evs {
			if ev.Kind == KindConnectionState && ev.Payload.String("state") == "ready" && ready < 0 {
				ready = j
			}
			if ev.Kind == KindAgentEvent && agent < 0 {
				agent = j
			}
		}
		evs.mu.Unlock()
		if ready < 0 || agent < ready {
			t.Fatalf("run %d: ready event at %d, first agent event at %d", i, ready, agent)
		}
		m.Close()
	}
}

func TestResolvePollsWhileRunning(t *testing.T) {
	f := newFakeAgent(t)
	f.dropBefore = dropFirst()
	f.runningPolls = 2
	m, l, _ := testManager(t, f, nil)
	h := connected(t, m)
	var info CallInfo
	res, err := h.Call(context.Background(), "exec.run", proto.Message{"argv": []string{"x"}}, CallOpts{Info: &info})
	if err != nil || res.String("stdout") != "hi\n" {
		t.Fatalf("result %v, %v", res, err)
	}
	if n := len(f.requests("req.status")); n != 3 {
		t.Fatalf("req.status sent %d times, want 3", n)
	}
	if got := l.history(info.RID); !reflect.DeepEqual(got, []string{"sent", "unknown", "done"}) {
		t.Fatalf("request log %v", got)
	}
}

func TestResolveWaitsThroughFailedReconnects(t *testing.T) {
	f := newFakeAgent(t)
	f.dropBefore = dropFirst()
	f.dialErr = func(n int) error {
		if n >= 2 && n <= 8 {
			return errors.New("network is unreachable")
		}
		return nil
	}
	m, l, _ := testManager(t, f, func(o *Options) {
		o.ReadyWait = 20 * time.Millisecond
		o.Backoff = BackoffConfig{Initial: 10 * time.Millisecond, Max: 20 * time.Millisecond, Jitter: -1}
	})
	h := connected(t, m)
	var info CallInfo
	if _, err := h.Call(context.Background(), "exec.run", proto.Message{"argv": []string{"x"}}, CallOpts{Info: &info}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if !info.Resolved || f.execCount() != 1 || f.dialCount() < 9 {
		t.Fatalf("info %+v, execs %d, dials %d", info, f.execCount(), f.dialCount())
	}
	if got := l.history(info.RID); got[len(got)-1] != "done" {
		t.Fatalf("request log %v", got)
	}
}

func TestWaitReadySkipsEndedConnection(t *testing.T) {
	f := newFakeAgent(t)
	m, _, _ := testManager(t, f, func(o *Options) { o.ReadyWait = 100 * time.Millisecond })
	h := m.Add(target)
	ended := newRPCClient(nil, nil, nil, nil)
	ended.once.Do(func() { close(ended.done) })
	h.mu.Lock()
	h.state, h.rpc, h.gen = StateReady, ended, 1
	h.mu.Unlock()
	begin := time.Now()
	rpc, _, err := h.waitReady(context.Background())
	if rpc != nil || err == nil || apierr.As(err).Code != wire.CodeOffline {
		t.Fatalf("waitReady %v, %v", rpc, err)
	}
	if d := time.Since(begin); d < 90*time.Millisecond {
		t.Fatalf("waitReady returned after %s instead of waiting for a change", d)
	}
	h.mu.Lock()
	h.state = StateOffline
	h.mu.Unlock()
}

func TestWaitReadyNamesNextStepWhenContextEnds(t *testing.T) {
	f := newFakeAgent(t)
	f.dialErr = func(int) error { return errors.New("network is unreachable") }
	m, _, _ := testManager(t, f, func(o *Options) { o.Backoff = BackoffConfig{Initial: time.Hour} })
	h := m.Add(target)
	h.Connect()
	waitState(t, h, StateReconnecting)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := h.Call(ctx, "fs.list", proto.Message{"path": "C:\\"}, CallOpts{})
	e := apierr.As(err)
	if e == nil || e.Code != wire.CodeOffline || strings.Contains(e.Message, "context deadline") || e.NextAction == "" {
		t.Fatalf("error %#v", err)
	}
}
