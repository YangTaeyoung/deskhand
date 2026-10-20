package screen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/agent/encode"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// fakeClock is a Clock whose time passes only when Sleep is called. hook,
// when set, sees each new reading on the goroutine that sleeps.
type fakeClock struct {
	mu   sync.Mutex
	us   uint64
	hook func(now uint64)
}

func (c *fakeClock) NowUS() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.us
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	if d > 0 {
		c.us += uint64(d / time.Microsecond)
	}
	now, hook := c.us, c.hook
	c.mu.Unlock()
	if hook != nil {
		hook(now)
	}
	return ctx.Err()
}

// plan answers one Next of a fakeSource at now: the frame or error to
// return and how long the call takes.
type plan func(s *fakeSource, now uint64, timeout time.Duration) (capture.Frame, error, time.Duration)

// fakeSource is a capture.Source that follows its plan on its clock.
type fakeSource struct {
	clk  Clock
	plan plan

	mu     sync.Mutex
	w, h   int
	closed bool
	// next is the time of the next image of a periodic plan; gave counts
	// the images given.
	next uint64
	gave int
}

func (s *fakeSource) Next(ctx context.Context, timeout time.Duration) (capture.Frame, error) {
	if err := ctx.Err(); err != nil {
		return capture.Frame{}, err
	}
	f, err, wait := s.plan(s, s.clk.NowUS(), timeout)
	if serr := s.clk.Sleep(ctx, wait); serr != nil {
		return capture.Frame{}, serr
	}
	if err == nil {
		s.mu.Lock()
		f.Width, f.Height = s.w, s.h
		s.mu.Unlock()
		f.MonoUS, f.Dirty, f.Texture = s.clk.NowUS(), true, 1
	}
	return f, err
}

func (s *fakeSource) Device() *d3d.Device { return &d3d.Device{} }

func (s *fakeSource) Size() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w, s.h
}

func (s *fakeSource) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

func (s *fakeSource) resize(w, h int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.w, s.h = w, h
}

// static gives one image and then no new one.
func static(s *fakeSource, _ uint64, timeout time.Duration) (capture.Frame, error, time.Duration) {
	if s.gave == 0 {
		s.gave++
		return capture.Frame{}, nil, 0
	}
	return capture.Frame{}, capture.ErrNoNewFrame, timeout
}

// periodic gives an image every interval.
func periodic(interval time.Duration) plan {
	step := uint64(interval / time.Microsecond)
	return func(s *fakeSource, now uint64, timeout time.Duration) (capture.Frame, error, time.Duration) {
		if s.gave == 0 {
			s.next = now
		}
		wait := time.Duration(0)
		if s.next > now {
			wait = time.Duration(s.next-now) * time.Microsecond
		}
		if wait > timeout {
			return capture.Frame{}, capture.ErrNoNewFrame, timeout
		}
		s.gave++
		s.next += step
		return capture.Frame{}, nil, wait
	}
}

// opener opens fake sources on clk with plan p, w×h at first; fail, when
// set, answers an Open instead while it returns an error.
type opener struct {
	clk  Clock
	p    plan
	w, h int
	fail func(n int) error

	mu      sync.Mutex
	opened  []*fakeSource
	calls   int
	display []int
}

func (o *opener) open(display int) (capture.Source, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls++
	o.display = append(o.display, display)
	if o.fail != nil {
		if err := o.fail(o.calls); err != nil {
			return nil, err
		}
	}
	s := &fakeSource{clk: o.clk, plan: o.p, w: o.w, h: o.h}
	o.opened = append(o.opened, s)
	return s, nil
}

func (o *opener) sources() []*fakeSource {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]*fakeSource(nil), o.opened...)
}

// fakeEncoder is an encode.Encoder that turns each frame into a one-NAL
// access unit at once: an IDR with parameter sets for the first frame and
// for a forced key frame, a P slice otherwise.
type fakeEncoder struct {
	clk                         Clock
	srcW, srcH, outW, outH, fps int
	bps                         int
	out                         chan wire.AU
	// written, when set, is signalled once an access unit is written;
	// Encode waits for it.
	written chan struct{}
	// hold, when set, keeps each access unit until it closes.
	hold chan struct{}

	mu       sync.Mutex
	frames   int
	bitrates []int
	closed   bool
}

func (e *fakeEncoder) Encode(f capture.Frame, forceKey bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errors.New("closed")
	}
	if f.Width != e.srcW || f.Height != e.srcH {
		return fmt.Errorf("frame %dx%d for a %dx%d encoder", f.Width, f.Height, e.srcW, e.srcH)
	}
	key := forceKey || e.frames == 0
	e.frames++
	data := []byte{0, 0, 0, 1, 0x41, 0x9a}
	if key {
		data = []byte{0, 0, 0, 1, 0x67, 0x64, 0, 0, 0, 1, 0x68, 0xee, 0, 0, 0, 1, 0x65, 0x88}
	}
	au := wire.AU{Key: key, HasConfig: key, Width: uint32(e.outW), Height: uint32(e.outH), CaptureMonoUS: f.MonoUS,
		EncodeDoneMonoUS: e.clk.NowUS() + 2000, Data: data}
	if e.hold != nil {
		go func() {
			<-e.hold
			e.mu.Lock()
			defer e.mu.Unlock()
			if !e.closed {
				e.out <- au
			}
		}()
		return nil
	}
	e.out <- au
	if e.written != nil {
		e.mu.Unlock()
		select {
		case <-e.written:
		case <-time.After(time.Second):
		}
		e.mu.Lock()
	}
	return nil
}

func (e *fakeEncoder) Output() <-chan wire.AU { return e.out }

func (e *fakeEncoder) SetBitrate(bps int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.bitrates = append(e.bitrates, bps)
	return nil
}

func (e *fakeEncoder) Info() encode.EncoderInfo {
	return encode.EncoderInfo{Name: "fake", Hardware: true, Width: e.outW, Height: e.outH}
}

func (e *fakeEncoder) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.closed {
		e.closed = true
		close(e.out)
	}
}

type encoders struct {
	clk     Clock
	written chan struct{}
	hold    chan struct{}
	mu      sync.Mutex
	made    []*fakeEncoder
}

func (es *encoders) new(_ *d3d.Device, srcW, srcH, outW, outH, fps, bps int) (encode.Encoder, error) {
	e := &fakeEncoder{clk: es.clk, srcW: srcW, srcH: srcH, outW: outW, outH: outH, fps: fps, bps: bps, out: make(chan wire.AU, 4096), written: es.written, hold: es.hold}
	es.mu.Lock()
	defer es.mu.Unlock()
	es.made = append(es.made, e)
	return e, nil
}

func (es *encoders) list() []*fakeEncoder {
	es.mu.Lock()
	defer es.mu.Unlock()
	return append([]*fakeEncoder(nil), es.made...)
}

// recorded is one frame the pipeline wrote.
type recorded struct {
	kind    byte
	payload []byte
}

type recorder struct {
	mu     sync.Mutex
	frames []recorded
	// written, when set, is signalled after each access unit.
	written chan struct{}
}

func (r *recorder) out(kind byte, payload []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, recorded{kind, append([]byte(nil), payload...)})
	if kind == wire.KindAU && r.written != nil {
		select {
		case r.written <- struct{}{}:
		default:
		}
	}
	return nil
}

// all returns the frames written, without the stats frames unless stats
// is set.
func (r *recorder) all(stats bool) []recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []recorded
	for _, f := range r.frames {
		if stats || f.kind != wire.KindVideoStats {
			out = append(out, f)
		}
	}
	return out
}

func (r *recorder) aus(t *testing.T) []wire.AU {
	t.Helper()
	var aus []wire.AU
	for _, f := range r.all(false) {
		if f.kind == wire.KindAU {
			au, err := wire.UnmarshalAU(f.payload)
			if err != nil {
				t.Fatal(err)
			}
			aus = append(aus, au)
		}
	}
	return aus
}

func decode[T any](t *testing.T, f recorded) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(f.payload, &v); err != nil {
		t.Fatalf("frame kind %#x: %v (%s)", f.kind, err, f.payload)
	}
	return v
}

// describe writes the frames as kinds with the details that matter: info
// sizes, AU key flags and capture times, paused reasons.
func describe(t *testing.T, frames []recorded) string {
	t.Helper()
	s := ""
	for _, f := range frames {
		switch f.kind {
		case wire.KindVideoInfo:
			i := decode[wire.VideoInfo](t, f)
			s += fmt.Sprintf(" info(%dx%d)", i.Width, i.Height)
		case wire.KindAU:
			au, _ := wire.UnmarshalAU(f.payload)
			k := "p"
			if au.Key {
				k = "K"
			}
			s += fmt.Sprintf(" %s@%dms", k, au.CaptureMonoUS/1000)
		case wire.KindVideoPaused:
			s += " paused(" + decode[wire.VideoPaused](t, f).Reason + ")"
		case wire.KindVideoStats:
			s += " stats"
		default:
			s += fmt.Sprintf(" kind%#x", f.kind)
		}
	}
	return s
}

// harness runs a pipeline on a fake clock until the clock reaches stopAt
// or the test's hook cancels it. Its encoders return from Encode once
// the access unit is written.
type harness struct {
	clk  *fakeClock
	op   *opener
	encs *encoders
	rec  *recorder
	p    *Pipeline
}

func newHarness(p plan, w, h int) *harness {
	clk := &fakeClock{}
	written := make(chan struct{}, 1)
	hs := &harness{clk: clk, op: &opener{clk: clk, p: p, w: w, h: h}, encs: &encoders{clk: clk, written: written}, rec: &recorder{written: written}}
	hs.p = New(Options{Open: hs.op.open, NewEncoder: hs.encs.new, Clock: clk})
	return hs
}

// run starts the pipeline with h and stops it at stopAt µs; at calls each
// function of at once when the clock passes its time in µs.
func (hs *harness) run(t *testing.T, h wire.StreamHeader, stopAt uint64, at map[uint64]func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := map[uint64]bool{}
	hs.clk.hook = func(now uint64) {
		for when, fn := range at {
			if now >= when && !done[when] {
				done[when] = true
				fn()
			}
		}
		if now >= stopAt {
			cancel()
		}
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.p.Start(ctx, h, hs.rec.out) }()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the pipeline did not stop")
	}
}

func TestPacingCapsFPS(t *testing.T) {
	hs := newHarness(periodic(time.Second/240), 1920, 1080)
	hs.run(t, wire.StreamHeader{S: "video", MaxFPS: 30}, 3_000_000, nil)
	perSecond := map[uint64]int{}
	for _, au := range hs.rec.aus(t) {
		perSecond[au.CaptureMonoUS/1_000_000]++
	}
	t.Logf("access units per second of capture time: %v", perSecond)
	for s := uint64(0); s < 3; s++ {
		if n := perSecond[s]; n > 31 || n < 28 {
			t.Errorf("second %d has %d access units at max_fps 30", s, n)
		}
	}
}

func TestStaticScreenSendsKeepaliveEvery10s(t *testing.T) {
	hs := newHarness(static, 1920, 1080)
	hs.run(t, wire.StreamHeader{S: "video"}, 35_000_000, nil)
	aus := hs.rec.aus(t)
	t.Logf("frames:%s", describe(t, hs.rec.all(false)))
	if len(aus) != 4 {
		t.Fatalf("%d access units in 35 s of a static screen, want 4", len(aus))
	}
	if !aus[0].Key || !aus[0].HasConfig {
		t.Fatalf("the first access unit is not a key frame with parameter sets: %+v", aus[0])
	}
	for i := 1; i < len(aus); i++ {
		gap := aus[i].CaptureMonoUS - aus[i-1].CaptureMonoUS
		if aus[i].Key || gap < 10_000_000 || gap > 10_100_000 {
			t.Errorf("access unit %d: key %v, %d µs after the one before", i, aus[i].Key, gap)
		}
	}
	if n := len(hs.encs.list()); n != 1 {
		t.Errorf("%d encoders for one static stream", n)
	}
	stats := 0
	for _, f := range hs.rec.all(true) {
		if f.kind == wire.KindVideoStats {
			stats++
		}
	}
	if stats < 33 || stats > 36 {
		t.Errorf("%d stats frames in 35 s", stats)
	}
}

func TestPausedThenResumesWithKeyframe(t *testing.T) {
	frames := periodic(time.Second / 30)
	resumed := false
	p := func(s *fakeSource, now uint64, timeout time.Duration) (capture.Frame, error, time.Duration) {
		switch {
		case now >= 1_000_000 && now < 3_000_000:
			return capture.Frame{}, &capture.Unavailable{Reason: capture.ReasonSecureDesktop}, timeout
		case now >= 3_000_000 && !resumed:
			resumed = true
			return capture.Frame{}, capture.ErrReset, 0
		}
		return frames(s, now, timeout)
	}
	hs := newHarness(p, 1920, 1080)
	hs.run(t, wire.StreamHeader{S: "video"}, 4_000_000, nil)
	got := hs.rec.all(false)
	t.Logf("frames:%s", describe(t, got))
	paused := -1
	for i, f := range got {
		if f.kind == wire.KindVideoPaused {
			if paused >= 0 {
				t.Fatalf("a second paused frame at %d", i)
			}
			paused = i
			if r := decode[wire.VideoPaused](t, f).Reason; r != wire.PausedSecureDesktop {
				t.Fatalf("paused reason %q", r)
			}
		}
	}
	if paused < 0 || paused+2 >= len(got) {
		t.Fatalf("no paused frame followed by frames (%d of %d)", paused, len(got))
	}
	if got[paused+1].kind != wire.KindVideoInfo || got[paused+2].kind != wire.KindAU {
		t.Fatalf("after paused: kinds %#x %#x, want info then an access unit", got[paused+1].kind, got[paused+2].kind)
	}
	au, _ := wire.UnmarshalAU(got[paused+2].payload)
	if !au.Key || !au.HasConfig || au.CaptureMonoUS < 3_000_000 {
		t.Fatalf("the first access unit after the pause: %+v", au)
	}
	for _, f := range got[:paused] {
		if f.kind == wire.KindAU {
			if au, _ := wire.UnmarshalAU(f.payload); au.CaptureMonoUS >= 1_000_000 {
				t.Fatalf("an access unit of %d µs during the outage", au.CaptureMonoUS)
			}
		}
	}
}

func TestReinitOnSizeChange(t *testing.T) {
	frames := periodic(time.Second / 30)
	reset := false
	p := func(s *fakeSource, now uint64, timeout time.Duration) (capture.Frame, error, time.Duration) {
		if now >= 1_000_000 && !reset {
			reset = true
			s.resize(1280, 720)
			return capture.Frame{}, capture.ErrReset, 0
		}
		return frames(s, now, timeout)
	}
	hs := newHarness(p, 1920, 1080)
	hs.run(t, wire.StreamHeader{S: "video"}, 2_000_000, nil)
	got := hs.rec.all(false)
	t.Logf("frames:%s", describe(t, got))
	var infos []int
	for i, f := range got {
		if f.kind == wire.KindVideoInfo {
			infos = append(infos, i)
		}
	}
	if len(infos) != 2 || infos[1]+1 >= len(got) {
		t.Fatalf("info frames at %v of %d frames, want two with frames after each", infos, len(got))
	}
	info := decode[wire.VideoInfo](t, got[infos[1]])
	if info.Width != 1280 || info.Height != 720 || info.SrcWidth != 1280 || info.SrcHeight != 720 {
		t.Fatalf("second info %+v", info)
	}
	au, err := wire.UnmarshalAU(got[infos[1]+1].payload)
	if err != nil || got[infos[1]+1].kind != wire.KindAU || !au.Key || au.Width != 1280 || au.Height != 720 {
		t.Fatalf("the access unit after the second info: kind %#x %+v %v", got[infos[1]+1].kind, au, err)
	}
	encs := hs.encs.list()
	if len(encs) != 2 || !encs[0].closed || encs[1].srcW != 1280 {
		t.Fatalf("%d encoders, first closed %v", len(encs), len(encs) > 0 && encs[0].closed)
	}
}

func TestControlKeyframeAndBitrate(t *testing.T) {
	hs := newHarness(static, 1920, 1080)
	hs.run(t, wire.StreamHeader{S: "video"}, 5_000_000, map[uint64]func(){
		2_000_000: func() { hs.p.Control(wire.VideoControl{Op: wire.ControlKeyframe}) },
		3_000_000: func() { hs.p.Control(wire.VideoControl{Op: wire.ControlBitrate, BPS: 3_000_000}) },
	})
	aus := hs.rec.aus(t)
	t.Logf("frames:%s", describe(t, hs.rec.all(false)))
	if len(aus) != 2 || !aus[1].Key || !aus[1].HasConfig || aus[1].CaptureMonoUS < 2_000_000 || aus[1].CaptureMonoUS > 2_050_000 {
		t.Fatalf("access units %+v; want the first and a key frame within 50 ms of the request at 2 s", aus)
	}
	encs := hs.encs.list()
	if len(encs) != 1 || len(encs[0].bitrates) != 1 || encs[0].bitrates[0] != 3_000_000 {
		t.Fatalf("%d encoders, bitrates %v", len(encs), encs[0].bitrates)
	}
}

func TestControlConfigPauseAndResume(t *testing.T) {
	hs := newHarness(static, 1920, 1080)
	hs.run(t, wire.StreamHeader{S: "video", Display: 1}, 4_000_000, map[uint64]func(){
		1_000_000: func() { hs.p.Control(wire.VideoControl{Op: wire.ControlConfig, MaxFPS: 30, MaxWidth: 960}) },
		2_000_000: func() { hs.p.Control(wire.VideoControl{Op: wire.ControlPause}) },
		3_000_000: func() { hs.p.Control(wire.VideoControl{Op: wire.ControlResume}) },
	})
	got := hs.rec.all(false)
	desc := describe(t, got)
	t.Logf("frames:%s", desc)
	want := " info(1920x1080) K@0ms info(960x540) K@1000ms paused(no_viewer) info(960x540) K@3000ms"
	if desc != want {
		t.Fatalf("frames%s\nwant%s", desc, want)
	}
	if i := decode[wire.VideoInfo](t, got[2]); i.FPS != 30 || i.Display != 1 || i.SrcWidth != 1920 || i.Bitrate != DefaultBitrate(960) {
		t.Fatalf("info after config %+v", i)
	}
	srcs, encs := hs.op.sources(), hs.encs.list()
	if len(srcs) != 2 || !srcs[0].closed || !srcs[1].closed || len(encs) != 3 || !encs[0].closed || !encs[1].closed || !encs[2].closed {
		t.Fatalf("%d sources and %d encoders, not all closed", len(srcs), len(encs))
	}
	for _, d := range hs.op.display {
		if d != 1 {
			t.Fatalf("opened display %d", d)
		}
	}
}

func TestOpenFailureSendsPausedAndRetries(t *testing.T) {
	hs := newHarness(static, 1920, 1080)
	hs.op.fail = func(n int) error {
		if n <= 3 {
			return &capture.Unavailable{Reason: capture.ReasonSessionLocked}
		}
		return nil
	}
	hs.run(t, wire.StreamHeader{S: "video"}, 10_000_000, nil)
	desc := describe(t, hs.rec.all(false))
	if !strings.HasPrefix(desc, " paused(session_locked) info(1920x1080) K@") {
		t.Fatalf("frames%s", desc)
	}
	if hs.op.calls != 4 {
		t.Fatalf("%d opens", hs.op.calls)
	}
}

func TestStartRefusesBadHeadersAndMissingDisplays(t *testing.T) {
	for _, c := range []struct {
		h    wire.StreamHeader
		open error
		code string
	}{
		{wire.StreamHeader{S: "video", Codec: "hevc"}, nil, wire.CodeCapabilityUnsupported},
		{wire.StreamHeader{S: "video", MaxFPS: 1000}, nil, wire.CodeInvalidArgument},
		{wire.StreamHeader{S: "video", MaxFPS: -1}, nil, wire.CodeInvalidArgument},
		{wire.StreamHeader{S: "video", Bitrate: 10}, nil, wire.CodeInvalidArgument},
		{wire.StreamHeader{S: "video", MaxWidth: -5}, nil, wire.CodeInvalidArgument},
		{wire.StreamHeader{S: "video", MaxWidth: 20}, nil, wire.CodeInvalidArgument},
		{wire.StreamHeader{S: "video", Display: -1}, nil, wire.CodeInvalidArgument},
		{wire.StreamHeader{S: "video", Display: 3}, fmt.Errorf("display 3: %w", capture.ErrNoDisplay), wire.CodeNotFound},
		{wire.StreamHeader{S: "video"}, capture.ErrUnsupported, wire.CodeCapabilityUnsupported},
	} {
		hs := newHarness(static, 1920, 1080)
		hs.op.fail = func(int) error { return c.open }
		err := hs.p.Start(context.Background(), c.h, hs.rec.out)
		var ref *Refusal
		if !errors.As(err, &ref) || ref.Code != c.code || ref.Message == "" {
			t.Errorf("%+v (open %v): %v, want a refusal %s", c.h, c.open, err, c.code)
		}
		if n := len(hs.rec.all(true)); n != 0 {
			t.Errorf("%+v: %d frames written before the refusal", c.h, n)
		}
	}
}

func TestStatsEverySecond(t *testing.T) {
	hs := newHarness(periodic(time.Second/30), 1920, 1080)
	hs.run(t, wire.StreamHeader{S: "video"}, 3_500_000, nil)
	var stats []wire.VideoStats
	for _, f := range hs.rec.all(true) {
		if f.kind == wire.KindVideoStats {
			stats = append(stats, decode[wire.VideoStats](t, f))
		}
	}
	t.Logf("stats %+v", stats)
	if len(stats) != 3 {
		t.Fatalf("%d stats frames in 3.5 s", len(stats))
	}
	for _, s := range stats[1:] {
		if s.FPS < 29 || s.FPS > 31 || s.Bitrate <= 0 || s.EncodeMSP50 != 2 || s.EncodeMSP95 != 2 || s.Dropped != 0 || s.CaptureMSP50 < 0 {
			t.Errorf("stats %+v", s)
		}
	}
}

func TestOutputSize(t *testing.T) {
	for _, c := range [][5]int{
		{3840, 2160, 0, 3840, 2160},
		{3840, 2160, 1920, 1920, 1080},
		{3840, 2160, 5000, 3840, 2160},
		{3008, 1692, 1568, 1568, 882},
		{1366, 768, 1000, 1000, 562},
		{1921, 1081, 0, 1920, 1080},
		{2560, 1600, 1281, 1280, 800},
	} {
		if w, h := OutputSize(c[0], c[1], c[2]); w != c[3] || h != c[4] {
			t.Errorf("OutputSize(%d, %d, %d) = %dx%d, want %dx%d", c[0], c[1], c[2], w, h, c[3], c[4])
		}
	}
}

func TestDefaultBitrate(t *testing.T) {
	for w, want := range map[int]int{3840: 20_000_000, 3008: 20_000_000, 2560: 12_000_000, 2048: 12_000_000, 1920: 6_000_000, 1280: 6_000_000} {
		if got := DefaultBitrate(w); got != want {
			t.Errorf("DefaultBitrate(%d) = %d, want %d", w, got, want)
		}
	}
}

func TestPacingFollowsThePhaseOfTheSource(t *testing.T) {
	const period = 16_667
	p := func(s *fakeSource, now uint64, timeout time.Duration) (capture.Frame, error, time.Duration) {
		next := uint64(0)
		if s.gave > 0 {
			next = uint64(s.gave)*period - 3000
		}
		if next > now+uint64(timeout/time.Microsecond) {
			return capture.Frame{}, capture.ErrNoNewFrame, timeout
		}
		s.gave++
		return capture.Frame{}, nil, us(max(next, now) - now)
	}
	hs := newHarness(p, 1920, 1080)
	hs.run(t, wire.StreamHeader{S: "video", MaxFPS: 60}, 2_000_000, nil)
	aus := hs.rec.aus(t)
	if len(aus) < 115 {
		t.Fatalf("%d access units of a 60 Hz source in 2 s", len(aus))
	}
	for i, au := range aus {
		if wait := au.EncodeDoneMonoUS - au.CaptureMonoUS - 2000; wait != 0 {
			t.Fatalf("access unit %d waited %d µs after its capture", i, wait)
		}
	}
}

func TestPollsTheDesktopWhileAnAccessUnitIsPending(t *testing.T) {
	var mu sync.Mutex
	var timeouts []time.Duration
	var at []uint64
	p := func(s *fakeSource, now uint64, timeout time.Duration) (capture.Frame, error, time.Duration) {
		mu.Lock()
		timeouts, at = append(timeouts, timeout), append(at, now)
		mu.Unlock()
		return static(s, now, timeout)
	}
	hs := newHarness(p, 1920, 1080)
	hs.encs.hold = make(chan struct{})
	hs.encs.written = nil
	hs.run(t, wire.StreamHeader{S: "video"}, 300_000, map[uint64]func(){100_000: func() {
		close(hs.encs.hold)
		for deadline := time.Now().Add(time.Second); len(hs.rec.aus(t)) == 0 && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
		}
		time.Sleep(10 * time.Millisecond)
	}})
	mu.Lock()
	defer mu.Unlock()
	polls, waits := 0, 0
	for i, d := range timeouts {
		switch {
		case at[i] > 0 && at[i] < 100_000:
			if d != 0 {
				t.Fatalf("Next(%v) at %d µs while the first access unit is held", d, at[i])
			}
			polls++
		case at[i] >= 150_000 && d == poll:
			waits++
		}
	}
	if polls < 50 || waits == 0 {
		t.Fatalf("%d polls while the access unit was held, %d waits of %v after it", polls, waits, poll)
	}
}
