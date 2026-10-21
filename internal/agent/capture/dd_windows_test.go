package capture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
)

// openOrSkip opens a source of display index and skips the test, naming
// the reason, when the session cannot be captured now.
func openOrSkip(t *testing.T, index int) Source {
	t.Helper()
	src, err := Open(index)
	var u *Unavailable
	if errors.As(err, &u) {
		t.Skipf("the session cannot be captured now: %v", u)
	}
	if err != nil {
		t.Fatalf("Open(%d): %v", index, err)
	}
	return src
}

// firstImage returns the first dirty frame of src within 500 ms.
func firstImage(t *testing.T, src Source) Frame {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		f, err := src.Next(context.Background(), time.Until(deadline))
		switch {
		case err == nil && f.Dirty:
			return f
		case err == nil, errors.Is(err, ErrNoNewFrame), errors.Is(err, ErrReset):
		default:
			t.Fatalf("Next: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("no image within 500 ms of Open")
		}
	}
}

// litShare copies the texture of f to a staging texture and returns the
// share of every seventh pixel of every seventh row brighter than
// near-black.
func litShare(t *testing.T, dev *d3d.Device, f Frame) float64 {
	t.Helper()
	stage, err := dev.NewTexture(f.Width, f.Height, d3d.FormatBGRA, d3d.UsageStaging, 0, d3d.CPUAccessRead)
	if err != nil {
		t.Fatal(err)
	}
	defer com.Release(stage)
	dev.CopyResource(stage, f.Texture)
	m, err := dev.Map(stage, d3d.MapRead)
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Unmap(stage)
	pitch := int(m.RowPitch)
	pix := unsafe.Slice((*byte)(m.Data), pitch*(f.Height-1)+f.Width*4)
	n, lit := 0, 0
	for y := 0; y < f.Height; y += 7 {
		for x := 0; x < f.Width; x += 7 {
			i := y*pitch + x*4
			n++
			if pix[i] > 8 || pix[i+1] > 8 || pix[i+2] > 8 {
				lit++
			}
		}
	}
	return float64(lit) / float64(n)
}

func TestFirstImageOfEachDuplicationIsTheDesktop(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	reference := 0.0
	for i := range 6 {
		src := openOrSkip(t, 0)
		if _, ok := src.(*ddSource); !ok {
			src.Close()
			t.Skipf("the source is %T, not Desktop Duplication", src)
		}
		start := time.Now()
		f := firstImage(t, src)
		share := litShare(t, src.Device(), f)
		t.Logf("duplication %d: first image after %v, %.4f lit", i, time.Since(start), share)
		if i == 0 {
			g, err := openGDI(nil, src.(*ddSource).disp, 1)
			if err != nil {
				t.Fatalf("openGDI: %v", err)
			}
			n, lit := 0, 0
			for y := 0; y < f.Height; y += 7 {
				for x := 0; x < f.Width; x += 7 {
					p := g.bufs[0].pix[(y*f.Width+x)*4:]
					n++
					if p[0] > 8 || p[1] > 8 || p[2] > 8 {
						lit++
					}
				}
			}
			g.Close()
			reference = float64(lit) / float64(n)
			t.Logf("GDI: %.4f lit", reference)
			if reference < 0.01 {
				src.Close()
				t.Skipf("display 0 is black to GDI as well (%.4f lit)", reference)
			}
		}
		src.Close()
		if share < reference/2 {
			t.Fatalf("duplication %d: the first image has %.4f lit pixels, GDI %.4f", i, share, reference)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestGDIImageFillsTheDuplicationTexture(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	src := openOrSkip(t, 0)
	defer src.Close()
	s, ok := src.(*ddSource)
	if !ok {
		t.Skipf("the source is %T, not Desktop Duplication", src)
	}
	if !s.gdiAllowed() {
		t.Fatalf("a source of display %+v with %dx%d textures cannot take a GDI image", s.disp, s.w, s.h)
	}
	f, ok := s.gdiImage()
	if !ok {
		t.Fatal("gdiImage failed")
	}
	if !f.Dirty || f.Texture != s.tex || f.Width != s.w || f.Height != s.h || f.MonoUS == 0 || s.awaiting || !s.haveImage {
		t.Fatalf("frame %+v, awaiting %v, have image %v", f, s.awaiting, s.haveImage)
	}
	if share := litShare(t, s.dev, f); share < 0.01 {
		t.Fatalf("the GDI image in the texture has %.4f lit pixels", share)
	}
}

func TestNextNoNewFrameOnStaticScreen(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	src := openOrSkip(t, 0)
	defer src.Close()
	w, h := src.Size()
	f := firstImage(t, src)
	t.Logf("source %T %dx%d on %q, first frame %dx%d at %d µs, texture %#x, %d CPU bytes, pointer %+v", src, w, h,
		src.Device().AdapterName, f.Width, f.Height, f.MonoUS, f.Texture, len(f.CPU), f.Pointer)
	if f.Width != w || f.Height != h || f.MonoUS == 0 || (f.Texture == 0) == (f.CPU == nil) || (f.CPU != nil && len(f.CPU) != w*h*4) {
		t.Fatalf("first frame %dx%d mono %d texture %#x cpu %d bytes for a %dx%d source", f.Width, f.Height, f.MonoUS, f.Texture, len(f.CPU), w, h)
	}
	for range 3 {
		start := time.Now()
		f, err := src.Next(context.Background(), 100*time.Millisecond)
		took := time.Since(start)
		t.Logf("Next(100 ms) after %v: dirty %v, %v", took, f.Dirty, err)
		switch {
		case errors.Is(err, ErrNoNewFrame):
			if took < 90*time.Millisecond {
				t.Fatalf("ErrNoNewFrame after %v of a 100 ms timeout", took)
			}
		case err != nil:
			t.Fatalf("Next: %v", err)
		case f.Width != w || f.Height != h:
			t.Fatalf("frame %dx%d for a %dx%d source", f.Width, f.Height, w, h)
		}
		if took > time.Second {
			t.Fatalf("Next(100 ms) took %v", took)
		}
	}
}

// served is the source that src serves frames from now.
func served(src Source) Source {
	if s, ok := src.(*switchSource); ok {
		return s.cur
	}
	return src
}

func TestSecondOpenOfADisplayServesGDIUntilTheFirstCloses(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	first := openOrSkip(t, 0)
	closed := false
	defer func() {
		if !closed {
			first.Close()
		}
	}()
	if _, ok := first.(*ddSource); !ok {
		t.Skipf("the first source is %T, not Desktop Duplication", first)
	}
	start := time.Now()
	second := openOrSkip(t, 0)
	defer second.Close()
	took := time.Since(start)
	t.Logf("the second Open took %v and gave %T serving %T", took, second, served(second))
	if took < dupRetryFor-dupRetryEvery || took > dupRetryFor+time.Second {
		t.Fatalf("the second Open of a duplicated display took %v, want about %v of retries", took, dupRetryFor)
	}
	if _, ok := served(second).(*gdiSource); !ok {
		t.Fatalf("the second source of a duplicated display serves %T", served(second))
	}
	f := firstImage(t, second)
	lit := 0
	for i := 0; i+2 < len(f.CPU); i += 4 * 97 {
		if f.CPU[i] > 8 || f.CPU[i+1] > 8 || f.CPU[i+2] > 8 {
			lit++
		}
	}
	if lit == 0 {
		t.Fatal("the GDI frame is black")
	}
	firstImage(t, first)

	first.Close()
	closed = true
	closedAt := time.Now()
	for {
		_, err := second.Next(context.Background(), 100*time.Millisecond)
		if errors.Is(err, ErrReset) {
			break
		}
		if err != nil && !errors.Is(err, ErrNoNewFrame) {
			t.Fatalf("Next of the GDI source after the first closed: %v", err)
		}
		if time.Since(closedAt) > ddRetryEvery+time.Second {
			t.Fatalf("no switch to Desktop Duplication within %v of the first source closing", ddRetryEvery+time.Second)
		}
	}
	t.Logf("switched to %T %v after the first source closed", served(second), time.Since(closedAt))
	if _, ok := served(second).(*ddSource); !ok {
		t.Fatalf("after ErrReset the source serves %T", served(second))
	}
	if f := firstImage(t, second); f.Texture == 0 || f.CPU != nil {
		t.Fatalf("the first frame after the switch has texture %#x and %d CPU bytes", f.Texture, len(f.CPU))
	}
}

func TestOpenGetsDuplicationWhenTheOtherSourceClosesWithinTheRetries(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	opened := make(chan Source, 1)
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		src, err := Open(0)
		if err != nil {
			opened <- nil
			return
		}
		opened <- src
		<-release
		time.Sleep(300 * time.Millisecond)
		src.Close()
	}()
	first := <-opened
	if first == nil {
		t.Skip("display 0 cannot be opened now")
	}
	if _, ok := first.(*ddSource); !ok {
		close(release)
		<-done
		t.Skipf("the first source is %T, not Desktop Duplication", first)
	}
	close(release)
	start := time.Now()
	second := openOrSkip(t, 0)
	defer second.Close()
	<-done
	took := time.Since(start)
	t.Logf("Open gave %T after %v", second, took)
	if _, ok := second.(*ddSource); !ok || took > time.Second {
		t.Fatalf("Open while another source of the display closed within 300 ms gave %T after %v", second, took)
	}
	firstImage(t, second)
}

func TestProbeIsReadyInTheConsoleSession(t *testing.T) {
	info, err := session.Current()
	if err != nil || info.SessionID == 0 || info.Protocol != session.ProtocolConsole || info.InputDesktop != session.DesktopDefault || info.IsLocked() {
		t.Skipf("needs the unlocked console session; session %+v (%v)", info, err)
	}
	forgetProbe()
	start := time.Now()
	err = Probe()
	t.Logf("Probe without an open source: %v in %v", err, time.Since(start))
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	start = time.Now()
	if err := Probe(); err != nil || time.Since(start) > 10*time.Millisecond {
		t.Fatalf("cached Probe: %v in %v", err, time.Since(start))
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	src := openOrSkip(t, 0)
	defer src.Close()
	forgetProbe()
	start = time.Now()
	err = Probe()
	t.Logf("Probe with an open source: %v in %v", err, time.Since(start))
	if err != nil {
		t.Fatalf("Probe with an open source: %v", err)
	}
	firstImage(t, src)
}

func TestCaptureInSessionZeroIsSession0(t *testing.T) {
	if os.Getenv("SSH_CONNECTION") == "" {
		t.Skip("needs a process started by sshd, which runs in Session 0; run scripts/wintest.sh --ssh")
	}
	forgetProbe()
	for name, err := range map[string]error{
		"Open":         func() error { _, err := Open(0); return err }(),
		"CaptureStill": func() error { _, err := CaptureStill(0, FormatPNG, 0, 0); return err }(),
		"Probe":        Probe(),
	} {
		var u *Unavailable
		if !errors.As(err, &u) || u.Reason != ReasonSession0 {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestProbeFailureHasAFixedReason(t *testing.T) {
	console := session.Info{SessionID: 1, ConnectState: session.ConnectActive, Protocol: session.ProtocolConsole, InputDesktop: session.DesktopDefault}
	rdp, locked := console, console
	yes := true
	rdp.Protocol, locked.Locked = session.ProtocolRDP, &yes
	denied := fmt.Errorf("BitBlt of the pixel (0,0): %w", windows.ERROR_ACCESS_DENIED)
	other := fmt.Errorf("BitBlt of the pixel (0,0): %w", windows.ERROR_INVALID_HANDLE)
	noOutput := &Unavailable{Reason: ReasonNoOutput, Detail: "no display is attached to the desktop"}
	for _, c := range []struct {
		err  error
		s    session.Info
		want string
	}{
		{denied, rdp, ReasonRDPOutputSuppressed},
		{other, rdp, ReasonRDPOutputSuppressed},
		{errors.New("GetDC of the screen failed"), rdp, ReasonRDPOutputSuppressed},
		{denied, console, ReasonAccessLost},
		{other, console, ReasonAccessLost},
		{errors.New("GetDC of the screen failed"), console, ReasonAccessLost},
		{other, locked, ReasonSessionLocked},
		{noOutput, console, ReasonNoOutput},
	} {
		u := probeFailure(c.err, c.s)
		if u == nil || u.Reason != c.want || u.Detail == "" {
			t.Errorf("%v in %s: %+v, want %s", c.err, c.s.Protocol, u, c.want)
		}
	}
}

func TestStillsReuseTheirStagingTexture(t *testing.T) {
	d := displaysOrSkip(t)[0]
	stage := func() uintptr {
		var tex uintptr
		if err := onStillWorker(func(c *devCache) error {
			if k := c.devs[d]; k != nil {
				tex = k.stage
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return tex
	}
	if stillOrSkip(t, 0, FormatPNG, 320, 0).Method != MethodDD {
		t.Skip("the still was not taken with Desktop Duplication")
	}
	first := stage()
	stillOrSkip(t, 0, FormatPNG, 320, 0)
	if first == 0 || stage() != first {
		t.Fatalf("staging texture %#x, then %#x", first, stage())
	}
}

func TestGDIStillSourceHasOneSection(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	d := displaysOrSkip(t)[0]
	g, err := openGDI(nil, d, 1)
	var u *Unavailable
	if errors.As(classifyErr(err), &u) {
		t.Skipf("the session cannot be captured now: %v", u)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if g.bufs[0].bmp == 0 || g.bufs[1].bmp != 0 {
		t.Fatalf("sections %#x %#x", g.bufs[0].bmp, g.bufs[1].bmp)
	}
	st, err := g.still(DefaultStillMaxWidth)
	if err != nil || st.method != MethodGDI || st.img.Bounds().Dx() != DefaultStillMaxWidth {
		t.Fatalf("still %v: %+v", err, st.img.Bounds())
	}
	for range 2 {
		if f, err := g.Next(context.Background(), 50*time.Millisecond); err != nil || !f.Dirty || len(f.CPU) != d.Width*d.Height*4 {
			t.Fatalf("Next of a one-section source: %v, dirty %v, %d bytes", err, f.Dirty, len(f.CPU))
		}
	}
}

func TestGDIFrameStaysWhileNoNewFrameComes(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	index := 0
	if list, err := display.List(); err == nil && len(list) > 1 {
		index = 1
	}
	first := openOrSkip(t, index)
	defer first.Close()
	src := Source(first)
	if _, ok := first.(*ddSource); ok {
		src = openOrSkip(t, index)
		defer src.Close()
	}
	if _, ok := served(src).(*gdiSource); !ok {
		t.Fatalf("the source serves %T, not GDI", served(src))
	}
	f := firstImage(t, src)
	kept := bytes.Clone(f.CPU)
	for range 3 {
		_, err := src.Next(context.Background(), 100*time.Millisecond)
		if !errors.Is(err, ErrNoNewFrame) {
			t.Skipf("display %d changed (%v); the pixels of an unchanged frame cannot be compared", index, err)
		}
		if !bytes.Equal(f.CPU, kept) {
			t.Fatal("the pixels of the latest frame changed while Next returned ErrNoNewFrame")
		}
	}
}
