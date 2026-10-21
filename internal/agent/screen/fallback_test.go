package screen

import (
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// madeKinds writes the encoders in the order made: h for hardware, s for
// software.
func madeKinds(encs []*fakeEncoder) string {
	s := ""
	for _, e := range encs {
		if e.Info().Hardware {
			s += "h"
		} else {
			s += "s"
		}
	}
	return s
}

// infos returns the info frames of frames.
func infos(t *testing.T, frames []recorded) []wire.VideoInfo {
	t.Helper()
	var out []wire.VideoInfo
	for _, f := range frames {
		if f.kind == wire.KindVideoInfo {
			out = append(out, decode[wire.VideoInfo](t, f))
		}
	}
	return out
}

func TestTwoHardwareEncodersWithoutAnAccessUnitLeaveTheStreamOnSoftware(t *testing.T) {
	for _, c := range []struct {
		name             string
		tune             func(n int, e *fakeEncoder)
		configAt, stopAt uint64
	}{
		{"encode fails", func(_ int, e *fakeEncoder) { e.failFrom = 1 }, 3_000_000, 4_000_000},
		{"frames held", func(_ int, e *fakeEncoder) { e.hold = make(chan struct{}) }, 8_000_000, 9_000_000},
	} {
		t.Run(c.name, func(t *testing.T) {
			hs := newHarness(periodic(time.Second/30), 1920, 1080)
			hs.encs.tune = c.tune
			hs.run(t, wire.StreamHeader{S: "video"}, c.stopAt, map[uint64]func(){c.configAt: func() {
				hs.p.Control(wire.VideoControl{Op: wire.ControlConfig, MaxWidth: 960})
			}})
			got := hs.rec.all(false)
			encs := hs.encs.list()
			t.Logf("encoders %s, frames:%s", madeKinds(encs), describe(t, got))
			if kinds := madeKinds(encs); kinds != "hhss" {
				t.Fatalf("encoders made %s, want two hardware ones and then software ones only", kinds)
			}
			is := infos(t, got)
			if len(is) < 2 || is[len(is)-2].Hardware || is[len(is)-1].Hardware || is[len(is)-1].Width != 960 {
				t.Fatalf("infos %+v, want the last two of software encoders, the last 960 wide", is)
			}
			var soft []wire.AU
			for _, au := range hs.rec.aus(t) {
				if au.CaptureMonoUS < c.configAt {
					soft = append(soft, au)
				}
			}
			if len(soft) < 20 || !soft[0].Key || !soft[0].HasConfig {
				t.Fatalf("%d access units of the first software encoder, the first %+v", len(soft), soft)
			}
		})
	}
}

func TestAHardwareEncoderThatGaveAnAccessUnitStartsTheCountAgain(t *testing.T) {
	hs := newHarness(periodic(time.Second/30), 1920, 1080)
	hs.encs.tune = func(n int, e *fakeEncoder) {
		switch n {
		case 0, 2:
			e.failFrom = 1
		case 1:
			e.failFrom = 6
		}
	}
	hs.run(t, wire.StreamHeader{S: "video"}, 3_000_000, nil)
	encs := hs.encs.list()
	t.Logf("encoders %s, frames:%s", madeKinds(encs), describe(t, hs.rec.all(false)))
	if kinds := madeKinds(encs); kinds != "hhhh" {
		t.Fatalf("encoders made %s, want four hardware ones", kinds)
	}
	if n := encs[3].encoded(); n < 20 {
		t.Fatalf("the fourth encoder took %d frames", n)
	}
}

func TestAnEncoderClosedForAConfigDoesNotCountAsAFailure(t *testing.T) {
	hs := newHarness(periodic(time.Second/30), 1920, 1080)
	hs.encs.tune = func(n int, e *fakeEncoder) {
		switch n {
		case 0, 1:
			e.hold = make(chan struct{})
		case 2:
			e.failFrom = 1
		}
	}
	hs.run(t, wire.StreamHeader{S: "video"}, 2_000_000, map[uint64]func(){
		300_000: func() { hs.p.Control(wire.VideoControl{Op: wire.ControlConfig, MaxWidth: 1280}) },
		600_000: func() { hs.p.Control(wire.VideoControl{Op: wire.ControlConfig, MaxWidth: 960}) },
	})
	encs := hs.encs.list()
	t.Logf("encoders %s, frames:%s", madeKinds(encs), describe(t, hs.rec.all(false)))
	if kinds := madeKinds(encs); kinds != "hhhh" {
		t.Fatalf("encoders made %s, want four hardware ones", kinds)
	}
	if n := encs[3].encoded(); n < 20 {
		t.Fatalf("the fourth encoder took %d frames", n)
	}
}
