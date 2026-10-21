package screen

import (
	"context"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/agent/encode"
	"github.com/YangTaeyoung/deskhand/internal/agent/monoclock"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

var procNtQueryTimerResolution = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQueryTimerResolution")

// timerResolution returns the current resolution of the system timer, 0
// when it cannot be read.
func timerResolution() time.Duration {
	var lowest, highest, current uint32
	if r, _, _ := procNtQueryTimerResolution.Call(uintptr(unsafe.Pointer(&lowest)), uintptr(unsafe.Pointer(&highest)), uintptr(unsafe.Pointer(&current))); r != 0 {
		return 0
	}
	return time.Duration(current) * 100
}

// spread returns the p50 and p95 of v by the nearest rank.
func spread(v []float64) (p50, p95 float64) {
	if len(v) == 0 {
		return 0, 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2], s[len(s)*95/100]
}

// timedEncoder is an encoder that records how long each Encode call took
// and when each access unit left it.
type timedEncoder struct {
	encode.Encoder
	out chan wire.AU

	mu      sync.Mutex
	callsMS []float64
	callAt  []uint64
	outAt   []uint64
}

func newTimedEncoder(enc encode.Encoder) *timedEncoder {
	te := &timedEncoder{Encoder: enc, out: make(chan wire.AU, 2)}
	go func() {
		defer close(te.out)
		for au := range enc.Output() {
			te.mu.Lock()
			te.outAt = append(te.outAt, monoclock.NowUS())
			te.mu.Unlock()
			te.out <- au
		}
	}()
	return te
}

func (te *timedEncoder) Encode(f capture.Frame, forceKey bool) error {
	start := monoclock.NowUS()
	err := te.Encoder.Encode(f, forceKey)
	end := monoclock.NowUS()
	te.mu.Lock()
	te.callsMS, te.callAt = append(te.callsMS, float64(end-start)/1000), append(te.callAt, start)
	te.mu.Unlock()
	return err
}

func (te *timedEncoder) Output() <-chan wire.AU { return te.out }

func (te *timedEncoder) Pending() int { return te.Encoder.Pending() + len(te.out) }

// idleMS returns, for each Encode call after the first, the time from the
// access unit that left the encoder last before it to the call.
func (te *timedEncoder) idleMS() []float64 {
	te.mu.Lock()
	defer te.mu.Unlock()
	var v []float64
	j := 0
	for _, at := range te.callAt {
		for j < len(te.outAt) && te.outAt[j] <= at {
			j++
		}
		if j > 0 {
			v = append(v, float64(at-te.outAt[j-1])/1000)
		}
	}
	return v
}

// TestWarmFeedCPU streams the first display with each encoder, without
// input and with a pointer move injected all the time, and logs the access
// units, bitrate, encode time and process CPU of each: the hardware encoder
// gets the warm feed, the software encoder none.
func TestWarmFeedCPU(t *testing.T) {
	streamDisplay(t)
	list, err := display.List()
	if err != nil || len(list) == 0 {
		t.Fatalf("display.List: %v (%d displays)", err, len(list))
	}
	d := list[0]
	const settle, measure = 2 * time.Second, 10 * time.Second
	for _, enc := range []struct {
		name string
		make func(dev *d3d.Device, srcW, srcH, outW, outH, fps, bps int) (encode.Encoder, error)
	}{
		{"hardware", encode.New},
		{"software", encode.NewSoftware},
	} {
		for _, warm := range []bool{false, true} {
			name := enc.name + "_idle"
			var lastInput func() (uint64, uint64)
			if warm {
				name, lastInput = enc.name+"_warm", func() (uint64, uint64) { return monoclock.NowUS(), 0 }
			}
			t.Run(name, func(t *testing.T) {
				rec := &recorder{}
				ctx, cancel := context.WithTimeout(context.Background(), settle+measure+5*time.Second)
				defer cancel()
				errc := make(chan error, 1)
				var timed []*timedEncoder
				var tmu sync.Mutex
				p := New(Options{NewEncoder: func(dev *d3d.Device, srcW, srcH, outW, outH, fps, bps int) (encode.Encoder, error) {
					e, err := enc.make(dev, srcW, srcH, outW, outH, fps, bps)
					if err != nil {
						return nil, err
					}
					te := newTimedEncoder(e)
					tmu.Lock()
					timed = append(timed, te)
					tmu.Unlock()
					return te, nil
				}, LastInput: lastInput})
				go func() { errc <- p.Start(ctx, wire.StreamHeader{S: "video", Display: d.Index}, rec.out) }()
				time.Sleep(settle)
				cpu0, wall0, aus0 := processCPU(t), time.Now(), len(rec.aus(t))
				time.Sleep(measure)
				cpu1, wall1, all := processCPU(t), time.Now(), rec.aus(t)
				cancel()
				if err := <-errc; err != nil {
					t.Fatalf("Start: %v", err)
				}
				frames := rec.all(true)
				if len(frames) == 0 || frames[0].kind != wire.KindVideoInfo {
					t.Fatalf("%d frames, the first not info", len(frames))
				}
				info := decode[wire.VideoInfo](t, frames[0])
				aus := all[aus0:]
				bytes, keys := 0, 0
				var gaps, encMS []float64
				for i, au := range aus {
					bytes += len(au.Data)
					if au.Key {
						keys++
					}
					encMS = append(encMS, float64(au.EncodeDoneMonoUS-au.CaptureMonoUS)/1000)
					if i > 0 {
						gaps = append(gaps, float64(au.CaptureMonoUS-aus[i-1].CaptureMonoUS)/1000)
					}
				}
				gap50, gap95 := spread(gaps)
				enc50, enc95 := spread(encMS)
				var encP50 []float64
				busy := 0
				for _, f := range frames {
					if f.kind == wire.KindVideoStats {
						s := decode[wire.VideoStats](t, f)
						busy += s.EncoderBusy
						if s.FPS > 0 {
							encP50 = append(encP50, s.EncodeMSP50)
						}
					}
				}
				var calls, idle []float64
				tmu.Lock()
				for _, te := range timed {
					te.mu.Lock()
					calls = append(calls, te.callsMS...)
					te.mu.Unlock()
					idle = append(idle, te.idleMS()...)
				}
				tmu.Unlock()
				call50, call95 := spread(calls)
				idle50, idle95 := spread(idle)
				t.Logf("%s: Encode call p50 %.2f p95 %.2f ms; access unit out → next Encode p50 %.2f p95 %.2f ms", name, call50, call95, idle50, idle95)
				wall := wall1.Sub(wall0)
				fps := float64(len(aus)) / wall.Seconds()
				oneCPU := float64(cpu1-cpu0) / float64(wall) * 100
				t.Logf("%s: display %d %dx%d on %s (hardware %v), timer %v: %d access units in %v (%.1f fps, gap p50 %.2f p95 %.2f ms), %.3f Mbps, key frames %d, capture→encoded p50 %.2f p95 %.2f ms, stats encode p50 %.2f ms (median of the seconds), encoder busy %d; process CPU %v = %.1f%% of one logical CPU, %.2f%% of all %d",
					name, d.Index, d.Width, d.Height, info.Encoder, info.Hardware, timerResolution(), len(aus), wall.Round(time.Millisecond), fps, gap50, gap95,
					float64(bytes)*8/wall.Seconds()/1e6, keys, enc50, enc95, median(encP50), busy, (cpu1 - cpu0).Round(time.Millisecond),
					oneCPU, oneCPU/float64(runtime.NumCPU()), runtime.NumCPU())
				if info.Hardware != (enc.name == "hardware") {
					t.Fatalf("encoder %s, hardware %v", info.Encoder, info.Hardware)
				}
				if keys != 0 {
					t.Errorf("%d key frames after the first", keys)
				}
				switch {
				case warm && info.Hardware && fps < 30:
					t.Errorf("%.1f fps on the hardware encoder with a pointer move injected all the time", fps)
				case warm && !info.Hardware && fps >= 5:
					t.Errorf("%.1f fps on the software encoder with a pointer move injected all the time; it gets no warm feed", fps)
				}
			})
		}
	}
}
