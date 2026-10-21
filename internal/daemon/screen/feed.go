package screen

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// openTimeout bounds the open of an agent stream up to its first frame.
	openTimeout = 20 * time.Second
	// acceptTimeout bounds the wait for the first line or frame of an agent
	// stream.
	acceptTimeout = 10 * time.Second
	// controlTimeout bounds the write of one control frame to the agent.
	controlTimeout = 5 * time.Second
	// endWait bounds the wait for the agent to end a stream the daemon
	// ended its direction of.
	endWait = 2 * time.Second
	// keyframeGap is the least time between two keyframe requests; a
	// request within it goes out when it has passed.
	keyframeGap = 100 * time.Millisecond
	// retryMin and retryMax bound the wait before an agent stream is opened
	// again for its viewers.
	retryMin = 200 * time.Millisecond
	retryMax = 5 * time.Second
	// steadyFor is how long an agent stream must have run for the next
	// open to wait retryMin again.
	steadyFor = 10 * time.Second
)

// errFeedGone is the join of a feed that has just ended; Attach takes a
// new one.
var errFeedGone = errors.New("the feed has ended")

// feed is the agent video and cursor streams of one display of a host and
// the viewers that share them. Its run loop opens the streams while it has
// viewers or attaches waiting, opens them again when they end, and ends
// once they were closed without viewers.
type feed struct {
	h   *Hub
	key feedKey

	mu       sync.Mutex
	viewers  map[*Viewer]struct{}
	waiting  map[*Viewer]chan error
	running  bool
	dead     bool
	kick     chan struct{}
	cur      *agentVideo
	grace    *time.Timer
	graceSeq uint64
	snap     snapshot
	lat      latencySum
}

// snapshot is the current state of a display as frames: what a viewer
// that joins or falls behind gets before the next keyframe.
type snapshot struct {
	info, paused, shape, pos []byte
}

func (s snapshot) frames() [][]byte {
	var out [][]byte
	for _, f := range [][]byte{s.info, s.paused, s.shape, s.pos} {
		if f != nil {
			out = append(out, f)
		}
	}
	return out
}

// latencySum adds up the capture-to-receive times of the access units
// since the last latency notice.
type latencySum struct {
	sumUS int64
	n     int64
}

func newFeed(h *Hub, key feedKey) *feed {
	return &feed{h: h, key: key, viewers: map[*Viewer]struct{}{}, waiting: map[*Viewer]chan error{}, kick: make(chan struct{}, 1)}
}

// join adds v to the feed: at once while the agent stream is open, after
// the next open otherwise, whose error it returns.
func (f *feed) join(ctx context.Context, v *Viewer) error {
	f.mu.Lock()
	if f.dead {
		f.mu.Unlock()
		return errFeedGone
	}
	if f.cur != nil {
		f.addLocked(v, true)
		f.mu.Unlock()
		return nil
	}
	ch := make(chan error, 1)
	f.waiting[v] = ch
	f.stopGraceLocked()
	if !f.running {
		f.running = true
		f.h.wg.Add(1)
		go f.run()
	}
	f.mu.Unlock()
	select {
	case f.kick <- struct{}{}:
	default:
	}
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
	}
	f.mu.Lock()
	if _, ok := f.waiting[v]; ok {
		delete(f.waiting, v)
		f.mu.Unlock()
		f.left()
		return ctx.Err()
	}
	f.mu.Unlock()
	if err := <-ch; err == nil {
		f.leave(v)
	}
	return ctx.Err()
}

// addLocked makes v a viewer of the open agent stream: it gets the
// snapshot and the access units from the next keyframe, which joined asks
// the agent for. f.mu is held.
func (f *feed) addLocked(v *Viewer, joined bool) {
	f.viewers[v] = struct{}{}
	v.f = f
	v.needKey = true
	v.stale = !v.q.push(f.snap.frames()...)
	f.stopGraceLocked()
	if joined {
		f.cur.wantKeyframe()
	}
	f.retuneLocked()
}

// leave removes v; the last viewer to leave starts the grace.
func (f *feed) leave(v *Viewer) {
	f.mu.Lock()
	if _, ok := f.viewers[v]; !ok {
		f.mu.Unlock()
		return
	}
	delete(f.viewers, v)
	f.retuneLocked()
	f.mu.Unlock()
	f.left()
}

// left starts the grace once the feed has neither viewers nor attaches
// waiting.
func (f *feed) left() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.viewers) > 0 || len(f.waiting) > 0 || f.dead {
		return
	}
	f.stopGraceLocked()
	seq := f.graceSeq
	f.grace = time.AfterFunc(f.h.opt.Grace, func() { f.graceOver(seq) })
}

// stopGraceLocked cancels the grace; f.mu is held.
func (f *feed) stopGraceLocked() {
	f.graceSeq++
	if f.grace != nil {
		f.grace.Stop()
		f.grace = nil
	}
}

// graceOver closes the agent streams when the grace seq is still running
// without viewers; an attach after it waits for new ones.
func (f *feed) graceOver(seq uint64) {
	f.mu.Lock()
	if seq != f.graceSeq || len(f.viewers) > 0 || len(f.waiting) > 0 {
		f.mu.Unlock()
		return
	}
	f.grace = nil
	cur := f.cur
	f.cur = nil
	f.mu.Unlock()
	if cur != nil {
		cur.close()
	}
}

// shutdown ends the feed's viewers and agent streams.
func (f *feed) shutdown() {
	f.mu.Lock()
	f.dead = true
	f.stopGraceLocked()
	viewers := make([]*Viewer, 0, len(f.viewers))
	for v := range f.viewers {
		viewers = append(viewers, v)
	}
	for v, ch := range f.waiting {
		ch <- errClosed
		delete(f.waiting, v)
	}
	cur := f.cur
	f.mu.Unlock()
	for _, v := range viewers {
		v.end()
	}
	if cur != nil {
		cur.abort()
	}
}

// bestLocked returns the quality of the best preset and the highest
// max_fps of the viewers and the attaches waiting; f.mu is held.
func (f *feed) bestLocked() (quality, bool) {
	var best quality
	found := false
	fps := 0
	pick := func(v *Viewer) {
		if q := qualities[v.preset]; !found || q.rank > best.rank {
			best, found = q, true
		}
		if want := v.maxFPS; want == 0 {
			fps = max(fps, defaultFPS)
		} else {
			fps = max(fps, want)
		}
	}
	for v := range f.viewers {
		pick(v)
	}
	for v := range f.waiting {
		pick(v)
	}
	if fps != defaultFPS {
		best.maxFPS = fps
	}
	return best, found
}

// retuneLocked asks the open agent stream for the best preset of the
// viewers; f.mu is held.
func (f *feed) retuneLocked() {
	if f.cur == nil || len(f.viewers) == 0 {
		return
	}
	if q, ok := f.bestLocked(); ok {
		f.cur.tune(q)
	}
}

// setPreset changes the preset of viewer v.
func (f *feed) setPreset(v *Viewer, p Preset) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v.preset = p
	f.retuneLocked()
}

// requestKeyframe asks the open agent stream for a keyframe.
func (f *feed) requestKeyframe() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cur != nil {
		f.cur.wantKeyframe()
	}
}

// run opens the agent streams and relays them while the feed has viewers
// or attaches waiting; it waits from retryMin up to retryMax between two
// opens.
func (f *feed) run() {
	defer f.h.wg.Done()
	defer f.stopped()
	wait := retryMin
	for {
		if f.h.base.Err() != nil {
			f.failWaiting(errClosed)
			return
		}
		if f.h.removeFeed(f) {
			return
		}
		st, err := f.open()
		switch {
		case err != nil && f.h.base.Err() != nil:
			f.failWaiting(errClosed)
			return
		case err != nil:
			f.failed(err)
		default:
			started := time.Now()
			f.serve(st)
			if time.Since(started) >= steadyFor {
				wait = retryMin
			}
		}
		if f.h.removeFeed(f) {
			return
		}
		f.sleep(wait)
		wait = min(wait*2, retryMax)
	}
}

// stopped marks the run loop ended.
func (f *feed) stopped() {
	f.mu.Lock()
	f.running = false
	f.mu.Unlock()
}

// sleep waits d, or less when an attach is waiting or the hub closes.
func (f *feed) sleep(d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-f.kick:
	case <-f.h.base.Done():
	}
}

// failWaiting fails the attaches waiting with err.
func (f *feed) failWaiting(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for v, ch := range f.waiting {
		ch <- err
		delete(f.waiting, v)
	}
}

// failed gives the error of an open to the attaches waiting, and ends the
// viewers too when retrying cannot succeed.
func (f *feed) failed(err error) {
	f.mu.Lock()
	for v, ch := range f.waiting {
		ch <- err
		delete(f.waiting, v)
	}
	var ended []*Viewer
	if !retryable(err) {
		for v := range f.viewers {
			ended = append(ended, v)
			delete(f.viewers, v)
		}
	}
	f.mu.Unlock()
	if len(ended) > 0 {
		f.h.lg.Printf("screen: %s display %d: %v; its %d viewers are detached", f.key.hostID, f.key.display, err, len(ended))
	}
	for _, v := range ended {
		v.end()
	}
}

// retryable reports whether opening the agent stream again can succeed
// after err.
func retryable(err error) bool {
	var e *apierr.Error
	if !errors.As(err, &e) {
		return true
	}
	return e.Retryable
}

// open opens the video stream of the feed's display at the best preset
// and reads its first frame.
func (f *feed) open() (*agentVideo, error) {
	a := f.h.agent(f.key.hostID)
	if a == nil {
		return nil, apierr.NotFound(fmt.Sprintf("PC %s가 등록돼 있지 않습니다. host.list로 PC를 확인하세요", f.key.hostID))
	}
	f.mu.Lock()
	q, _ := f.bestLocked()
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(f.h.base, openTimeout)
	defer cancel()
	st, connGen, err := a.OpenStream(ctx, wire.StreamHeader{S: "video", Display: f.key.display, MaxWidth: q.maxWidth, MaxFPS: q.maxFPS, Bitrate: q.bitrate})
	if err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(st, 64<<10)
	_ = st.SetReadDeadline(time.Now().Add(acceptTimeout))
	b, err := br.Peek(1)
	if err == nil && b[0] == '{' {
		var line struct {
			OK    bool   `json:"ok"`
			Code  string `json:"code"`
			Error string `json:"error"`
		}
		err = wire.ReadHeader(br, &line)
		st.CancelRead()
		st.CancelWrite()
		if err == nil {
			f.h.lg.Printf("screen: %s display %d: the agent refused the video stream: %s %s", f.key.hostID, f.key.display, line.Code, line.Error)
			return nil, f.refusal(line.Code)
		}
	}
	var first []byte
	if err == nil {
		first, err = readFrame(br)
	}
	if err != nil {
		st.CancelRead()
		st.CancelWrite()
		f.h.lg.Printf("screen: %s display %d: no first frame on the video stream: %v", f.key.hostID, f.key.display, err)
		return nil, apierr.New(wire.CodeOffline, "PC가 화면 스트림에 답하지 않았습니다. 잠시 뒤 다시 붙으세요", true, apierr.NextRetry)
	}
	_ = st.SetReadDeadline(time.Time{})
	return newAgentVideo(f, a, st, br, connGen, q, first), nil
}

// refusal is the error of the agent's refusal of the video stream with
// code: the code (NOT_FOUND as DISPLAY_UNAVAILABLE) with a sentence that
// names the next step.
func (f *feed) refusal(code string) error {
	switch code {
	case wire.CodeNotFound:
		return apierr.New(wire.CodeDisplayUnavailable, fmt.Sprintf("PC에 디스플레이 %d가 없습니다. 다른 디스플레이를 고르세요", f.key.display), false, apierr.NextFixParams)
	case wire.CodeCapabilityUnsupported:
		return apierr.New(code, "이 PC의 에이전트가 화면 스트림을 지원하지 않습니다. 에이전트를 업데이트하세요.", false, "")
	case wire.CodeInvalidArgument:
		return apierr.New(code, "PC의 에이전트가 화면 스트림 설정을 받아들이지 않았습니다. 다른 화질로 다시 붙거나 에이전트를 업데이트하세요.", false, apierr.NextFixParams)
	case wire.CodePermissionDenied:
		return apierr.New(code, "PC의 에이전트가 화면 스트림을 허용하지 않습니다. PC에서 에이전트 설정을 확인하세요.", false, "")
	case "":
		code = wire.CodeInternal
	}
	return apierr.New(code, fmt.Sprintf("PC의 에이전트가 화면 스트림을 열지 못했습니다(%s). 잠시 뒤 다시 붙고, 계속되면 PC의 에이전트 로그를 확인하세요.", code), true, apierr.NextRetry)
}

// serve relays st until it ends: it makes st the feed's open stream,
// gives the viewers its first frame and the attaches waiting their
// snapshot, opens the cursor stream and reads both.
func (f *feed) serve(st *agentVideo) {
	f.mu.Lock()
	if f.dead {
		f.mu.Unlock()
		st.abort()
		return
	}
	f.snap = snapshot{}
	f.lat = latencySum{}
	f.cur = st
	f.pauseViewersLocked()
	f.handleLocked(st, st.first, f.h.clock())
	for v, ch := range f.waiting {
		delete(f.waiting, v)
		f.addLocked(v, false)
		ch <- nil
	}
	f.retuneLocked()
	empty := len(f.viewers) == 0
	f.mu.Unlock()
	if empty {
		f.left()
	}
	f.h.measureClock(f.key.hostID, st.agent, st.connGen)
	stop := context.AfterFunc(f.h.base, st.abort)
	defer stop()
	f.h.wg.Add(2)
	go func() {
		defer f.h.wg.Done()
		st.steer()
	}()
	go func() {
		defer f.h.wg.Done()
		f.cursor(st)
	}()
	for {
		frame, err := readFrame(st.r)
		if err != nil {
			break
		}
		recv := f.h.clock()
		f.mu.Lock()
		if f.cur == st {
			f.handleLocked(st, frame, recv)
		}
		f.mu.Unlock()
	}
	f.mu.Lock()
	if f.cur == st {
		f.cur = nil
	}
	f.mu.Unlock()
	st.abort()
}

// handleLocked records frame of st in the snapshot and hands it to the
// viewers; f.mu is held.
func (f *feed) handleLocked(st *agentVideo, frame []byte, recv int64) {
	kind, payload := frame[4], frame[5:]
	switch kind {
	case wire.KindVideoInfo:
		f.snap.info, f.snap.paused = frame, nil
		f.broadcastLocked(frame, true)
	case wire.KindVideoPaused:
		f.snap.paused = frame
		f.pauseViewersLocked()
		f.broadcastLocked(frame, true)
	case wire.KindVideoStats:
		f.broadcastLocked(frame, false)
	case wire.KindAU:
		au, err := wire.UnmarshalAU(payload)
		if err != nil {
			return
		}
		f.snap.paused = nil
		if off, ok := f.h.offset(f.key.hostID, st.connGen); ok {
			lat := recv - (int64(au.CaptureMonoUS) - off)
			f.lat.sumUS += max(lat, 0)
			f.lat.n++
		}
		for v := range f.viewers {
			f.deliverAULocked(v, frame, au.Key)
		}
	}
}

// pauseViewersLocked makes every viewer wait for the next keyframe; f.mu
// is held.
func (f *feed) pauseViewersLocked() {
	for v := range f.viewers {
		v.needKey = true
	}
}

// deliverAULocked queues an access unit for v: from a keyframe on after
// v joined, fell behind or the stream paused. A viewer whose queue is full
// falls behind; f.mu is held.
func (f *feed) deliverAULocked(v *Viewer, frame []byte, key bool) {
	if v.needKey && !key {
		return
	}
	if v.stale && !f.catchUpLocked(v) {
		f.behindLocked(v)
		return
	}
	if !v.q.push(frame) {
		f.behindLocked(v)
		return
	}
	if key {
		v.needKey = false
	}
}

// broadcastLocked queues a frame other than an access unit for every
// viewer; inSnap marks a frame the snapshot holds, which a viewer that
// fell behind gets with the snapshot instead. f.mu is held.
func (f *feed) broadcastLocked(frame []byte, inSnap bool) {
	for v := range f.viewers {
		if v.stale {
			if !f.catchUpLocked(v) || inSnap {
				continue
			}
		}
		if !v.q.push(frame) {
			f.behindLocked(v)
		}
	}
}

// broadcastNotice queues notice frame for every viewer.
func (f *feed) broadcastNotice(frame []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broadcastLocked(frame, false)
}

// catchUpLocked queues the snapshot for v, which fell behind, and reports
// whether it fit; f.mu is held.
func (f *feed) catchUpLocked(v *Viewer) bool {
	if !v.q.push(f.snap.frames()...) {
		return false
	}
	v.stale = false
	return true
}

// behindLocked marks v as behind: it drops frames until the snapshot fits
// and access units until the next keyframe, which caughtUp asks the agent
// for once v's queue has drained; f.mu is held.
func (f *feed) behindLocked(v *Viewer) {
	v.stale, v.needKey = true, true
	v.lagging.Store(true)
}

// caughtUp gives v, which fell behind and whose queue has drained, the
// snapshot and asks the agent for the keyframe v waits for.
func (f *feed) caughtUp(v *Viewer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.viewers[v]; !ok {
		return
	}
	if v.stale && !f.catchUpLocked(v) {
		v.lagging.Store(true)
		return
	}
	if v.needKey && f.cur != nil {
		f.cur.wantKeyframe()
	}
}

// pauseNoOutput pauses the viewers with no_output, the reason of a
// display that is gone, unless the agent stream is paused for it already.
func (f *feed) pauseNoOutput() {
	b, _ := json.Marshal(wire.VideoPaused{Reason: "no_output"})
	frame := encodeFrame(wire.KindVideoPaused, b)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snap.paused != nil && string(f.snap.paused[5:]) == string(b) {
		return
	}
	f.snap.paused = frame
	f.pauseViewersLocked()
	f.broadcastLocked(frame, true)
}

// latencyNotice sends the mean capture-to-receive time of the access
// units since the last notice, when there were any.
func (f *feed) latencyNotice(st *agentVideo) {
	off, ok := f.h.offset(f.key.hostID, st.connGen)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cur != st || f.lat.n == 0 || !ok {
		f.lat = latencySum{}
		return
	}
	mean := float64(f.lat.sumUS) / float64(f.lat.n) / 1000
	f.lat = latencySum{}
	frame := noticeFrame(map[string]any{"type": noticeLatency, "capture_to_receive_ms": mean, "clock_offset_ms": float64(off) / 1000})
	if frame != nil {
		f.broadcastLocked(frame, false)
	}
}

// cursor opens the cursor stream of the feed's display for st and relays
// its frames until it or st ends. A refused or failed cursor stream leaves
// the viewers without cursor frames.
func (f *feed) cursor(st *agentVideo) {
	ctx, cancel := context.WithTimeout(f.h.base, openTimeout)
	cs, _, err := st.agent.OpenStream(ctx, wire.StreamHeader{S: "cursor", Display: f.key.display})
	cancel()
	if err != nil {
		f.h.lg.Printf("screen: %s display %d: open the cursor stream: %v", f.key.hostID, f.key.display, err)
		return
	}
	if !st.setCursor(cs) {
		cs.CancelRead()
		cs.CancelWrite()
		return
	}
	stop := context.AfterFunc(f.h.base, func() {
		cs.CancelRead()
		cs.CancelWrite()
	})
	defer stop()
	br := bufio.NewReaderSize(cs, 16<<10)
	_ = cs.SetReadDeadline(time.Now().Add(acceptTimeout))
	var line struct {
		OK    bool   `json:"ok"`
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := wire.ReadHeader(br, &line); err != nil || !line.OK {
		f.h.lg.Printf("screen: %s display %d: the agent refused the cursor stream: %s %s %v", f.key.hostID, f.key.display, line.Code, line.Error, err)
		cs.CancelRead()
		cs.CancelWrite()
		return
	}
	_ = cs.SetReadDeadline(time.Time{})
	for {
		frame, err := readFrame(br)
		if err != nil {
			return
		}
		kind := frame[4]
		if kind != wire.KindCursorShape && kind != wire.KindCursorPos {
			continue
		}
		f.mu.Lock()
		if f.cur == st {
			if kind == wire.KindCursorShape {
				f.snap.shape = frame
			} else {
				f.snap.pos = frame
			}
			f.broadcastLocked(frame, true)
		}
		f.mu.Unlock()
	}
}
