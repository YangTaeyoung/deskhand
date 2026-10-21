// Package screen relays the hosts' screens to the local API's screen
// streams (docs/local-api.md §4): one agent video and cursor stream per
// host and display, shared by every viewer of that display, and the human
// input of the viewers to the host's agent under control arbitration.
package screen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/daemon/events"
	"github.com/YangTaeyoung/deskhand/internal/daemon/model"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// Agent is the connection to a host's agent.
type Agent interface {
	Call(ctx context.Context, op string, params proto.Message, opts conn.CallOpts) (proto.Message, error)
	OpenStream(ctx context.Context, hdr wire.StreamHeader) (conn.DataStream, uint64, error)
	State() conn.ConnState
	Generation() uint64
}

// Agents finds the agent connection of a host.
type Agents interface {
	// Agent returns the connection of hostID, or nil when there is none.
	Agent(hostID string) Agent
}

// AgentsFrom returns the Agents of the daemon's connection manager.
func AgentsFrom(m *conn.Manager) Agents { return connAgents{m: m} }

type connAgents struct{ m *conn.Manager }

func (c connAgents) Agent(hostID string) Agent {
	if h := c.m.Get(hostID); h != nil {
		return h
	}
	return nil
}

// Control is the control arbitration the human input goes through
// (control.Manager).
type Control interface {
	Get(hostID string) model.ControlState
	HumanGen(hostID string) uint64
	CheckHuman(hostID string, c model.Caller) (uint64, error)
	Resync(hostID string, tried uint64) bool
}

// Options configure a Hub.
type Options struct {
	Agents  Agents
	Control Control
	// Bus delivers the control, connection, display and session events the
	// viewers follow; nil follows none.
	Bus    *events.Bus
	Logger *log.Logger
	// Grace is how long a display's agent streams stay open without
	// viewers (5 s when 0).
	Grace time.Duration
	// KeyframeEvery is the time between two keyframe requests while a
	// display's agent stream is open (30 s when 0).
	KeyframeEvery time.Duration
	// LatencyEvery is the time between two latency notices (1 s when 0).
	LatencyEvery time.Duration
	// InputIdle is how long the human input stream of a host stays open
	// without input while no viewer holds a key or button (30 s when 0).
	InputIdle time.Duration
	// QueueFrames and QueueBytes bound the frames waiting for one viewer
	// (1024 frames and 16 MiB when 0).
	QueueFrames int
	QueueBytes  int
	// Clock reads the daemon's monotonic clock in µs; nil reads the time
	// since New.
	Clock func() int64
}

const (
	defaultGrace         = 5 * time.Second
	defaultKeyframeEvery = 30 * time.Second
	defaultLatencyEvery  = time.Second
	defaultInputIdle     = 30 * time.Second
	defaultQueueFrames   = 1024
	defaultQueueBytes    = 16 << 20
)

// Preset is the stream quality a viewer asks for.
type Preset string

// Presets.
const (
	PresetSharp    Preset = "sharp"
	PresetBalanced Preset = "balanced"
	PresetSaver    Preset = "saver"
)

// quality is what the viewers ask of the agent's video stream: max_width
// (0 for the display's width), the bitrate in bps and max_fps (0 for the
// agent's default).
type quality struct {
	rank     int
	maxWidth int
	bitrate  int
	maxFPS   int
}

// Frame rate bounds of protocol.md §2 (video max_fps); 0 is the agent's
// default of defaultFPS.
const (
	maxFPSLimit = 240
	defaultFPS  = 60
)

// CheckMaxFPS reports whether fps is a max_fps a viewer may ask for.
func CheckMaxFPS(fps int) error {
	if fps < 0 || fps > maxFPSLimit {
		return apierr.Invalid(fmt.Sprintf("max_fps %d는 쓸 수 없습니다. max_fps는 1–%d 사이로 주거나, 기본값을 쓰려면 생략하세요", fps, maxFPSLimit))
	}
	return nil
}

var qualities = map[Preset]quality{
	PresetSharp:    {rank: 3, maxWidth: 0, bitrate: 20_000_000},
	PresetBalanced: {rank: 2, maxWidth: 2560, bitrate: 12_000_000},
	PresetSaver:    {rank: 1, maxWidth: 1920, bitrate: 4_000_000},
}

// ParsePreset returns the preset p names, PresetBalanced for "".
func ParsePreset(p string) (Preset, error) {
	if p == "" {
		return PresetBalanced, nil
	}
	if _, ok := qualities[Preset(p)]; !ok {
		return "", apierr.Invalid(fmt.Sprintf(`preset %q를 모릅니다. preset은 "sharp", "balanced", "saver" 중 하나로 주세요`, p))
	}
	return Preset(p), nil
}

// feedKey names the agent streams of one display of a host.
type feedKey struct {
	hostID  string
	display int
}

// Hub relays the screens of the hosts.
type Hub struct {
	opt   Options
	lg    *log.Logger
	clock func() int64

	base context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup
	sub  *events.Sub

	mu     sync.Mutex
	feeds  map[feedKey]*feed
	links  map[string]*inputLink
	closed bool

	clockMu sync.Mutex
	clocks  map[string]*hostClock
}

// New returns a Hub that follows opt.Bus.
func New(opt Options) *Hub {
	lg := opt.Logger
	if lg == nil {
		lg = log.New(io.Discard, "", 0)
	}
	if opt.Grace <= 0 {
		opt.Grace = defaultGrace
	}
	if opt.KeyframeEvery <= 0 {
		opt.KeyframeEvery = defaultKeyframeEvery
	}
	if opt.LatencyEvery <= 0 {
		opt.LatencyEvery = defaultLatencyEvery
	}
	if opt.InputIdle <= 0 {
		opt.InputIdle = defaultInputIdle
	}
	if opt.QueueFrames <= 0 {
		opt.QueueFrames = defaultQueueFrames
	}
	if opt.QueueBytes <= 0 {
		opt.QueueBytes = defaultQueueBytes
	}
	h := &Hub{opt: opt, lg: lg, clock: opt.Clock, feeds: map[feedKey]*feed{}, links: map[string]*inputLink{}, clocks: map[string]*hostClock{}}
	if h.clock == nil {
		start := time.Now()
		h.clock = func() int64 { return time.Since(start).Microseconds() }
	}
	h.base, h.stop = context.WithCancel(context.Background())
	if opt.Bus != nil {
		h.sub = opt.Bus.Subscribe()
		h.wg.Add(1)
		go h.watch(h.sub)
	}
	return h
}

// Close ends every viewer and agent stream and waits for them.
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	feeds := make([]*feed, 0, len(h.feeds))
	for _, f := range h.feeds {
		feeds = append(feeds, f)
	}
	links := make([]*inputLink, 0, len(h.links))
	for _, l := range h.links {
		links = append(links, l)
	}
	h.mu.Unlock()
	h.stop()
	if h.sub != nil {
		h.sub.Close()
	}
	for _, f := range feeds {
		f.shutdown()
	}
	for _, l := range links {
		l.close()
	}
	h.wg.Wait()
}

func (h *Hub) agent(hostID string) Agent {
	if h.opt.Agents == nil {
		return nil
	}
	return h.opt.Agents.Agent(hostID)
}

// errClosed refuses attaches after Close.
var errClosed = apierr.New(wire.CodeInternal, "Deskhand 데몬이 끝나는 중입니다. 데몬을 다시 시작한 뒤 다시 붙으세요", true, apierr.NextRetry)

// Attach attaches a viewer of client c to display of hostID at preset and
// maxFPS (0 for the default):
// the display's agent stream is opened for the first viewer and shared
// by the others. The viewer runs once Run is given its connection.
func (h *Hub) Attach(ctx context.Context, hostID string, display int, preset Preset, maxFPS int, c model.Caller) (*Viewer, error) {
	p, err := ParsePreset(string(preset))
	if err != nil {
		return nil, err
	}
	if err := CheckMaxFPS(maxFPS); err != nil {
		return nil, err
	}
	if display < 0 {
		return nil, apierr.Invalid(fmt.Sprintf("display %d는 쓸 수 없습니다. 0 이상의 디스플레이 번호를 주세요", display))
	}
	if h.agent(hostID) == nil {
		return nil, apierr.NotFound(fmt.Sprintf("PC %s가 등록돼 있지 않습니다. host.list로 PC를 확인하세요", hostID))
	}
	v := newViewer(h, hostID, c, p)
	v.maxFPS = maxFPS
	key := feedKey{hostID: hostID, display: display}
	for {
		f, err := h.feedFor(key)
		if err != nil {
			return nil, err
		}
		err = f.join(ctx, v)
		if err == errFeedGone {
			continue
		}
		if err != nil {
			return nil, err
		}
		return v, nil
	}
}

// feedFor returns the feed of key, adding it.
func (h *Hub) feedFor(key feedKey) (*feed, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, errClosed
	}
	f := h.feeds[key]
	if f == nil {
		f = newFeed(h, key)
		h.feeds[key] = f
	}
	return f, nil
}

// hostFeeds returns the feeds of hostID.
func (h *Hub) hostFeeds(hostID string) []*feed {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*feed
	for k, f := range h.feeds {
		if k.hostID == hostID {
			out = append(out, f)
		}
	}
	return out
}

// removeFeed forgets f once it has neither viewers nor attaches waiting,
// and reports whether it did.
func (h *Hub) removeFeed(f *feed) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.viewers) > 0 || len(f.waiting) > 0 {
		return false
	}
	f.dead = true
	f.stopGraceLocked()
	if h.feeds[f.key] == f {
		delete(h.feeds, f.key)
	}
	return true
}

// watch follows the events of sub for the viewers until the hub closes.
func (h *Hub) watch(sub *events.Sub) {
	defer h.wg.Done()
	for {
		select {
		case <-h.base.Done():
			return
		case ev := <-sub.C:
			h.event(ev)
		}
	}
}

// event hands one bus event to the feeds of its host: control changes
// and connection states as notices, a display list without a feed's
// display as a no_output pause, and a session change as a keyframe
// request.
func (h *Hub) event(ev events.Event) {
	if ev.HostID == "" {
		return
	}
	switch ev.Kind {
	case events.ControlChanged:
		n := map[string]any{"type": noticeOwnerChanged}
		for k, v := range ev.Fields {
			n[k] = v
		}
		h.noticeHost(ev.HostID, n)
	case events.ConnectionState:
		n := map[string]any{"type": noticeConnectionState, "state": ev.Fields["state"]}
		if r, ok := ev.Fields["reason"].(string); ok && r != "" {
			n["reason"] = r
		}
		h.noticeHost(ev.HostID, n)
	case events.DisplayChanged:
		present := displayIndexes(ev.Fields["displays"])
		for _, f := range h.hostFeeds(ev.HostID) {
			if present[f.key.display] {
				f.requestKeyframe()
			} else {
				f.pauseNoOutput()
			}
		}
	case events.SessionChanged:
		for _, f := range h.hostFeeds(ev.HostID) {
			f.requestKeyframe()
		}
	}
}

// displayIndexes returns the index of each display of a display list.
func displayIndexes(v any) map[int]bool {
	out := map[int]bool{}
	list, _ := v.([]any)
	for _, d := range list {
		m, ok := d.(map[string]any)
		if !ok {
			continue
		}
		switch n := m["index"].(type) {
		case float64:
			out[int(n)] = true
		case int:
			out[n] = true
		}
	}
	return out
}

// Notice types of the 0x30 frames (docs/local-api.md §4).
const (
	noticeLatency         = "latency"
	noticeControlDenied   = "control_denied"
	noticeOwnerChanged    = "owner_changed"
	noticeConnectionState = "connection_state"
)

// noticeFrame returns the 0x30 frame of notice n.
func noticeFrame(n any) []byte {
	b, err := json.Marshal(n)
	if err != nil {
		return nil
	}
	return encodeFrame(wire.KindNotice, b)
}

// noticeHost sends notice n to every viewer of hostID.
func (h *Hub) noticeHost(hostID string, n any) {
	frame := noticeFrame(n)
	if frame == nil {
		return
	}
	for _, f := range h.hostFeeds(hostID) {
		f.broadcastNotice(frame)
	}
}

// hostViewers returns the viewers of hostID.
func (h *Hub) hostViewers(hostID string) []*Viewer {
	var out []*Viewer
	for _, f := range h.hostFeeds(hostID) {
		f.mu.Lock()
		for v := range f.viewers {
			out = append(out, v)
		}
		f.mu.Unlock()
	}
	return out
}

// CloseAIViewers ends the viewers of AI clients on hostID; each viewer's
// connection closes.
func (h *Hub) CloseAIViewers(hostID string) int {
	n := 0
	for _, v := range h.hostViewers(hostID) {
		if v.caller.Actor == model.ActorAI {
			v.end()
			n++
		}
	}
	return n
}
