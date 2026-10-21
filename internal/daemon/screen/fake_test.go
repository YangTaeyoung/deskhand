package screen

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/control"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/daemon/store"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// errReset is the error of a pipe direction the other side reset.
var errReset = errors.New("stream reset")

// pipeEnd is one end of an in-memory stream whose directions end apart.
type pipeEnd struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (p pipeEnd) Read(b []byte) (int, error)       { return p.r.Read(b) }
func (p pipeEnd) Write(b []byte) (int, error)      { return p.w.Write(b) }
func (p pipeEnd) CloseWrite() error                { return p.w.Close() }
func (p pipeEnd) CancelWrite()                     { p.w.CloseWithError(errReset) }
func (p pipeEnd) CancelRead()                      { p.r.CloseWithError(errReset) }
func (p pipeEnd) SetReadDeadline(time.Time) error  { return nil }
func (p pipeEnd) SetWriteDeadline(time.Time) error { return nil }
func (p pipeEnd) Close() error {
	p.w.Close()
	p.r.Close()
	return nil
}

// duplex returns the two ends of a new stream.
func duplex() (a, b pipeEnd) {
	toB, fromA := io.Pipe()
	toA, fromB := io.Pipe()
	return pipeEnd{r: toA, w: fromA}, pipeEnd{r: toB, w: fromB}
}

// fakeVideo is a video stream the fake agent serves: it writes an info
// frame first, like the agent, and then the frames the test writes.
type fakeVideo struct {
	hdr      wire.StreamHeader
	st       pipeEnd
	ready    chan struct{}
	wmu      sync.Mutex
	controls chan wire.VideoControl
	ended    chan struct{}
}

// readControls queues the control frames of the daemon until the daemon
// ends its direction; then it ends the agent's direction too, as the agent
// does.
func (v *fakeVideo) readControls() {
	defer func() {
		close(v.ended)
		<-v.ready
		v.wmu.Lock()
		_ = v.st.CloseWrite()
		v.wmu.Unlock()
	}()
	br := bufio.NewReader(v.st)
	for {
		kind, payload, err := wire.ReadFrame(br, 1<<20)
		if err != nil {
			return
		}
		if kind != wire.KindClientControl {
			continue
		}
		var c wire.VideoControl
		if json.Unmarshal(payload, &c) == nil {
			v.controls <- c
		}
	}
}

func (v *fakeVideo) write(kind byte, payload []byte) {
	<-v.ready
	v.wmu.Lock()
	defer v.wmu.Unlock()
	_ = wire.WriteFrame(v.st, kind, payload)
}

func infoPayload(w, h, display int) []byte {
	b, _ := json.Marshal(wire.VideoInfo{Codec: "h264", Width: w, Height: h, SrcWidth: w, SrcHeight: h, FPS: 60, Bitrate: 6000000,
		Encoder: "fake", Capture: "dd", Display: display})
	return b
}

func (v *fakeVideo) info() { v.write(wire.KindVideoInfo, infoPayload(1920, 1080, v.hdr.Display)) }

// au writes an access unit; a key one carries SPS/PPS.
func (v *fakeVideo) au(key bool, seq uint64) { v.auAt(key, seq, 0) }

func (v *fakeVideo) auAt(key bool, seq, captureUS uint64) {
	nal := byte(0x41)
	if key {
		nal = 0x65
	}
	a := wire.AU{Key: key, HasConfig: key, Width: 1920, Height: 1080, Seq: seq, CaptureMonoUS: captureUS, EncodeDoneMonoUS: captureUS,
		Data: []byte{0, 0, 0, 1, nal, byte(seq)}}
	v.write(wire.KindAU, a.Marshal())
}

func (v *fakeVideo) paused(reason string) {
	b, _ := json.Marshal(wire.VideoPaused{Reason: reason})
	v.write(wire.KindVideoPaused, b)
}

// end ends the agent's direction of the stream.
func (v *fakeVideo) end() {
	<-v.ready
	v.wmu.Lock()
	defer v.wmu.Unlock()
	_ = v.st.CloseWrite()
}

// isEnded reports whether the daemon has ended its direction.
func (v *fakeVideo) isEnded() bool {
	select {
	case <-v.ended:
		return true
	default:
		return false
	}
}

// control waits up to 5 s for the next control frame of op, skipping
// others.
func (v *fakeVideo) control(t *testing.T, op string) wire.VideoControl {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case c := <-v.controls:
			if c.Op == op {
				return c
			}
		case <-timer.C:
			t.Fatalf("no %s control on the video stream", op)
		}
	}
}

// drainControls forgets the control frames received so far.
func (v *fakeVideo) drainControls() {
	for {
		select {
		case <-v.controls:
		default:
			return
		}
	}
}

// fakeCursor is a cursor stream the fake agent serves.
type fakeCursor struct {
	hdr   wire.StreamHeader
	st    pipeEnd
	ready chan struct{}
	wmu   sync.Mutex
	ended chan struct{}
}

func (c *fakeCursor) write(kind byte, payload []byte) {
	<-c.ready
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = wire.WriteFrame(c.st, kind, payload)
}

// fakeInput is what an input stream of the fake agent received.
type fakeInput struct {
	hdrGen uint64
	events []wire.InputEvent
	acks   []wire.InputAck
	ended  bool
}

// fakeAgent serves video, cursor and input streams and answers
// control.set_writer and time.sync with the rules of docs/protocol.md.
type fakeAgent struct {
	mu      sync.Mutex
	state   conn.ConnState
	connGen uint64
	writer  uint64
	held    map[string]bool
	videos  []*fakeVideo
	cursors []*fakeCursor
	inputs  []*fakeInput
	// refuseVideo answers a video stream of a display with a refusal of
	// that code.
	refuseVideo map[int]string
	// timeSync answers time.sync; nil answers CAPABILITY_UNSUPPORTED.
	timeSync func() uint64
	syncs    int
	// inputHook runs once on the next input stream: with stage "open"
	// before its header is checked and with "accepted" after the header was
	// accepted, before the acceptance line is written.
	inputHook func(stage string)
	// changed is closed and replaced whenever the state changes.
	changed chan struct{}
}

func newFakeAgent() *fakeAgent {
	return &fakeAgent{state: conn.StateReady, connGen: 1, held: map[string]bool{}, refuseVideo: map[int]string{}, changed: make(chan struct{})}
}

// setState changes the connection state; open streams stay open.
func (a *fakeAgent) setState(st conn.ConnState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = st
	close(a.changed)
	a.changed = make(chan struct{})
}

// waitReady waits, as a connection does, until the state is ready or
// degraded or ctx ends; a.mu is held and held again on return.
func (a *fakeAgent) waitReadyLocked(ctx context.Context) bool {
	for a.state != conn.StateReady && a.state != conn.StateDegraded {
		ch := a.changed
		a.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			a.mu.Lock()
			return false
		}
		a.mu.Lock()
	}
	return true
}

// hook runs the input hook of stage.
func (a *fakeAgent) hook(stage string) {
	a.mu.Lock()
	f := a.inputHook
	a.mu.Unlock()
	if f != nil {
		f(stage)
	}
}

func (a *fakeAgent) State() conn.ConnState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *fakeAgent) Generation() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.connGen
}

func (a *fakeAgent) Call(_ context.Context, op string, params proto.Message, _ conn.CallOpts) (proto.Message, error) {
	a.mu.Lock()
	switch op {
	case "control.set_writer":
		defer a.mu.Unlock()
		g, _ := params["gen"].(uint64)
		a.raiseLocked(g)
		return proto.Message{"gen": float64(a.writer)}, nil
	case "time.sync":
		f := a.timeSync
		a.syncs++
		a.mu.Unlock()
		if f == nil {
			return nil, apierr.New(wire.CodeCapabilityUnsupported, "unknown op 'time.sync'", false, "")
		}
		return proto.Message{"mono_us": float64(f())}, nil
	}
	a.mu.Unlock()
	return nil, apierr.New(wire.CodeCapabilityUnsupported, "unknown op '"+op+"'", false, "")
}

// raiseLocked raises the writer generation to gen, releasing every held
// key; a.mu is held.
func (a *fakeAgent) raiseLocked(gen uint64) {
	if gen > a.writer {
		a.writer = gen
		a.held = map[string]bool{}
	}
}

func (a *fakeAgent) OpenStream(ctx context.Context, hdr wire.StreamHeader) (conn.DataStream, uint64, error) {
	a.mu.Lock()
	if hdr.S == "input" && !a.waitReadyLocked(ctx) {
		a.mu.Unlock()
		return nil, 0, ctx.Err()
	}
	if a.state != conn.StateReady && a.state != conn.StateDegraded {
		a.mu.Unlock()
		return nil, 0, apierr.New(wire.CodeOffline, "pc is offline", true, apierr.NextHostConnect)
	}
	cg := a.connGen
	d, ag := duplex()
	switch hdr.S {
	case "video":
		if code := a.refuseVideo[hdr.Display]; code != "" {
			a.mu.Unlock()
			go func() {
				_ = wire.WriteHeader(ag, map[string]any{"ok": false, "code": code, "error": "refused by the fake agent"})
				_ = ag.Close()
			}()
			return d, cg, nil
		}
		v := &fakeVideo{hdr: hdr, st: ag, ready: make(chan struct{}), controls: make(chan wire.VideoControl, 256), ended: make(chan struct{})}
		a.videos = append(a.videos, v)
		a.mu.Unlock()
		go v.readControls()
		go func() {
			v.wmu.Lock()
			_ = wire.WriteFrame(v.st, wire.KindVideoInfo, infoPayload(1920, 1080, hdr.Display))
			v.wmu.Unlock()
			close(v.ready)
		}()
	case "cursor":
		c := &fakeCursor{hdr: hdr, st: ag, ready: make(chan struct{}), ended: make(chan struct{})}
		a.cursors = append(a.cursors, c)
		a.mu.Unlock()
		go func() {
			c.wmu.Lock()
			_ = wire.WriteHeader(ag, map[string]any{"ok": true})
			c.wmu.Unlock()
			close(c.ready)
			_, _ = io.Copy(io.Discard, ag)
			close(c.ended)
			c.wmu.Lock()
			_ = ag.CloseWrite()
			c.wmu.Unlock()
		}()
	case "input":
		a.mu.Unlock()
		go a.serveInput(ag, hdr)
	default:
		a.mu.Unlock()
		return nil, 0, fmt.Errorf("the fake agent has no %s stream", hdr.S)
	}
	return d, cg, nil
}

func (a *fakeAgent) serveInput(st pipeEnd, hdr wire.StreamHeader) {
	defer st.Close()
	defer func() {
		a.mu.Lock()
		a.inputHook = nil
		a.mu.Unlock()
	}()
	a.hook("open")
	a.mu.Lock()
	if hdr.Gen < a.writer {
		cur := a.writer
		a.mu.Unlock()
		_ = wire.WriteHeader(st, map[string]any{"ok": false, "code": wire.CodeControlPreempted,
			"error": fmt.Sprintf("writer generation %d is older than the current %d", hdr.Gen, cur)})
		return
	}
	a.raiseLocked(hdr.Gen)
	fs := &fakeInput{hdrGen: hdr.Gen}
	a.inputs = append(a.inputs, fs)
	a.mu.Unlock()
	a.hook("accepted")
	a.mu.Lock()
	a.inputHook = nil
	cur := a.writer
	a.mu.Unlock()
	if wire.WriteHeader(st, map[string]any{"ok": true, "gen": cur}) != nil {
		return
	}
	defer func() {
		a.mu.Lock()
		fs.ended = true
		a.mu.Unlock()
	}()
	for {
		kind, payload, err := wire.ReadFrame(st, 4<<20)
		if err != nil {
			return
		}
		if kind != wire.KindClientInput {
			continue
		}
		var ev wire.InputEvent
		if json.Unmarshal(payload, &ev) != nil {
			continue
		}
		a.mu.Lock()
		fs.events = append(fs.events, ev)
		a.raiseLocked(ev.Gen)
		ack := wire.InputAck{Seq: ev.Seq, OK: true, DoneMonoUS: 1}
		if ev.Gen < a.writer {
			ack = wire.InputAck{Seq: ev.Seq, Code: wire.CodeControlPreempted, Error: "older writer generation"}
		} else {
			switch ev.K {
			case "key":
				k := fmt.Sprintf("sc:%#x", ev.SC)
				if ev.Down {
					a.held[k] = true
				} else {
					delete(a.held, k)
				}
			case "button":
				k := "b:" + ev.B
				if ev.Down {
					a.held[k] = true
				} else {
					delete(a.held, k)
				}
			case "release_all":
				a.held = map[string]bool{}
			}
		}
		fs.acks = append(fs.acks, ack)
		a.mu.Unlock()
		b, _ := json.Marshal(ack)
		if wire.WriteFrame(st, wire.KindInputAck, b) != nil {
			return
		}
	}
}

func (a *fakeAgent) videoCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.videos)
}

func (a *fakeAgent) video(i int) *fakeVideo {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i >= len(a.videos) {
		return nil
	}
	return a.videos[i]
}

func (a *fakeAgent) cursor(i int) *fakeCursor {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i >= len(a.cursors) {
		return nil
	}
	return a.cursors[i]
}

// inputEvents returns every event the input streams received, in order of
// the streams.
func (a *fakeAgent) inputEvents() []wire.InputEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []wire.InputEvent
	for _, s := range a.inputs {
		out = append(out, s.events...)
	}
	return out
}

func (a *fakeAgent) inputAcks() []wire.InputAck {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []wire.InputAck
	for _, s := range a.inputs {
		out = append(out, s.acks...)
	}
	return out
}

func (a *fakeAgent) heldKeys() map[string]bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]bool{}
	for k := range a.held {
		out[k] = true
	}
	return out
}

func (a *fakeAgent) syncCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.syncs
}

// fakeAgents serves one host, "pc", to the hub and to the control manager.
type fakeAgents struct{ a *fakeAgent }

func (f fakeAgents) Agent(hostID string) Agent {
	if hostID != "pc" || f.a == nil {
		return nil
	}
	return f.a
}

type controlAgents struct{ a *fakeAgent }

func (f controlAgents) Agent(hostID string) control.Agent {
	if hostID != "pc" || f.a == nil {
		return nil
	}
	return f.a
}

// fakeClock is a daemon clock that moves only when a test moves it.
type fakeClock struct{ us atomic.Int64 }

func (c *fakeClock) now() int64        { return c.us.Load() }
func (c *fakeClock) advance(us int64)  { c.us.Add(us) }
func (c *fakeClock) set(us int64)      { c.us.Store(us) }
func newFakeClock(us int64) *fakeClock { c := &fakeClock{}; c.set(us); return c }

// rig is a hub over a control manager, a temporary store and the fake
// agent of "pc".
type rig struct {
	t     *testing.T
	st    *store.Store
	bus   *events.Bus
	agent *fakeAgent
	ctl   *control.Manager
	hub   *Hub
}

type rigOpt func(*Options)

func newRig(t *testing.T, opts ...rigOpt) *rig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "deskhand.db"))
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, st: st, bus: events.NewBus(st, nil), agent: newFakeAgent()}
	r.ctl = control.New(control.Options{Store: st, Bus: r.bus, Agents: controlAgents{r.agent}})
	if err := r.ctl.Load(); err != nil {
		t.Fatal(err)
	}
	o := Options{Agents: fakeAgents{r.agent}, Control: r.ctl, Bus: r.bus}
	for _, f := range opts {
		f(&o)
	}
	r.hub = New(o)
	t.Cleanup(func() {
		r.hub.Close()
		r.ctl.Close()
		st.Close()
	})
	return r
}

var (
	human  = model.Caller{Actor: model.ActorHuman, ClientID: "c-app-1", ClientName: "Deskhand"}
	human2 = model.Caller{Actor: model.ActorHuman, ClientID: "c-app-2", ClientName: "Deskhand 2"}
	cli    = model.Caller{Actor: model.ActorHuman, ClientID: "c-cli", ClientName: "deskhand-cli"}
	aiA    = model.Caller{Actor: model.ActorAI, ClientID: "c-mcp-a", ClientName: "deskhand-mcp/claude-code"}
)

// clientFrame is a frame a viewer received.
type clientFrame struct {
	kind    byte
	payload []byte
}

// viewerClient is the app's end of a screen stream.
type viewerClient struct {
	t      *testing.T
	end    pipeEnd
	frames chan clientFrame
	ran    chan struct{}
	once   sync.Once
}

// attach attaches a viewer that reads its frames as they come.
func (r *rig) attach(display int, p Preset, who model.Caller) *viewerClient {
	r.t.Helper()
	c := r.attachSlow(display, p, who)
	c.startReading()
	return c
}

// attachSlow attaches a viewer that reads nothing until startReading.
func (r *rig) attachSlow(display int, p Preset, who model.Caller) *viewerClient {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v, err := r.hub.Attach(ctx, "pc", display, p, who)
	if err != nil {
		r.t.Fatalf("attach display %d: %v", display, err)
	}
	daemonEnd, clientEnd := duplex()
	c := &viewerClient{t: r.t, end: clientEnd, frames: make(chan clientFrame, 8192), ran: make(chan struct{})}
	go func() {
		defer close(c.ran)
		v.Run(daemonEnd)
	}()
	r.t.Cleanup(c.close)
	return c
}

func (c *viewerClient) startReading() {
	go func() {
		br := bufio.NewReader(c.end)
		for {
			kind, payload, err := wire.ReadFrame(br, wire.MaxFrame)
			if err != nil {
				close(c.frames)
				return
			}
			c.frames <- clientFrame{kind: kind, payload: payload}
		}
	}()
}

// next waits up to 5 s for the next frame.
func (c *viewerClient) next() clientFrame {
	c.t.Helper()
	select {
	case f, ok := <-c.frames:
		if !ok {
			c.t.Fatal("the screen stream ended")
		}
		return f
	case <-time.After(5 * time.Second):
		c.t.Fatal("no frame within 5 s")
	}
	return clientFrame{}
}

// nextOf waits for the next frame of kind, skipping others.
func (c *viewerClient) nextOf(kind byte) clientFrame {
	c.t.Helper()
	for {
		if f := c.next(); f.kind == kind {
			return f
		}
	}
}

// nextAU waits for the next access unit, skipping other frames.
func (c *viewerClient) nextAU() wire.AU {
	c.t.Helper()
	f := c.nextOf(wire.KindAU)
	a, err := wire.UnmarshalAU(f.payload)
	if err != nil {
		c.t.Fatal(err)
	}
	return a
}

// notice waits for the next notice of type typ, skipping other frames.
func (c *viewerClient) notice(typ string) map[string]any {
	c.t.Helper()
	for {
		f := c.nextOf(wire.KindNotice)
		var m map[string]any
		if err := json.Unmarshal(f.payload, &m); err != nil {
			c.t.Fatalf("notice %q: %v", f.payload, err)
		}
		if m["type"] == typ {
			return m
		}
	}
}

// quiet reports whether no frame of kind arrives within d.
func (c *viewerClient) quiet(kind byte, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case f, ok := <-c.frames:
			if !ok {
				return true
			}
			if f.kind == kind {
				return false
			}
		case <-timer.C:
			return true
		}
	}
}

func (c *viewerClient) send(kind byte, v any) {
	c.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := wire.WriteFrame(c.end, kind, b); err != nil {
		c.t.Fatal(err)
	}
}

func (c *viewerClient) input(ev map[string]any) { c.send(wire.KindClientInput, ev) }

// close ends the viewer's connection and waits for its stream to end.
func (c *viewerClient) close() {
	c.once.Do(func() {
		_ = c.end.Close()
		select {
		case <-c.ran:
		case <-time.After(5 * time.Second):
			c.t.Error("the viewer's stream did not end within 5 s of its close")
		}
	})
}

// eventually waits up to 5 s for cond.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
