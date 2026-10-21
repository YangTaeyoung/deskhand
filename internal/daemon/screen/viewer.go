package screen

import (
	"bufio"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// viewerWriteTimeout ends a viewer whose connection takes no frame for
	// this long.
	viewerWriteTimeout = 10 * time.Second
	// maxViewerFrame bounds a frame a viewer sends.
	maxViewerFrame = 1 << 20
	// deniedEvery is the least time between two control_denied notices to
	// one viewer.
	deniedEvery = time.Second
)

// inputKinds are the input events a viewer may send (docs/protocol.md §6).
var inputKinds = map[string]bool{"move": true, "button": true, "wheel": true, "key": true, "text": true, "release_all": true}

// heldKey is a key or button a viewer pressed and has not released.
type heldKey struct {
	button string
	sc, vk uint16
	ext    bool
}

// Viewer is one screen stream of the local API.
type Viewer struct {
	h       *Hub
	hostID  string
	caller  model.Caller
	initial Preset
	maxFPS  int
	q       *queue

	// f, preset, needKey and stale are guarded by f.mu once the viewer
	// joined f. needKey drops access units until the next keyframe; stale
	// drops other frames until the snapshot fits in the queue.
	f       *feed
	preset  Preset
	needKey bool
	stale   bool
	// lagging is set while the viewer fell behind and waits for its queue
	// to drain.
	lagging atomic.Bool

	imu      sync.Mutex
	held     map[heldKey]bool
	deniedAt time.Time

	emu    sync.Mutex
	rw     io.ReadWriteCloser
	ended  bool
	endOne sync.Once
	detOne sync.Once
}

func newViewer(h *Hub, hostID string, c model.Caller, p Preset) *Viewer {
	return &Viewer{h: h, hostID: hostID, caller: c, initial: p, preset: p, q: newQueue(h.opt.QueueFrames, h.opt.QueueBytes),
		held: map[heldKey]bool{}}
}

// Preset returns the preset the viewer attached with.
func (v *Viewer) Preset() Preset { return v.initial }

// Run relays the viewer's frames over rw until either side ends: the
// display's frames and the notices go to rw; keyframe and preset controls
// and human input come from it. Then the viewer leaves its display and the
// keys and buttons it holds are released. It closes rw.
func (v *Viewer) Run(rw io.ReadWriteCloser) {
	v.emu.Lock()
	if v.ended {
		v.emu.Unlock()
		_ = rw.Close()
		v.detach()
		return
	}
	v.rw = rw
	v.emu.Unlock()
	written := make(chan struct{})
	go func() {
		defer close(written)
		v.write(rw)
	}()
	v.read(rw)
	v.end()
	<-written
	v.detach()
}

// write writes the queued frames to rw until the queue closes or a write
// fails. Once it has written the last queued frame of a viewer that fell
// behind, the viewer catches up.
func (v *Viewer) write(rw io.ReadWriteCloser) {
	dl, _ := rw.(interface{ SetWriteDeadline(time.Time) error })
	for {
		b, ok, drained := v.q.pop()
		if !ok {
			return
		}
		if dl != nil {
			_ = dl.SetWriteDeadline(time.Now().Add(viewerWriteTimeout))
		}
		if _, err := rw.Write(b); err != nil {
			v.end()
			return
		}
		if drained && v.f != nil && v.lagging.CompareAndSwap(true, false) {
			v.f.caughtUp(v)
		}
	}
}

// read handles the viewer's frames until rw ends; other kinds than 0x40
// and 0x41 are dropped.
func (v *Viewer) read(rw io.Reader) {
	br := bufio.NewReaderSize(rw, 64<<10)
	for {
		kind, payload, err := wire.ReadFrame(br, maxViewerFrame)
		if err != nil {
			return
		}
		switch kind {
		case wire.KindClientControl:
			v.control(payload)
		case wire.KindClientInput:
			v.input(payload)
		}
	}
}

// Close ends the viewer; Run, called or not yet called, then closes its
// connection and takes it off its display.
func (v *Viewer) Close() { v.end() }

// end closes the viewer's queue and connection.
func (v *Viewer) end() {
	v.endOne.Do(func() {
		v.q.close()
		v.emu.Lock()
		v.ended = true
		rw := v.rw
		v.emu.Unlock()
		if rw != nil {
			_ = rw.Close()
		}
	})
}

// detach takes the viewer off its display and releases the keys and
// buttons it holds; the host's input stream closes with its last viewer.
func (v *Viewer) detach() {
	v.detOne.Do(func() {
		if v.f != nil {
			v.f.leave(v)
		}
		v.release()
		if len(v.h.hostViewers(v.hostID)) == 0 {
			v.h.closeLink(v.hostID)
		}
	})
}

// control handles a 0x40 frame: {"op":"keyframe"} or
// {"op":"preset","preset":…}; others are dropped.
func (v *Viewer) control(payload []byte) {
	var c struct {
		Op     string `json:"op"`
		Preset string `json:"preset"`
	}
	if json.Unmarshal(payload, &c) != nil || v.f == nil {
		return
	}
	switch c.Op {
	case wire.ControlKeyframe:
		v.f.requestKeyframe()
	case "preset":
		if p, err := ParsePreset(c.Preset); err == nil && c.Preset != "" {
			v.f.setPreset(v, p)
		}
	}
}

// input handles a 0x41 frame: a human input event goes to the host's
// agent with the daemon's seq and writer generation once control lets the
// viewer's client drive the host, taking control when nobody holds it.
// Input while the host is not ready is dropped at once, before control is
// checked. Input of an AI client's viewer and input refused because an AI
// client holds control or control changed before the write
// (CONTROL_PREEMPTED) is dropped with a control_denied notice. release_all
// goes out only while a person holds control, with that owner's gen.
func (v *Viewer) input(payload []byte) {
	var ev wire.InputEvent
	if json.Unmarshal(payload, &ev) != nil || !inputKinds[ev.K] {
		return
	}
	ctl := v.h.opt.Control
	if ctl == nil {
		return
	}
	if v.caller.Actor == model.ActorAI {
		v.denied(ctl.Get(v.hostID))
		return
	}
	if ev.K == "release_all" {
		v.imu.Lock()
		v.held = map[heldKey]bool{}
		v.imu.Unlock()
		st := ctl.Get(v.hostID)
		if st.Owner != model.OwnerHuman {
			return
		}
		ev.Gen = st.Gen
		_ = v.h.link(v.hostID).send(v.caller, ev, false)
		return
	}
	if a := v.h.agent(v.hostID); a == nil || a.State() != conn.StateReady {
		return
	}
	gen, err := ctl.CheckHuman(v.hostID, v.caller)
	if err == nil {
		ev.Gen = gen
		err = v.h.link(v.hostID).send(v.caller, ev, true)
	}
	if apierr.CodeOf(err) == wire.CodeControlPreempted {
		v.denied(ctl.Get(v.hostID))
		return
	}
	if err != nil {
		return
	}
	v.track(ev)
}

// track records the keys and buttons ev presses and releases.
func (v *Viewer) track(ev wire.InputEvent) {
	var k heldKey
	switch ev.K {
	case "key":
		k = heldKey{sc: ev.SC, vk: ev.VK, ext: ev.Ext}
	case "button":
		k = heldKey{button: ev.B}
	default:
		return
	}
	v.imu.Lock()
	defer v.imu.Unlock()
	if ev.Down {
		v.held[k] = true
	} else {
		delete(v.held, k)
	}
}

// holds reports whether the viewer holds a key or button.
func (v *Viewer) holds() bool {
	v.imu.Lock()
	defer v.imu.Unlock()
	return len(v.held) > 0
}

// release sends release_all for the keys and buttons the viewer holds
// while a person holds control; another owner's generation has released
// them already.
func (v *Viewer) release() {
	v.imu.Lock()
	held := len(v.held) > 0
	v.held = map[heldKey]bool{}
	v.imu.Unlock()
	ctl := v.h.opt.Control
	if !held || ctl == nil {
		return
	}
	st := ctl.Get(v.hostID)
	if st.Owner != model.OwnerHuman {
		return
	}
	_ = v.h.link(v.hostID).send(v.caller, wire.InputEvent{K: "release_all", Gen: st.Gen}, false)
}

// denied sends the viewer a control_denied notice with the owner of st, at
// most one every deniedEvery.
func (v *Viewer) denied(st model.ControlState) {
	v.imu.Lock()
	if time.Since(v.deniedAt) < deniedEvery {
		v.imu.Unlock()
		return
	}
	v.deniedAt = time.Now()
	v.imu.Unlock()
	n := map[string]any{"type": noticeControlDenied, "owner": st.Owner}
	if st.OwnerClient != "" {
		n["owner_client"] = st.OwnerClient
	}
	if frame := noticeFrame(n); frame != nil {
		v.q.push(frame)
	}
}

// queue holds the frames waiting for one viewer, up to maxFrames frames
// and maxBytes bytes; an empty queue takes frames of any size.
type queue struct {
	mu        sync.Mutex
	frames    [][]byte
	bytes     int
	maxFrames int
	maxBytes  int
	closed    bool
	wake      chan struct{}
}

func newQueue(maxFrames, maxBytes int) *queue {
	return &queue{maxFrames: maxFrames, maxBytes: maxBytes, wake: make(chan struct{}, 1)}
}

func (q *queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// push queues frames, all of them or, when they do not fit, none, and
// reports whether they fit. A closed queue takes and drops them.
func (q *queue) push(frames ...[]byte) bool {
	if len(frames) == 0 {
		return true
	}
	n := 0
	for _, f := range frames {
		n += len(f)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return true
	}
	if len(q.frames) > 0 && (len(q.frames)+len(frames) > q.maxFrames || q.bytes+n > q.maxBytes) {
		return false
	}
	q.frames = append(q.frames, frames...)
	q.bytes += n
	q.signal()
	return true
}

// pop waits for the next frame and reports whether it was the last one
// queued; ok is false once the queue is closed.
func (q *queue) pop() (b []byte, ok, drained bool) {
	for {
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			return nil, false, false
		}
		if len(q.frames) > 0 {
			b := q.frames[0]
			q.frames[0] = nil
			q.frames = q.frames[1:]
			q.bytes -= len(b)
			drained := len(q.frames) == 0
			q.mu.Unlock()
			return b, true, drained
		}
		q.mu.Unlock()
		<-q.wake
	}
}

// close ends the queue; the frames left are dropped.
func (q *queue) close() {
	q.mu.Lock()
	q.closed = true
	q.frames = nil
	q.bytes = 0
	q.mu.Unlock()
	q.signal()
}
