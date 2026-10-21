package screen

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

func TestWarmDue(t *testing.T) {
	const (
		n      = 5_000_000
		window = uint64(warmWindow / time.Microsecond)
		hold   = uint64(actionHoldOff / time.Microsecond)
		at60   = 16_667
		at30   = 33_334
	)
	// moved is the state of a pointer move injected at n with nothing else
	// for long, at max_fps 60.
	moved := warmState{inputUS: n, auUS: 0, maxFPS: 60}
	with := func(f func(*warmState)) warmState {
		s := moved
		f(&s)
		return s
	}
	for _, c := range []struct {
		name string
		s    warmState
		now  uint64
		want bool
	}{
		{"no input", with(func(s *warmState) { s.inputUS = 0 }), n, false},
		{"a pointer move just injected", moved, n, true},
		{"a pointer move injected after the reading of now", with(func(s *warmState) { s.inputUS = n + 3_000 }), n, true},
		{"last moment of the window", moved, n + window - 1, true},
		{"window ended", moved, n + window, false},
		{"long after the window", moved, n + 10*window, false},
		{"an action just injected", with(func(s *warmState) { s.actionUS = n }), n, false},
		{"an action injected after the reading of now", with(func(s *warmState) { s.inputUS, s.actionUS = n+3_000, n+3_000 }), n, false},
		{"last moment of the hold-off after an action", with(func(s *warmState) { s.actionUS = n }), n + hold - 1, false},
		{"hold-off after an action ended", with(func(s *warmState) { s.actionUS = n }), n + hold, true},
		{"an older action and a newer move", with(func(s *warmState) { s.actionUS, s.inputUS = n-hold, n }), n, true},
		{"one interval at 60 fps after the last frame", with(func(s *warmState) { s.auUS = n + 100_000 - at60 }), n + 100_000, true},
		{"less than one interval at 60 fps", with(func(s *warmState) { s.auUS = n + 100_000 - at60 + 1 }), n + 100_000, false},
		{"last frame at now", with(func(s *warmState) { s.auUS = n + 100_000 }), n + 100_000, false},
		{"a new image one and a quarter intervals ago", with(func(s *warmState) { s.imageUS = n + 100_000 - at60 - at60/4 }), n + 100_000, true},
		{"a new image less than one and a quarter intervals ago", with(func(s *warmState) { s.imageUS = n + 100_000 - at60 - at60/4 + 1 }), n + 100_000, false},
		{"max_fps 30 waits its interval", with(func(s *warmState) { s.maxFPS, s.auUS = 30, n+200_000-at60 }), n + 200_000, false},
		{"max_fps 30 after its interval", with(func(s *warmState) { s.maxFPS, s.auUS = 30, n+200_000-at30 }), n + 200_000, true},
		{"max_fps 30 waits a quarter interval more after a new image", with(func(s *warmState) { s.maxFPS, s.imageUS = 30, n+200_000-at30-at30/4+1 }), n + 200_000, false},
		{"max_fps 120 keeps the 60 fps interval", with(func(s *warmState) { s.maxFPS, s.auUS = 120, n+200_000-10_000 }), n + 200_000, false},
		{"max_fps 120 after the 60 fps interval", with(func(s *warmState) { s.maxFPS, s.auUS = 120, n+200_000-at60 }), n + 200_000, true},
		{"max_fps 1", with(func(s *warmState) { s.maxFPS, s.auUS = 1, n+500_000-999_999 }), n + 500_000, false},
		{"max_fps 0", with(func(s *warmState) { s.maxFPS = 0 }), n + 100_000, false},
		{"a frame in the encoder", with(func(s *warmState) { s.inFlight = 1 }), n + 100_000, false},
		{"two frames in the encoder", with(func(s *warmState) { s.inFlight = 2 }), n + 100_000, false},
	} {
		if got := warmDue(c.s, c.now); got != c.want {
			t.Errorf("%s: warmDue(%+v, now %d) = %v, want %v", c.name, c.s, c.now, got, c.want)
		}
	}
	if got := (warmState{maxFPS: 0}).earliestUS(); got != 1_000_000 {
		t.Errorf("earliest reading at max_fps 0 is %d µs, want one second", got)
	}
	if warmWindow != 750*time.Millisecond || warmMaxFPS != 60 || actionHoldOff != 50*time.Millisecond {
		t.Fatalf("warm window %v at most %d fps, hold-off %v; want 750ms at most 60 fps, 50ms", warmWindow, warmMaxFPS, actionHoldOff)
	}
}

// maxPerSecond returns the most access units captured within one second.
func maxPerSecond(aus []wire.AU) int {
	most := 0
	for i := range aus {
		n := 0
		for _, au := range aus[i:] {
			if au.CaptureMonoUS < aus[i].CaptureMonoUS+1_000_000 {
				n++
			}
		}
		most = max(most, n)
	}
	return most
}

// between returns the access units captured at from µs or later and
// before to.
func between(aus []wire.AU, from, to uint64) []wire.AU {
	var out []wire.AU
	for _, au := range aus {
		if au.CaptureMonoUS >= from && au.CaptureMonoUS < to {
			out = append(out, au)
		}
	}
	return out
}

func TestWarmFeedAfterAnInputOnAStaticScreen(t *testing.T) {
	for _, c := range []struct {
		maxFPS      int
		interval    uint64
		least, most int
	}{
		{0, 16_667, 40, 46},
		{30, 33_334, 20, 23},
		{120, 16_667, 40, 46},
	} {
		t.Run(fmt.Sprintf("max_fps %d", c.maxFPS), func(t *testing.T) {
			const input = 2_000_000
			window := uint64(warmWindow / time.Microsecond)
			hs := newHarness(static, 1920, 1080)
			hs.run(t, wire.StreamHeader{S: "video", MaxFPS: c.maxFPS}, 14_000_000, map[uint64]func(){input: func() { hs.input.Store(input) }})
			aus := hs.rec.aus(t)
			t.Logf("frames:%s", describe(t, hs.rec.all(false)))
			if len(aus) == 0 || aus[0].CaptureMonoUS != 0 || !aus[0].Key {
				t.Fatalf("the first access unit %+v", aus)
			}
			if n := len(between(aus, 1, input)); n != 0 {
				t.Fatalf("%d access units before the input", n)
			}
			warm := between(aus, input, input+window)
			if len(warm) < c.least || len(warm) > c.most {
				t.Fatalf("%d access units in the %v after the input, want %d to %d", len(warm), warmWindow, c.least, c.most)
			}
			if warm[0].CaptureMonoUS > input+uint64(poll/time.Microsecond) {
				t.Fatalf("the first access unit after the input at %d µs", warm[0].CaptureMonoUS)
			}
			for i, au := range warm {
				if au.Key || au.EncodeDoneMonoUS != au.CaptureMonoUS+2000 {
					t.Fatalf("access unit %d of the window: key %v, captured %d µs, encoded %d µs", i, au.Key, au.CaptureMonoUS, au.EncodeDoneMonoUS)
				}
				if i > 0 && au.CaptureMonoUS-warm[i-1].CaptureMonoUS < c.interval {
					t.Fatalf("access units %d and %d of the window %d µs apart, want at least %d", i-1, i, au.CaptureMonoUS-warm[i-1].CaptureMonoUS, c.interval)
				}
			}
			last := warm[len(warm)-1].CaptureMonoUS
			after := between(aus, input+window, 14_000_000)
			if len(after) != 1 || after[0].Key || after[0].CaptureMonoUS < last+10_000_000 || after[0].CaptureMonoUS > last+10_100_000 {
				t.Fatalf("after the window: %+v, want one repeat 10 s after %d µs", after, last)
			}
			if n := len(hs.encs.list()); n != 1 {
				t.Fatalf("%d encoders", n)
			}
		})
	}
}

func TestNoInputKeepsOnlyTheKeepalive(t *testing.T) {
	for _, name := range []string{"LastInput nil", "LastInput 0"} {
		t.Run(name, func(t *testing.T) {
			hs := newHarness(static, 1920, 1080)
			if name == "LastInput nil" {
				hs.p.opt.LastInput = nil
			}
			hs.run(t, wire.StreamHeader{S: "video"}, 35_000_000, nil)
			aus := hs.rec.aus(t)
			if len(aus) != 4 {
				t.Fatalf("%d access units in 35 s of a static screen, want 4:%s", len(aus), describe(t, hs.rec.all(false)))
			}
			for i := 1; i < len(aus); i++ {
				if gap := aus[i].CaptureMonoUS - aus[i-1].CaptureMonoUS; gap < 10_000_000 || gap > 10_100_000 {
					t.Fatalf("access unit %d %d µs after the one before", i, gap)
				}
			}
		})
	}
}

func TestWarmFeedKeepsEveryNewImage(t *testing.T) {
	const input = 1_000_000
	window := uint64(warmWindow / time.Microsecond)
	frames := periodic(time.Second / 30)
	p := func(s *fakeSource, now uint64, timeout time.Duration) (capture.Frame, error, time.Duration) {
		f, err, wait := frames(s, now, timeout)
		if err == nil {
			f.Texture = uintptr(s.gave)
		}
		return f, err, wait
	}
	hs := newHarness(p, 1920, 1080)
	hs.run(t, wire.StreamHeader{S: "video"}, 3_000_000, map[uint64]func(){input: func() { hs.input.Store(input) }})
	aus := hs.rec.aus(t)
	if n := maxPerSecond(aus); n > DefaultMaxFPS+1 {
		t.Fatalf("%d access units within one second at max_fps %d", n, DefaultMaxFPS)
	}
	given := hs.encs.list()[0].frameList()
	images, repeats := map[uintptr]bool{}, 0
	var lastTexture uintptr
	for _, g := range given {
		switch {
		case g.texture != lastTexture:
			if images[g.texture] {
				t.Fatalf("image %d given again after image %d", g.texture, lastTexture)
			}
			images[g.texture] = true
			if g.monoUS > g.atUS {
				t.Fatalf("image %d captured at %d µs given at %d µs", g.texture, g.monoUS, g.atUS)
			}
		case g.atUS < input || g.atUS >= input+window+uint64(poll/time.Microsecond):
			t.Fatalf("image %d given again at %d µs, outside the window", g.texture, g.atUS)
		default:
			if g.monoUS != g.atUS {
				t.Fatalf("a repeat of image %d captured at %d µs given at %d µs", g.texture, g.monoUS, g.atUS)
			}
			repeats++
		}
		lastTexture = g.texture
	}
	if len(images) < 85 || repeats < 15 {
		t.Fatalf("%d images and %d repeats in 3 s of a 30 fps source with an input at 1 s", len(images), repeats)
	}
}

func TestWarmFeedStopsAtAResetUntilANewImage(t *testing.T) {
	const (
		input   = 1_000_000
		resetAt = 1_200_000
		newAt   = 1_500_000
	)
	window := uint64(warmWindow / time.Microsecond)
	var hs *harness
	var mu sync.Mutex
	reset, second := false, false
	givenAtReset := -1
	p := func(s *fakeSource, now uint64, timeout time.Duration) (capture.Frame, error, time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case s.gave == 0:
			s.gave++
			return capture.Frame{Texture: 1}, nil, 0
		case now >= resetAt && !reset:
			reset = true
			givenAtReset = len(hs.encs.list()[0].frameList())
			return capture.Frame{}, capture.ErrReset, 0
		case now >= newAt && !second:
			second = true
			return capture.Frame{Texture: 2}, nil, 0
		}
		return capture.Frame{}, capture.ErrNoNewFrame, timeout
	}
	hs = newHarness(p, 1920, 1080)
	hs.run(t, wire.StreamHeader{S: "video"}, 3_000_000, map[uint64]func(){input: func() { hs.input.Store(input) }})
	given := hs.encs.list()[0].frameList()
	mu.Lock()
	defer mu.Unlock()
	if givenAtReset < 0 {
		t.Fatal("the source never reset")
	}
	repeats := 0
	for _, g := range given[:givenAtReset] {
		if g.atUS >= input {
			repeats++
		}
	}
	for i, g := range given[givenAtReset:] {
		switch {
		case g.texture != 2:
			t.Fatalf("frame %d after the reset is image %d, given at %d µs", i, g.texture, g.atUS)
		case g.atUS < newAt:
			t.Fatalf("frame %d after the reset given at %d µs, before the next image", i, g.atUS)
		case g.atUS >= input+window+uint64(poll/time.Microsecond):
			t.Fatalf("frame %d after the reset given at %d µs, after the window", i, g.atUS)
		}
	}
	if after := len(given) - givenAtReset; repeats < 10 || after < 10 {
		t.Fatalf("%d repeats before the reset and %d frames after it", repeats, after)
	}
}

func TestPollsTheDesktopWhileWarming(t *testing.T) {
	const input = 1_000_000
	window := uint64(warmWindow / time.Microsecond)
	var c calls
	hs := newHarness(c.wrap(static), 1920, 1080)
	hs.run(t, wire.StreamHeader{S: "video"}, 3_000_000, map[uint64]func(){input: func() { hs.input.Store(input) }})
	c.mu.Lock()
	defer c.mu.Unlock()
	during, waits := 0, 0
	for i, at := range c.at {
		switch {
		case at >= input+uint64(poll/time.Microsecond) && at < input+window:
			if c.timeouts[i] != 0 {
				t.Fatalf("Next(%v) at %d µs while warming", c.timeouts[i], at)
			}
			during++
		case at >= input+window+uint64(pollWindow/time.Microsecond) && c.timeouts[i] == poll:
			waits++
		}
	}
	if during < 500 || waits == 0 {
		t.Fatalf("%d looks without waiting while warming, %d waits of %v after", during, waits, poll)
	}
}

func TestWarmEarliestIsWhenWarmDueHolds(t *testing.T) {
	for _, s := range []warmState{
		{inputUS: 1_000_000, maxFPS: 60},
		{inputUS: 1_000_000, actionUS: 1_000_000, auUS: 990_000, maxFPS: 60},
		{inputUS: 1_000_000, imageUS: 1_010_000, auUS: 1_012_000, maxFPS: 60},
		{inputUS: 1_000_000, actionUS: 900_000, imageUS: 1_001_000, auUS: 1_005_000, maxFPS: 30},
	} {
		at := max(s.earliestUS(), s.inputUS)
		if !warmDue(s, at) {
			t.Errorf("%+v: warmDue at its earliest %d is false", s, at)
		}
		if at > s.inputUS && warmDue(s, at-1) {
			t.Errorf("%+v: warmDue holds before its earliest %d", s, at)
		}
	}
}

func TestWarmFeedWaitsAfterAnAction(t *testing.T) {
	const input = 2_000_000
	hold := uint64(actionHoldOff / time.Microsecond)
	window := uint64(warmWindow / time.Microsecond)
	hs := newHarness(static, 1920, 1080)
	hs.run(t, wire.StreamHeader{S: "video"}, 4_000_000, map[uint64]func(){input: func() {
		hs.action.Store(input)
		hs.input.Store(input)
	}})
	warm := between(hs.rec.aus(t), input, input+window)
	if len(warm) < 38 || warm[0].CaptureMonoUS < input+hold || warm[0].CaptureMonoUS > input+hold+uint64(pollStep/time.Microsecond) {
		t.Fatalf("%d access units in the window:%s; want the first at the end of the %v hold-off", len(warm), describe(t, hs.rec.all(false)), actionHoldOff)
	}
}

func TestWarmFeedLeavesAMovingScreenAlone(t *testing.T) {
	const input = 1_000_000
	jitter := []int64{900, -900, 500, -300, 0, 700, -700}
	p := func(s *fakeSource, now uint64, timeout time.Duration) (capture.Frame, error, time.Duration) {
		next := uint64(int64(s.gave)*16_667 + jitter[s.gave%len(jitter)] + 1_000)
		if next > now+uint64(timeout/time.Microsecond) {
			return capture.Frame{}, capture.ErrNoNewFrame, timeout
		}
		s.gave++
		return capture.Frame{Texture: uintptr(s.gave)}, nil, us(max(next, now) - now)
	}
	hs := newHarness(p, 1920, 1080)
	hs.run(t, wire.StreamHeader{S: "video"}, 2_000_000, map[uint64]func(){input: func() { hs.input.Store(input) }})
	given := hs.encs.list()[0].frameList()
	for i, g := range given {
		if i > 0 && g.texture == given[i-1].texture {
			t.Fatalf("image %d given again at %d µs while the screen changes every frame", g.texture, g.atUS)
		}
		if g.atUS-g.monoUS > 1_000 {
			t.Fatalf("image %d taken at %d µs given at %d µs", g.texture, g.monoUS, g.atUS)
		}
	}
	if len(given) < 115 {
		t.Fatalf("%d images of a 60 fps source in 2 s", len(given))
	}
}

func TestASoftwareEncoderIsNotWarmed(t *testing.T) {
	window := uint64(warmWindow / time.Microsecond)
	for _, c := range []struct {
		name     string
		software func(n int) bool
		tune     func(n int, e *fakeEncoder)
		input    uint64
		at       map[uint64]func(hs *harness)
	}{
		{"software from the start", func(int) bool { return true }, nil, 2_000_000, nil},
		{"software after a rebuild", func(n int) bool { return n > 0 }, nil, 2_000_000, map[uint64]func(hs *harness){
			1_000_000: func(hs *harness) { hs.p.Control(wire.VideoControl{Op: wire.ControlConfig}) },
		}},
		{"software after two hardware encoders failed", nil, func(_ int, e *fakeEncoder) { e.failFrom = 1 }, 2_000_000, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			var calls calls
			hs := newHarness(calls.wrap(static), 1920, 1080)
			hs.encs.software, hs.encs.tune = c.software, c.tune
			at := map[uint64]func(){c.input: func() { hs.input.Store(c.input) }}
			for when, fn := range c.at {
				at[when] = func() { fn(hs) }
			}
			hs.run(t, wire.StreamHeader{S: "video"}, 4_000_000, at)
			encs := hs.encs.list()
			last := encs[len(encs)-1]
			if last.Info().Hardware {
				t.Fatalf("the last of %d encoders is a hardware encoder", len(encs))
			}
			if n := len(between(hs.rec.aus(t), c.input, c.input+window)); n != 0 {
				t.Fatalf("%d access units in the window after an input on a software encoder:%s", n, describe(t, hs.rec.all(false)))
			}
			calls.mu.Lock()
			defer calls.mu.Unlock()
			for i, at := range calls.at {
				if at >= c.input+uint64(pollWindow/time.Microsecond) && at < c.input+window && calls.timeouts[i] == 0 {
					t.Fatalf("Next(0) at %d µs after an input on a software encoder", at)
				}
			}
		})
	}
}
