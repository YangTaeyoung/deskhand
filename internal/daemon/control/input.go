package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// acceptTimeout bounds the wait for the first line of an input stream.
	acceptTimeout = 10 * time.Second
	// frameTimeout bounds the write of one input frame.
	frameTimeout = 10 * time.Second
	// cutTimeout bounds the write of the release_all of a cut.
	cutTimeout = 2 * time.Second
	// maxAckFrame bounds an ack frame of the agent.
	maxAckFrame = 64 << 10
)

// aiLink carries one host's AI input to its agent over one input stream at
// a time.
type aiLink struct {
	// sendMu runs one AI send at a time.
	sendMu sync.Mutex

	mu  sync.Mutex
	cur *aiStream
}

// abort ends the link's stream at once.
func (lk *aiLink) abort() {
	lk.mu.Lock()
	s := lk.cur
	lk.cur = nil
	lk.mu.Unlock()
	if s != nil {
		s.abort()
	}
}

// link returns the AI input link of hostID, adding it.
func (m *Manager) link(hostID string) *aiLink {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.hostLocked(hostID)
	if h.link == nil {
		h.link = &aiLink{}
	}
	return h.link
}

// cut ends the AI input stream of hostID whose writer generation is older
// than gen: it sends release_all with gen on that stream and ends the
// daemon's direction. The agent takes gen when it reads that event, stops
// the text it is typing and releases every held key and button.
func (m *Manager) cut(hostID string, gen uint64) {
	m.mu.Lock()
	var lk *aiLink
	if h := m.hosts[hostID]; h != nil {
		lk = h.link
	}
	m.mu.Unlock()
	if lk == nil {
		return
	}
	lk.mu.Lock()
	s := lk.cur
	if s != nil && s.gen < gen {
		lk.cur = nil
	} else {
		s = nil
	}
	lk.mu.Unlock()
	if s != nil {
		s.cutTo(gen)
	}
}

// aiStream is one input stream to the agent (docs/protocol.md §6).
type aiStream struct {
	st      conn.DataStream
	gen     uint64
	connGen uint64

	// wmu orders the frame writes.
	wmu sync.Mutex

	mu      sync.Mutex
	seq     uint64
	waiters map[uint64]chan wire.InputAck
	// cut is set once the stream takes no more AI events; ended once it has
	// ended.
	cut, ended bool
}

// errStreamCut is the send of an event on a stream that was cut.
var errStreamCut = errors.New("the input stream was cut")

// acceptLine is the first line of an input stream: the agent's acceptance
// or refusal.
type acceptLine struct {
	OK    bool   `json:"ok"`
	Gen   uint64 `json:"gen"`
	Code  string `json:"code"`
	Error string `json:"error"`
}

// openStream opens an input stream of writer generation gen to a.
func openStream(ctx context.Context, a Agent, gen uint64) (*aiStream, error) {
	st, cg, err := a.OpenStream(ctx, wire.StreamHeader{S: "input", Gen: gen})
	if err != nil {
		return nil, err
	}
	br := bufio.NewReader(st)
	_ = st.SetReadDeadline(time.Now().Add(acceptTimeout))
	var line acceptLine
	if err := wire.ReadHeader(br, &line); err != nil {
		st.CancelWrite()
		st.CancelRead()
		return nil, apierr.New(wire.CodeOffline, "PC의 에이전트가 입력 스트림에 답하지 않았습니다. 연결을 확인한 뒤 다시 시도하세요", true, apierr.NextRetry)
	}
	_ = st.SetReadDeadline(time.Time{})
	if !line.OK {
		st.CancelWrite()
		st.CancelRead()
		code := line.Code
		if code == "" {
			code = wire.CodeInternal
		}
		msg := line.Error
		if msg == "" {
			msg = "PC의 에이전트가 입력 스트림을 거절했습니다. PC의 에이전트 로그를 확인하세요"
		}
		return nil, apierr.New(code, msg, false, "")
	}
	s := &aiStream{st: st, gen: gen, connGen: cg, waiters: map[uint64]chan wire.InputAck{}}
	go s.read(br)
	return s, nil
}

// read hands the agent's acks to their waiters until the stream ends; the
// waiters left then get a closed channel.
func (s *aiStream) read(br *bufio.Reader) {
	for {
		kind, payload, err := wire.ReadFrame(br, maxAckFrame)
		if err != nil {
			break
		}
		if kind != wire.KindInputAck {
			continue
		}
		var ack wire.InputAck
		if json.Unmarshal(payload, &ack) != nil {
			continue
		}
		s.mu.Lock()
		ch := s.waiters[ack.Seq]
		delete(s.waiters, ack.Seq)
		s.mu.Unlock()
		if ch != nil {
			ch <- ack
		}
	}
	s.mu.Lock()
	s.ended = true
	waiters := s.waiters
	s.waiters = nil
	s.mu.Unlock()
	for _, ch := range waiters {
		close(ch)
	}
	s.st.CancelRead()
	_ = s.st.Close()
}

// usable reports whether the stream takes AI events of gen on the
// connection connGen.
func (s *aiStream) usable(gen, connGen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.cut && !s.ended && s.gen == gen && s.connGen == connGen
}

// send writes ev with the stream's gen and the next seq and returns the
// channel its ack arrives on.
func (s *aiStream) send(ev wire.InputEvent) (chan wire.InputAck, error) {
	s.mu.Lock()
	if s.cut || s.ended {
		s.mu.Unlock()
		return nil, errStreamCut
	}
	s.seq++
	ev.Seq, ev.Gen = s.seq, s.gen
	ch := make(chan wire.InputAck, 1)
	s.waiters[ev.Seq] = ch
	s.mu.Unlock()
	b, err := json.Marshal(ev)
	if err == nil {
		s.wmu.Lock()
		_ = s.st.SetWriteDeadline(time.Now().Add(frameTimeout))
		err = wire.WriteFrame(s.st, wire.KindClientInput, b)
		s.wmu.Unlock()
	}
	if err != nil {
		s.mu.Lock()
		delete(s.waiters, ev.Seq)
		s.mu.Unlock()
		s.abort()
		return nil, err
	}
	return ch, nil
}

// cutTo stops the stream taking AI events, sends release_all with gen and
// ends the daemon's direction; the agent then injects what it has read and
// closes the stream.
func (s *aiStream) cutTo(gen uint64) {
	s.mu.Lock()
	if s.cut || s.ended {
		s.cut = true
		s.mu.Unlock()
		return
	}
	s.cut = true
	s.seq++
	seq := s.seq
	s.mu.Unlock()
	b, _ := json.Marshal(wire.InputEvent{Seq: seq, Gen: gen, K: "release_all"})
	s.wmu.Lock()
	_ = s.st.SetWriteDeadline(time.Now().Add(cutTimeout))
	err := wire.WriteFrame(s.st, wire.KindClientInput, b)
	if err == nil {
		err = s.st.CloseWrite()
	}
	s.wmu.Unlock()
	if err != nil {
		s.abort()
	}
}

// abort resets both directions; the agent stops the text it is typing and
// releases the keys and buttons the stream holds.
func (s *aiStream) abort() {
	s.mu.Lock()
	s.cut = true
	s.mu.Unlock()
	s.st.CancelWrite()
	s.st.CancelRead()
}

// stream returns the link's stream for AI events of gen, opening a new one
// when the current one is of another generation or connection.
func (m *Manager) stream(ctx context.Context, hostID string, lk *aiLink, gen uint64) (*aiStream, error) {
	a := m.agent(hostID)
	if a == nil {
		return nil, apierr.New(wire.CodeOffline, "이 PC와 연결돼 있지 않습니다. deskhand host connect로 연결한 뒤 다시 시도하세요", true, apierr.NextHostConnect)
	}
	lk.mu.Lock()
	s := lk.cur
	lk.mu.Unlock()
	if s != nil && s.usable(gen, a.Generation()) {
		return s, nil
	}
	if s != nil {
		s.abort()
	}
	ns, err := openStream(ctx, a, gen)
	if err != nil {
		return nil, err
	}
	lk.mu.Lock()
	lk.cur = ns
	lk.mu.Unlock()
	if now := m.HumanGen(hostID); now != gen {
		m.cut(hostID, now)
	}
	return ns, nil
}

// SendAI sends evs as AI input of client c to hostID's agent and returns
// the acks that came back, in order. It checks control first (CheckAI) and
// sends nothing once the writer has changed; the agent then refuses what
// it had not injected yet. A failed ack is the error of its code; a stream
// that ends before every ack is EXECUTION_OUTCOME_UNKNOWN.
func (m *Manager) SendAI(ctx context.Context, hostID string, c ClientInfo, evs []wire.InputEvent) ([]wire.InputAck, error) {
	if len(evs) == 0 {
		return nil, apierr.Invalid("보낼 입력이 없습니다. 입력 이벤트를 하나 이상 주세요")
	}
	gen, err := m.CheckAI(hostID, c)
	if err != nil {
		return nil, err
	}
	lk := m.link(hostID)
	lk.sendMu.Lock()
	defer lk.sendMu.Unlock()
	if err := m.stillAI(hostID, c, gen); err != nil {
		return nil, err
	}
	s, err := m.stream(ctx, hostID, lk, gen)
	if err != nil {
		return nil, err
	}
	var waits []chan wire.InputAck
	var stopped error
	for _, ev := range evs {
		if err := m.stillAI(hostID, c, gen); err != nil {
			stopped = err
			break
		}
		ch, err := s.send(ev)
		if err != nil {
			if stopped = m.stillAI(hostID, c, gen); stopped == nil {
				stopped = outcomeUnknown()
			}
			break
		}
		waits = append(waits, ch)
	}
	acks := make([]wire.InputAck, 0, len(waits))
	var failed error
	for _, ch := range waits {
		select {
		case ack, ok := <-ch:
			if !ok {
				return acks, outcomeUnknown()
			}
			acks = append(acks, ack)
			if !ack.OK && failed == nil {
				failed = ackError(ack)
			}
		case <-ctx.Done():
			lk.abort()
			return acks, outcomeUnknown()
		}
	}
	if failed != nil {
		return acks, failed
	}
	return acks, stopped
}

// ackError is the error of a failed ack.
func ackError(a wire.InputAck) error {
	code := a.Code
	if code == "" {
		code = wire.CodeInternal
	}
	msg := a.Error
	if msg == "" {
		msg = fmt.Sprintf("PC의 에이전트가 입력 %d을 주입하지 못했습니다. PC의 에이전트 로그를 확인하세요", a.Seq)
	}
	next := ""
	if code == wire.CodeControlPreempted {
		next = apierr.NextCheckState
	}
	return apierr.New(code, msg, false, next)
}

func outcomeUnknown() *apierr.Error {
	return apierr.New(wire.CodeExecutionOutcomeUnknown, "입력을 보냈지만 PC의 확인을 받기 전에 연결이 끊겼습니다. 화면을 다시 확인한 뒤 필요하면 다시 보내세요", false, apierr.NextCheckState)
}
