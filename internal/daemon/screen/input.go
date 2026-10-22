package screen

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// inputOpenTimeout bounds the open of an input stream up to its
	// acceptance line.
	inputOpenTimeout = 3 * time.Second
	// inputWriteTimeout bounds the write of one input frame.
	inputWriteTimeout = 10 * time.Second
	// inputEndWait bounds the wait for the agent to inject the events left
	// on an input stream the daemon ended its direction of.
	inputEndWait = 10 * time.Second
	// maxAckFrame bounds an ack frame of the agent.
	maxAckFrame = 64 << 10
	// logEvery is the least time between two log lines of failed sends.
	logEvery = 10 * time.Second
	// resyncEvery is the least time between two writer generation resyncs
	// after acks the agent refused for an older generation.
	resyncEvery = time.Second
)

// inputLink carries the human input of a host's viewers to its agent over
// one input stream at a time, opened on the first event and closed after
// InputIdle without input while no viewer holds a key or button, and with
// the host's last viewer.
type inputLink struct {
	h      *Hub
	hostID string

	mu       sync.Mutex
	st       conn.DataStream
	connGen  uint64
	identity *InputBinding // identity of st, not the next connection
	seq      uint64
	ended    chan struct{}
	last     time.Time
	idle     *time.Timer
	closed   bool
	logged   time.Time

	resyncAt atomic.Int64
}

// link returns the input link of hostID, adding it.
func (h *Hub) link(hostID string) *inputLink {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.links[hostID]
	if l == nil {
		l = &inputLink{h: h, hostID: hostID, closed: h.closed}
		if !h.closed {
			h.links[hostID] = l
		}
	}
	return l
}

// closeLink ends the input stream of hostID.
func (h *Hub) closeLink(hostID string) {
	h.mu.Lock()
	l := h.links[hostID]
	h.mu.Unlock()
	if l != nil {
		l.end()
	}
}

// anyHeld reports whether a viewer of hostID holds a key or button.
func (h *Hub) anyHeld(hostID string) bool {
	for _, v := range h.hostViewers(hostID) {
		if v.holds() {
			return true
		}
	}
	return false
}

// errControlChanged is the send of an event whose writer generation is no
// longer a person's.
var errControlChanged = apierr.New(wire.CodeControlPreempted, "보내기 전에 제어권이 바뀌어 입력을 보내지 않았습니다. 제어권을 확인한 뒤 다시 조작하세요", false, apierr.NextCheckState)

// errNotReady is the send of an event while the host is not ready.
var errNotReady = apierr.New(wire.CodeOffline, "PC와의 연결이 준비되지 않아 입력을 보내지 않았습니다. 연결된 뒤 다시 조작하세요", true, apierr.NextRetry)

// send writes ev, whose gen is set, with the stream's next seq, opening
// the stream when there is none on the agent's current connection. Input
// that may take control (acquire) goes out only while the host is ready;
// a release only while it is ready or degraded. A stream the agent
// refuses as older than its writer generation is opened once more after
// Resync took the agent's higher generation, with the gen CheckHuman then
// gives c when acquire is set. Right before the write, a person must still
// hold control at ev's gen, or ev is dropped with CONTROL_PREEMPTED. It
// returns why ev did not go out.
func (l *inputLink) send(c model.Caller, ev wire.InputEvent, acquire bool, binding *InputBinding) error {
	err := l.sendErr(c, ev, acquire, binding)
	if err != nil {
		l.mu.Lock()
		if time.Since(l.logged) >= logEvery {
			l.logged = time.Now()
			l.h.lg.Printf("screen: %s: human input not sent: %v", l.hostID, err)
		}
		l.mu.Unlock()
	}
	return err
}

func (l *inputLink) sendErr(c model.Caller, ev wire.InputEvent, acquire bool, binding *InputBinding) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.h.base.Err() != nil {
		return errClosed
	}
	a := l.h.agent(l.hostID)
	if binding != nil && !binding.matches(a) {
		l.retireBoundLocked(binding)
		return errInputSessionChanged
	}
	if a == nil {
		return apierr.New(wire.CodeOffline, "이 PC와 연결돼 있지 않습니다", true, apierr.NextHostConnect)
	}
	if st := a.State(); st != conn.StateReady && (acquire || st != conn.StateDegraded) {
		return errNotReady
	}
	ctl := l.h.opt.Control
	opened := false
	if !l.usableLocked(a) || (binding != nil && (l.identity == nil || *l.identity != *binding)) {
		if binding != nil {
			l.abortLocked()
		} else {
			l.endLocked()
		}
		err := l.openLocked(a, ev.Gen)
		if apierr.CodeOf(err) == wire.CodeControlPreempted && acquire && (binding == nil || binding.matches(a)) && ctl.Resync(l.hostID, ev.Gen) {
			gen, cerr := ctl.CheckHuman(l.hostID, c)
			if cerr != nil {
				return cerr
			}
			ev.Gen = gen
			err = l.openLocked(a, gen)
		}
		if err != nil {
			return err
		}
		opened = true
	}
	if st := ctl.Get(l.hostID); st.Owner != model.OwnerHuman || st.Gen != ev.Gen {
		return errControlChanged
	}
	l.seq++
	ev.Seq = l.seq
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_ = l.st.SetWriteDeadline(time.Now().Add(inputWriteTimeout))
	// OpenStream may wait through a reconnect; check both its returned
	// generation and the authoritative identity after acceptance and directly
	// before writing. A stale viewer must never reopen to send release_all.
	if binding != nil && (!binding.matches(l.h.agent(l.hostID)) || l.connGen != binding.ConnectionGeneration || l.identity == nil || *l.identity != *binding) {
		if opened {
			l.abortLocked()
		} else {
			l.retireBoundLocked(binding)
		}
		return errInputSessionChanged
	}
	if err := wire.WriteFrame(l.st, wire.KindClientInput, b); err != nil {
		l.abortLocked()
		return err
	}
	l.last = time.Now()
	if l.idle == nil {
		l.idle = time.AfterFunc(l.h.opt.InputIdle, l.idleCheck)
	} else {
		l.idle.Reset(l.h.opt.InputIdle)
	}
	return nil
}

// usableLocked reports whether the stream is open on a's current
// connection; l.mu is held.
func (l *inputLink) usableLocked(a Agent) bool {
	if l.st == nil || l.connGen != a.Generation() {
		return false
	}
	select {
	case <-l.ended:
		return false
	default:
		return true
	}
}

// openLocked opens an input stream of writer generation gen to a and
// reads its acceptance; l.mu is held.
func (l *inputLink) openLocked(a Agent, gen uint64) error {
	ctx, cancel := context.WithTimeout(l.h.base, inputOpenTimeout)
	defer cancel()
	identity, verified := currentInputBinding(a)
	st, connGen, err := a.OpenStream(ctx, wire.StreamHeader{S: "input", Gen: gen})
	if err != nil {
		return err
	}
	br := bufio.NewReader(st)
	_ = st.SetReadDeadline(time.Now().Add(inputOpenTimeout))
	var line struct {
		OK    bool   `json:"ok"`
		Gen   uint64 `json:"gen"`
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := wire.ReadHeader(br, &line); err != nil {
		st.CancelRead()
		st.CancelWrite()
		return apierr.New(wire.CodeOffline, fmt.Sprintf("PC의 에이전트가 입력 스트림에 답하지 않았습니다(%v)", err), true, apierr.NextRetry)
	}
	_ = st.SetReadDeadline(time.Time{})
	if !line.OK {
		st.CancelRead()
		st.CancelWrite()
		code := line.Code
		if code == "" {
			code = wire.CodeInternal
		}
		return apierr.New(code, line.Error, false, "")
	}
	ended := make(chan struct{})
	l.st, l.connGen, l.seq, l.ended = st, connGen, 0, ended
	l.identity = nil
	if verified && identity.ConnectionGeneration == connGen {
		l.identity = &identity
	}
	l.h.wg.Add(1)
	go l.drain(st, br, ended)
	return nil
}

// drain reads the agent's acks of st until it ends; an ack refused for an
// older writer generation resyncs the host's generation with the agent.
func (l *inputLink) drain(st conn.DataStream, br *bufio.Reader, ended chan struct{}) {
	defer l.h.wg.Done()
	defer close(ended)
	defer st.CancelRead()
	for {
		kind, payload, err := wire.ReadFrame(br, maxAckFrame)
		if err != nil {
			return
		}
		if kind != wire.KindInputAck {
			continue
		}
		var ack wire.InputAck
		if json.Unmarshal(payload, &ack) == nil && !ack.OK && ack.Code == wire.CodeControlPreempted {
			l.resync()
		}
	}
}

// resync sends the host's writer generation to the agent, taking a higher
// one it answers, at most once every resyncEvery.
func (l *inputLink) resync() {
	now := time.Now().UnixNano()
	last := l.resyncAt.Load()
	if now-last < int64(resyncEvery) || !l.resyncAt.CompareAndSwap(last, now) {
		return
	}
	ctl := l.h.opt.Control
	l.h.wg.Add(1)
	go func() {
		defer l.h.wg.Done()
		ctl.Resync(l.hostID, ctl.HumanGen(l.hostID))
	}()
}

// idleCheck ends the stream after InputIdle without input unless a viewer
// holds a key or button.
func (l *inputLink) idleCheck() {
	held := l.h.anyHeld(l.hostID)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.st == nil {
		return
	}
	if left := l.h.opt.InputIdle - time.Since(l.last); held || left > 0 {
		l.idle.Reset(max(left, l.h.opt.InputIdle/2))
		return
	}
	l.endLocked()
}

// end ends the stream; the next event opens another.
func (l *inputLink) end() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.endLocked()
}

// close resets the stream for good; the agent releases what it holds.
func (l *inputLink) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	l.abortLocked()
	if l.idle != nil {
		l.idle.Stop()
	}
}

// endLocked ends the daemon's direction of the stream: the agent injects
// the events it has, releases what the stream holds and ends it. The
// stream is reset when the agent has not ended it within inputEndWait;
// l.mu is held.
func (l *inputLink) endLocked() {
	st := l.st
	if st == nil {
		return
	}
	l.st = nil
	_ = st.CloseWrite()
	ended := l.ended
	time.AfterFunc(inputEndWait, func() {
		select {
		case <-ended:
		default:
			st.CancelRead()
			st.CancelWrite()
		}
	})
}

// abortLocked resets the stream; l.mu is held.
func (l *inputLink) abortLocked() {
	if l.st != nil {
		l.st.CancelWrite()
		l.st.CancelRead()
		l.st = nil
	}
}

// retireBoundLocked resets only the stream used by this binding. Resetting
// that old stream releases its held keys at the old agent; it writes no
// release_all into a replacement session, even if another viewer reopened it.
func (l *inputLink) retireBoundLocked(binding *InputBinding) {
	if l.identity != nil && *l.identity == *binding {
		l.abortLocked()
	}
}

func (l *inputLink) retireBound(binding *InputBinding) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.retireBoundLocked(binding)
}
