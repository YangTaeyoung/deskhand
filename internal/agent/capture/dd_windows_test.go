package capture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/YangTaeyoung/deskhand/internal/agent/session"
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

func TestSecondOpenOfADisplayFallsBackToGDI(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	first := openOrSkip(t, 0)
	defer first.Close()
	if _, ok := first.(*ddSource); !ok {
		t.Skipf("the first source is %T, not Desktop Duplication", first)
	}
	second := openOrSkip(t, 0)
	defer second.Close()
	if _, ok := second.(*gdiSource); !ok {
		t.Fatalf("the second source of a duplicated display is %T", second)
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
}

func TestProbeIsReadyInTheConsoleSession(t *testing.T) {
	info, err := session.Current()
	if err != nil || info.SessionID == 0 || info.Protocol != session.ProtocolConsole || info.InputDesktop != session.DesktopDefault || info.Locked {
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
	rdp.Protocol, locked.Locked = session.ProtocolRDP, true
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
