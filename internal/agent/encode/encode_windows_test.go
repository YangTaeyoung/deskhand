package encode

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/capture"
	"github.com/YangTaeyoung/deskhand/internal/agent/monoclock"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// lockThread locks the test goroutine to its thread with COM and Media
// Foundation ready until the test ends.
func lockThread(t *testing.T) {
	t.Helper()
	runtime.LockOSThread()
	leave, err := InitThread()
	if err != nil {
		runtime.UnlockOSThread()
		t.Fatalf("InitThread: %v", err)
	}
	t.Cleanup(func() {
		leave()
		runtime.UnlockOSThread()
	})
}

// testDevice returns a device on the first output attached to the
// desktop, skipping the test in a session without outputs.
func testDevice(t *testing.T) *d3d.Device {
	t.Helper()
	outs, err := d3d.Outputs()
	var hr com.HRESULT
	if errors.As(err, &hr) && hr == com.DXGI_ERROR_NOT_CURRENTLY_AVAILABLE {
		t.Skipf("this session has no DXGI outputs (%v); run this test in the user's session through scripts/wintest.sh", err)
	}
	if err != nil {
		t.Fatalf("Outputs: %v", err)
	}
	for _, o := range outs {
		if o.Attached {
			dev, err := d3d.NewDeviceForOutput(o.Name)
			if err != nil {
				t.Fatalf("NewDeviceForOutput(%q): %v", o.Name, err)
			}
			t.Cleanup(dev.Close)
			return dev
		}
	}
	t.Skip("no output is attached to the desktop")
	return nil
}

// synthetic makes w×h BGRA images whose content moves with each frame.
type synthetic struct {
	dev       *d3d.Device
	tex       uintptr
	w, h      int
	base, pix []byte
	cpu       bool
	box       [2]int
}

const boxW, boxH = 400, 300

func newSynthetic(t *testing.T, dev *d3d.Device, w, h int, cpu bool) *synthetic {
	t.Helper()
	s := &synthetic{dev: dev, w: w, h: h, cpu: cpu, base: make([]byte, w*h*4)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			v := byte(((x>>4)^(y>>4))&1) * 40
			s.base[i], s.base[i+1], s.base[i+2], s.base[i+3] = 36+v+byte(x>>5), 32+v, 30+v+byte(y>>5), 255
		}
	}
	s.pix = slices.Clone(s.base)
	if !cpu {
		tex, err := dev.NewTexture(w, h, d3d.FormatBGRA, d3d.UsageDefault, d3d.BindRenderTarget|d3d.BindShaderResource, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { com.Release(tex) })
		s.tex = tex
	}
	return s
}

// frame draws image n and returns it as a frame captured now.
func (s *synthetic) frame(n int) capture.Frame {
	bx, by := s.box[0], s.box[1]
	for y := by; y < by+boxH && y < s.h; y++ {
		lo, hi := (y*s.w+bx)*4, (y*s.w+min(bx+boxW, s.w))*4
		copy(s.pix[lo:hi], s.base[lo:hi])
	}
	bx, by = (n*24)%(s.w-boxW), (n*14)%(s.h-boxH)
	s.box = [2]int{bx, by}
	for y := by; y < by+boxH; y++ {
		for x := bx; x < bx+boxW; x++ {
			i := (y*s.w + x) * 4
			s.pix[i], s.pix[i+1], s.pix[i+2] = byte(x+n*3), byte(y-n*5), byte((x^y)+n)
		}
	}
	f := capture.Frame{Width: s.w, Height: s.h, MonoUS: monoclock.NowUS(), Dirty: true}
	if s.cpu {
		f.CPU = s.pix
	} else {
		s.dev.UpdateSubresource(s.tex, s.pix, s.w*4)
		f.Texture = s.tex
	}
	return f
}

// run encodes n frames of src at fps, forcing a key frame at frame
// forceAt (none when negative), with SetBitrate(bitrateAt[1]) before frame
// bitrateAt[0] when it is positive, and returns the access units.
func run(t *testing.T, enc Encoder, src *synthetic, n, fps, forceAt int, bitrateAt [2]int) []wire.AU {
	t.Helper()
	var aus []wire.AU
	collect := func(wait time.Duration) {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		for len(aus) < n {
			select {
			case au, ok := <-enc.Output():
				if !ok {
					t.Fatalf("the output closed after %d access units", len(aus))
				}
				aus = append(aus, au)
			case <-timer.C:
				return
			}
		}
	}
	start := time.Now()
	for i := 0; i < n; i++ {
		if d := time.Until(start.Add(time.Duration(i) * time.Second / time.Duration(fps))); d > 0 {
			collect(d)
		}
		if bitrateAt[0] > 0 && i == bitrateAt[0] {
			if err := enc.SetBitrate(bitrateAt[1]); err != nil {
				t.Fatalf("SetBitrate(%d): %v", bitrateAt[1], err)
			}
		}
		f := src.frame(i)
		for tries := 0; ; tries++ {
			err := enc.Encode(f, i == forceAt)
			if err == nil {
				break
			}
			if !errors.Is(err, ErrBusy) || tries > 200 {
				t.Fatalf("Encode of frame %d: %v", i, err)
			}
			collect(2 * time.Millisecond)
		}
	}
	collect(10 * time.Second)
	took := time.Since(start)
	if len(aus) != n {
		t.Fatalf("%d access units for %d frames", len(aus), n)
	}
	var lat []float64
	var keys []int
	total := 0
	for i, au := range aus {
		lat = append(lat, float64(au.EncodeDoneMonoUS-au.CaptureMonoUS)/1000)
		total += len(au.Data)
		if au.Key {
			keys = append(keys, i)
		}
	}
	sort.Float64s(lat)
	info := enc.Info()
	t.Logf("%s (hardware %v) %dx%d: %d frames in %v (%.1f fps), capture→access unit p50 %.2f ms p95 %.2f ms, %d bytes (%.2f Mbps at %d fps), key frames %v",
		info.Name, info.Hardware, info.Width, info.Height, n, took.Round(time.Millisecond), float64(n)/took.Seconds(),
		lat[len(lat)/2], lat[len(lat)*95/100], total, float64(total)*8*float64(fps)/float64(n)/1e6, fps, keys)
	return aus
}

// check verifies the access units of a w×h stream with a forced key
// frame at forceAt and writes them to <name>.h264 when the run keeps
// artifacts.
func check(t *testing.T, name string, aus []wire.AU, w, h, forceAt int) {
	t.Helper()
	first := aus[0]
	types := NALTypes(first.Data)
	if !first.Key || !first.HasConfig || !bytes.Contains(types, []byte{5}) || !bytes.Contains(types, []byte{7}) || !bytes.Contains(types, []byte{8}) {
		t.Fatalf("first access unit: key %v config %v NAL types %v", first.Key, first.HasConfig, types)
	}
	if forceAt >= 0 {
		if au := aus[forceAt]; !au.Key || !au.HasConfig || !bytes.Contains(NALTypes(au.Data), []byte{5}) {
			t.Fatalf("access unit %d of the forced key frame: key %v config %v NAL types %v", forceAt, au.Key, au.HasConfig, NALTypes(au.Data))
		}
	}
	var stream []byte
	for i, au := range aus {
		if int(au.Width) != w || int(au.Height) != h {
			t.Fatalf("access unit %d is %dx%d, want %dx%d", i, au.Width, au.Height, w, h)
		}
		if i > 0 && au.CaptureMonoUS < aus[i-1].CaptureMonoUS {
			t.Fatalf("access unit %d is out of order", i)
		}
		if au.EncodeDoneMonoUS < au.CaptureMonoUS || len(au.Data) == 0 {
			t.Fatalf("access unit %d: captured %d done %d, %d bytes", i, au.CaptureMonoUS, au.EncodeDoneMonoUS, len(au.Data))
		}
		key, config := describe(au.Data)
		if key != au.Key || config != au.HasConfig {
			t.Fatalf("access unit %d flags key %v config %v, its NAL units say %v %v", i, au.Key, au.HasConfig, key, config)
		}
		stream = append(stream, au.Data...)
	}
	writeArtifact(t, name+".h264", stream)
}

// writeArtifact writes data to name in DESKHAND_WINTEST_ARTIFACTS when the
// run keeps artifacts.
func writeArtifact(t *testing.T, name string, data []byte) {
	t.Helper()
	dir := os.Getenv("DESKHAND_WINTEST_ARTIFACTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHardwareEncoderProducesIDR(t *testing.T) {
	lockThread(t)
	dev := testDevice(t)
	enc, err := newHW(dev, 3840, 2160, 3840, 2160, 60, 20_000_000)
	if err != nil {
		t.Fatalf("hardware encoder on %s: %v", dev.AdapterName, err)
	}
	defer enc.Close()
	if info := enc.Info(); !info.Hardware || info.Name == "" || info.Width != 3840 || info.Height != 2160 {
		t.Fatalf("info %+v", info)
	}
	aus := run(t, enc, newSynthetic(t, dev, 3840, 2160, false), 60, 60, 30, [2]int{})
	check(t, "hw_3840x2160", aus, 3840, 2160, 30)
}

func TestNewPrefersTheHardwareEncoder(t *testing.T) {
	lockThread(t)
	dev := testDevice(t)
	enc, err := New(dev, 1920, 1080, 1920, 1080, 60, 6_000_000)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	if info := enc.Info(); !info.Hardware {
		t.Fatalf("New chose %+v on %s", info, dev.AdapterName)
	}
}

func TestHardwareEncoderScalesToMaxWidth(t *testing.T) {
	lockThread(t)
	dev := testDevice(t)
	enc, err := newHW(dev, 3840, 2160, 1920, 1080, 60, 6_000_000)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	aus := run(t, enc, newSynthetic(t, dev, 3840, 2160, false), 120, 60, 60, [2]int{90, 3_000_000})
	check(t, "hw_3840x2160_to_1920x1080", aus, 1920, 1080, 60)
}

func TestSoftwareFallback(t *testing.T) {
	lockThread(t)
	dev := testDevice(t)
	for _, c := range []struct {
		name                   string
		srcW, srcH, outW, outH int
		bps                    int
	}{
		{"sw_1920x1080", 1920, 1080, 1920, 1080, 6_000_000},
		{"sw_3840x2160_to_1920x1080", 3840, 2160, 1920, 1080, 6_000_000},
		{"sw_3840x2160", 3840, 2160, 3840, 2160, 20_000_000},
	} {
		t.Run(c.name, func(t *testing.T) {
			enc, err := NewSoftware(dev, c.srcW, c.srcH, c.outW, c.outH, 60, c.bps)
			if err != nil {
				t.Fatal(err)
			}
			defer enc.Close()
			if info := enc.Info(); info.Hardware || info.Name == "" || info.Width != c.outW || info.Height != c.outH {
				t.Fatalf("info %+v", info)
			}
			aus := run(t, enc, newSynthetic(t, dev, c.srcW, c.srcH, false), 90, 30, 45, [2]int{60, c.bps / 2})
			check(t, c.name, aus, c.outW, c.outH, 45)
		})
	}
}

func TestEncodersTakeCPUFrames(t *testing.T) {
	lockThread(t)
	dev := testDevice(t)
	for _, hardware := range []bool{true, false} {
		mk := NewSoftware
		if hardware {
			mk = func(dev *d3d.Device, srcW, srcH, outW, outH, fps, bps int) (Encoder, error) {
				return newHW(dev, srcW, srcH, outW, outH, fps, bps)
			}
		}
		enc, err := mk(dev, 1280, 720, 1280, 720, 30, 4_000_000)
		if err != nil {
			t.Fatalf("hardware %v: %v", hardware, err)
		}
		aus := run(t, enc, newSynthetic(t, dev, 1280, 720, true), 30, 30, -1, [2]int{})
		enc.Close()
		name := "sw_cpu_1280x720"
		if hardware {
			name = "hw_cpu_1280x720"
		}
		check(t, name, aus, 1280, 720, -1)
	}
}

func TestEncodeRefusesAFrameOfAnotherSize(t *testing.T) {
	lockThread(t)
	dev := testDevice(t)
	for _, mk := range []func(*d3d.Device, int, int, int, int, int, int) (Encoder, error){New, NewSoftware} {
		enc, err := mk(dev, 1280, 720, 1280, 720, 30, 4_000_000)
		if err != nil {
			t.Fatal(err)
		}
		err = enc.Encode(capture.Frame{CPU: make([]byte, 640*360*4), Width: 640, Height: 360, Dirty: true}, false)
		enc.Close()
		if err == nil || errors.Is(err, ErrBusy) {
			t.Fatalf("%s: Encode of a 640x360 frame: %v", enc.Info().Name, err)
		}
	}
}

func TestCloseWithoutFramesEndsTheOutput(t *testing.T) {
	lockThread(t)
	dev := testDevice(t)
	for _, mk := range []func(*d3d.Device, int, int, int, int, int, int) (Encoder, error){New, NewSoftware} {
		enc, err := mk(dev, 1280, 720, 1280, 720, 30, 4_000_000)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		enc.Close()
		select {
		case _, ok := <-enc.Output():
			if ok {
				t.Fatalf("%s: an access unit without frames", enc.Info().Name)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: the output stays open after Close", enc.Info().Name)
		}
		t.Logf("%s closed in %v", enc.Info().Name, time.Since(start))
	}
}

func TestProbeFindsAnEncoder(t *testing.T) {
	lockThread(t)
	testDevice(t)
	start := time.Now()
	err := Probe()
	t.Logf("Probe: %v in %v", err, time.Since(start))
	if err != nil {
		t.Fatal(err)
	}
}

func TestEncodersInsertNoPeriodicIDR(t *testing.T) {
	lockThread(t)
	dev := testDevice(t)
	for _, mk := range []func(*d3d.Device, int, int, int, int, int, int) (Encoder, error){New, NewSoftware} {
		enc, err := mk(dev, 1280, 720, 1280, 720, 60, 4_000_000)
		if err != nil {
			t.Fatal(err)
		}
		aus := run(t, enc, newSynthetic(t, dev, 1280, 720, false), 150, 60, -1, [2]int{})
		enc.Close()
		for i, au := range aus[1:] {
			if au.Key {
				t.Fatalf("%s: access unit %d is an IDR without a request", enc.Info().Name, i+1)
			}
		}
	}
}
