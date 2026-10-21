package capture

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
)

// hookSource is a Source whose Next is next.
type hookSource struct {
	next   func(ctx context.Context, timeout time.Duration) (Frame, error)
	closed atomic.Int32
}

func (h *hookSource) Next(ctx context.Context, timeout time.Duration) (Frame, error) {
	return h.next(ctx, timeout)
}
func (h *hookSource) Device() *d3d.Device { return nil }
func (h *hookSource) Size() (int, int)    { return 640, 360 }
func (h *hookSource) Close()              { h.closed.Add(1) }

// idle waits timeout like a source without news.
func idle(ctx context.Context, timeout time.Duration) (Frame, error) {
	if err := sleepUntil(ctx, time.Now().Add(timeout)); err != nil {
		return Frame{}, err
	}
	return Frame{}, ErrNoNewFrame
}

// gdiStub gives its baseline capture at once and then a frame while
// changing reports true.
func gdiStub(changing func() bool) (*hookSource, *atomic.Int32) {
	var nexts atomic.Int32
	g := &hookSource{}
	g.next = func(ctx context.Context, timeout time.Duration) (Frame, error) {
		if nexts.Add(1) == 1 || !changing() {
			if nexts.Load() == 1 {
				return Frame{CPU: []byte{1}, Dirty: true}, nil
			}
			return idle(ctx, 0)
		}
		return Frame{CPU: []byte{2}, Dirty: true}, nil
	}
	return g, &nexts
}

type stallHarness struct {
	dd, gdi   *hookSource
	gdiNexts  *atomic.Int32
	gdiMade   atomic.Int32
	deviceEnd atomic.Int32
	src       *stallSource
}

func newStallHarness(dd *hookSource, changing func() bool) *stallHarness {
	h := &stallHarness{dd: dd}
	h.gdi, h.gdiNexts = gdiStub(changing)
	h.src = newStallSource(dd, func() (Source, error) {
		h.gdiMade.Add(1)
		return h.gdi, nil
	}, func() { h.deviceEnd.Add(1) })
	return h
}

// poll calls Next with the pipeline's wait until a frame or an error comes
// or the limit passes.
func (h *stallHarness) poll(limit time.Duration) (Frame, error, bool) {
	end := time.Now().Add(limit)
	for time.Now().Before(end) {
		f, err := h.src.Next(context.Background(), 25*time.Millisecond)
		if !errors.Is(err, ErrNoNewFrame) {
			return f, err, true
		}
	}
	return Frame{}, nil, false
}

func TestStalledDuplicationOfAChangingScreenSwitchesToGDI(t *testing.T) {
	t.Parallel()
	start := time.Now()
	dd := &hookSource{next: idle}
	h := newStallHarness(dd, func() bool { return time.Since(start) > stallAfter+stallProbeEvery })
	f, err, ok := h.poll(5 * time.Second)
	took := time.Since(start)
	if !ok || err != nil || f.CPU == nil || f.Texture != 0 {
		t.Fatalf("after %v: frame %+v, error %v, ok %v; want a GDI frame", took, f, err, ok)
	}
	if took < stallAfter {
		t.Fatalf("switched after %v, before the duplication was silent for %v", took, stallAfter)
	}
	if dd.closed.Load() != 1 || h.gdi.closed.Load() != 0 || h.deviceEnd.Load() != 0 {
		t.Fatalf("closed: duplication %d, GDI %d, device %d; want the duplication only", dd.closed.Load(), h.gdi.closed.Load(), h.deviceEnd.Load())
	}
	if g, err := h.src.Next(context.Background(), 0); err != nil || g.CPU == nil {
		t.Fatalf("after the switch Next gave %+v, %v; want the GDI source's", g, err)
	}
	h.src.Close()
	if dd.closed.Load() != 1 || h.gdi.closed.Load() != 1 || h.deviceEnd.Load() != 1 || h.gdiMade.Load() != 1 {
		t.Fatalf("after Close: duplication %d, GDI %d, device %d, GDI made %d", dd.closed.Load(), h.gdi.closed.Load(), h.deviceEnd.Load(), h.gdiMade.Load())
	}
}

func TestStaticScreenWithSilentDuplicationStaysOnItWithFewCaptures(t *testing.T) {
	t.Parallel()
	dd := &hookSource{next: idle}
	h := newStallHarness(dd, func() bool { return false })
	if f, err, ok := h.poll(3 * time.Second); ok {
		t.Fatalf("static screen gave %+v, %v", f, err)
	}
	if dd.closed.Load() != 0 || h.gdiMade.Load() != 1 {
		t.Fatalf("duplication closed %d times, GDI made %d times; want 0 and 1", dd.closed.Load(), h.gdiMade.Load())
	}
	if n := h.gdiNexts.Load(); n > 6 {
		t.Fatalf("%d GDI captures in 3 s of silence, want about one per %v", n, stallProbeEvery)
	}
}

func TestDuplicationThatPresentsTheChangeIsKept(t *testing.T) {
	t.Parallel()
	start := time.Now()
	var confirmed atomic.Bool
	dd := &hookSource{}
	dd.next = func(ctx context.Context, timeout time.Duration) (Frame, error) {
		if timeout == stallConfirmWait {
			confirmed.Store(true)
			return Frame{Texture: 1, Dirty: true}, nil
		}
		return idle(ctx, timeout)
	}
	h := newStallHarness(dd, func() bool { return time.Since(start) > stallAfter })
	f, err, ok := h.poll(5 * time.Second)
	if !ok || err != nil || f.Texture != 1 || !confirmed.Load() {
		t.Fatalf("frame %+v, error %v, ok %v, confirmed %v; want the duplication's frame", f, err, ok, confirmed.Load())
	}
	if dd.closed.Load() != 0 || h.gdi.closed.Load() != 1 || h.src.stalled {
		t.Fatalf("closed: duplication %d, GDI %d, stalled %v; want the comparison dropped and the duplication kept", dd.closed.Load(), h.gdi.closed.Load(), h.src.stalled)
	}
}

func TestAnImageOfTheDuplicationEndsTheComparison(t *testing.T) {
	t.Parallel()
	var images atomic.Int32
	dd := &hookSource{}
	dd.next = func(ctx context.Context, timeout time.Duration) (Frame, error) {
		if images.Load() == 1 {
			images.Store(2)
			return Frame{Texture: 1, Dirty: true}, nil
		}
		return idle(ctx, timeout)
	}
	h := newStallHarness(dd, func() bool { return false })
	if _, _, ok := h.poll(stallAfter + 700*time.Millisecond); ok {
		t.Fatal("a frame came from a static screen")
	}
	if h.gdiMade.Load() != 1 {
		t.Fatalf("GDI made %d times, want 1", h.gdiMade.Load())
	}
	images.Store(1)
	if f, err, ok := h.poll(time.Second); !ok || err != nil || f.Texture != 1 {
		t.Fatalf("frame %+v, error %v, ok %v", f, err, ok)
	}
	if h.gdi.closed.Load() != 1 || h.src.probe != nil {
		t.Fatalf("the comparison stays after an image: GDI closed %d", h.gdi.closed.Load())
	}
}

func TestWaitOfALongNextEndsWhereSilenceReachesTheLimit(t *testing.T) {
	t.Parallel()
	dd := &hookSource{next: idle}
	h := newStallHarness(dd, func() bool { return false })
	now := time.Now()
	deadline := now.Add(time.Minute)
	h.src.lastImage = now.Add(-stallAfter / 2)
	if got := h.src.wait(now, deadline); got != stallAfter/2 {
		t.Fatalf("wait %v, want %v", got, stallAfter/2)
	}
	h.src.lastImage, h.src.probeAt = now.Add(-2*stallAfter), now.Add(stallProbeEvery)
	if got := h.src.wait(now, deadline); got != stallProbeEvery {
		t.Fatalf("wait %v, want %v", got, stallProbeEvery)
	}
	if got := h.src.wait(now, now.Add(time.Millisecond)); got != time.Millisecond {
		t.Fatalf("wait %v, want the deadline's 1ms", got)
	}
}
