package capture

import (
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
	"time"

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
