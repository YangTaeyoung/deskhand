package screen

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// streamDisplay returns the display the real pipeline tests stream: the
// second display when there is one, else the first; it skips the test
// when the session cannot be captured.
func streamDisplay(t *testing.T) display.Info {
	t.Helper()
	display.EnsureDPIAware()
	if err := capture.Probe(); err != nil {
		var u *capture.Unavailable
		if errors.As(err, &u) {
			t.Skipf("the session cannot be captured now: %v", u)
		}
		t.Fatalf("Probe: %v", err)
	}
	list, err := display.List()
	if err != nil || len(list) == 0 {
		t.Fatalf("display.List: %v (%d displays)", err, len(list))
	}
	d := list[0]
	if len(list) > 1 {
		d = list[1]
	}
	return d
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}

func TestPipelineStreamsADisplay(t *testing.T) {
	d := streamDisplay(t)
	for _, sc := range []struct {
		name     string
		maxWidth int
		motion   bool
		run      time.Duration
	}{
		{"native_static", 0, false, 12 * time.Second},
		{"native_motion", 0, true, 8 * time.Second},
		{"1920_static", 1920, false, 12 * time.Second},
		{"1920_motion", 1920, true, 8 * time.Second},
	} {
		t.Run(sc.name, func(t *testing.T) {
			if sc.motion {
				w, h := min(1280, d.Width), min(720, d.Height)
				stop := showMotion(t, d.X+(d.Width-w)/2, d.Y+(d.Height-h)/2, w, h)
				defer stop()
				time.Sleep(200 * time.Millisecond)
			}
			var logs bytes.Buffer
			rec := &recorder{}
			p := New(Options{Logger: log.New(&logs, "", log.Lmicroseconds)})
			ctx, cancel := context.WithTimeout(context.Background(), sc.run)
			defer cancel()
			start := time.Now()
			if err := p.Start(ctx, wire.StreamHeader{S: "video", Display: d.Index, MaxWidth: sc.maxWidth}, rec.out); err != nil {
				t.Fatalf("Start: %v\n%s", err, logs.String())
			}
			ran := time.Since(start)
			t.Logf("pipeline log:\n%s", logs.String())
			frames := rec.all(true)
			if len(frames) == 0 || frames[0].kind != wire.KindVideoInfo {
				t.Fatalf("%d frames, the first not info", len(frames))
			}
			info := decode[wire.VideoInfo](t, frames[0])
			wantW, wantH := OutputSize(d.Width, d.Height, sc.maxWidth)
			if info.Width != wantW || info.Height != wantH || info.SrcWidth != d.Width || info.SrcHeight != d.Height || info.Display != d.Index ||
				info.Codec != "h264" || info.FPS != DefaultMaxFPS || info.Bitrate != DefaultBitrate(wantW) || info.Capture == "" || info.Encoder == "" {
				t.Fatalf("info %+v for display %+v", info, d)
			}
			aus := rec.aus(t)
			if len(aus) == 0 || !aus[0].Key || !aus[0].HasConfig {
				t.Fatalf("%d access units, the first not a key frame with parameter sets", len(aus))
			}
			var stream []byte
			var lat []float64
			bytesAfter, ausAfter := 0, 0
			maxGap := time.Duration(0)
			for i, au := range aus {
				stream = append(stream, au.Data...)
				lat = append(lat, float64(au.EncodeDoneMonoUS-au.CaptureMonoUS)/1000)
				if au.CaptureMonoUS >= aus[0].CaptureMonoUS+1_000_000 {
					bytesAfter += len(au.Data)
					ausAfter++
				}
				if i > 0 {
					maxGap = max(maxGap, time.Duration(au.CaptureMonoUS-aus[i-1].CaptureMonoUS)*time.Microsecond)
				}
				if int(au.Width) != wantW || int(au.Height) != wantH || au.Seq != uint64(i+1) {
					t.Fatalf("access unit %d: %dx%d seq %d", i, au.Width, au.Height, au.Seq)
				}
			}
			var encP50, encP95, capP50 []float64
			stats, dropped, busy := 0, 0, 0
			for _, f := range frames {
				if f.kind == wire.KindVideoStats {
					s := decode[wire.VideoStats](t, f)
					stats++
					dropped, busy = dropped+s.Dropped, busy+s.EncoderBusy
					if s.FPS > 0 {
						encP50, encP95, capP50 = append(encP50, s.EncodeMSP50), append(encP95, s.EncodeMSP95), append(capP50, s.CaptureMSP50)
					}
				}
				if f.kind == wire.KindVideoPaused {
					t.Errorf("paused frame %s", f.payload)
				}
			}
			sort.Float64s(lat)
			span := ran.Seconds() - 1
			fps := float64(ausAfter) / span
			t.Logf("%s: display %d %dx%d → %dx%d, %s (hardware %v), capture %s: %d access units in %v (%.1f fps after the first second), "+
				"%.2f Mbps, key frames %d, longest gap %v, capture→access unit p50 %.2f ms p95 %.2f ms; stats frames %d: encode p50 %.2f ms p95 %.2f ms, capture p50 %.2f ms (medians of the seconds with frames), dropped %d, encoder busy %d",
				sc.name, d.Index, d.Width, d.Height, info.Width, info.Height, info.Encoder, info.Hardware, info.Capture, len(aus), ran.Round(time.Millisecond),
				fps, float64(bytesAfter)*8/span/1e6, countKeys(aus), maxGap.Round(time.Millisecond), lat[len(lat)/2], lat[len(lat)*95/100],
				stats, median(encP50), median(encP95), median(capP50), dropped, busy)
			if maxGap > keepalive+500*time.Millisecond {
				t.Errorf("%v without an access unit", maxGap)
			}
			if sc.motion && fps < 30 {
				t.Errorf("%.1f fps with a moving window on the display", fps)
			}
			if stats < int(sc.run/time.Second)-2 {
				t.Errorf("%d stats frames in %v", stats, sc.run)
			}
			if dir := os.Getenv("DESKHAND_WINTEST_ARTIFACTS"); dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "pipeline_"+sc.name+".h264"), stream, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func countKeys(aus []wire.AU) int {
	n := 0
	for _, au := range aus {
		if au.Key {
			n++
		}
	}
	return n
}
