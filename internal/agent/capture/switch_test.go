package capture

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
)

func TestOpenRetriesOnlyAnInvalidArgDuplicationForTwoSeconds(t *testing.T) {
	for _, c := range []struct {
		hr     com.HRESULT
		waited time.Duration
		want   bool
	}{
		{com.E_INVALIDARG, 0, true},
		{com.E_INVALIDARG, time.Second, true},
		{com.E_INVALIDARG, dupRetryFor - dupRetryEvery, true},
		{com.E_INVALIDARG, dupRetryFor - dupRetryEvery + time.Millisecond, false},
		{com.E_INVALIDARG, dupRetryFor, false},
		{com.E_ACCESSDENIED, 0, false},
		{com.DXGI_ERROR_NOT_CURRENTLY_AVAILABLE, 0, false},
		{com.DXGI_ERROR_UNSUPPORTED, 0, false},
		{com.S_OK, 0, false},
	} {
		if got := retryDuplicate(c.hr, c.waited); got != c.want {
			t.Errorf("retryDuplicate(%v, %v) = %v, want %v", c.hr, c.waited, got, c.want)
		}
	}
	if dupRetryFor != 2*time.Second || dupRetryEvery <= 0 || dupRetryEvery > 250*time.Millisecond {
		t.Fatalf("Open retries for %v every %v", dupRetryFor, dupRetryEvery)
	}
}

func TestGDIAfterAPassingRefusalTriesDuplicationEveryTwoSeconds(t *testing.T) {
	for hr, want := range map[com.HRESULT]bool{
		com.E_INVALIDARG:                       true,
		com.E_ACCESSDENIED:                     true,
		com.DXGI_ERROR_NOT_CURRENTLY_AVAILABLE: true,
		com.DXGI_ERROR_UNSUPPORTED:             false,
		com.DXGI_ERROR_ACCESS_DENIED:           false,
		com.S_OK:                               false,
	} {
		if got := passingRefusal(hr); got != want {
			t.Errorf("passingRefusal(%v) = %v, want %v", hr, got, want)
		}
	}
	if ddRetryEvery != 2*time.Second {
		t.Fatalf("a GDI source tries Desktop Duplication every %v", ddRetryEvery)
	}
}

// stubSource is a Source whose Next waits its timeout and returns
// ErrNoNewFrame, or returns frame at once when frame is set.
type stubSource struct {
	name  string
	frame *Frame
	log   *[]string
	mu    *sync.Mutex

	nexts  int
	closed bool
}

func (s *stubSource) Next(ctx context.Context, timeout time.Duration) (Frame, error) {
	s.nexts++
	if s.frame != nil {
		return *s.frame, nil
	}
	if err := sleepUntil(ctx, time.Now().Add(timeout)); err != nil {
		return Frame{}, err
	}
	return Frame{}, ErrNoNewFrame
}

func (s *stubSource) Device() *d3d.Device { return nil }
func (s *stubSource) Size() (int, int)    { return 640, 360 }

func (s *stubSource) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	*s.log = append(*s.log, "close "+s.name)
}

// switchHarness is a switchSource over a GDI stub whose redup fails until
// ok is set and then gives a Desktop Duplication stub.
type switchHarness struct {
	mu        sync.Mutex
	log       []string
	gdi, dd   *stubSource
	ok        bool
	tries     []time.Time
	src       *switchSource
	createdAt time.Time
}

func newSwitchHarness(every time.Duration) *switchHarness {
	h := &switchHarness{}
	h.gdi = &stubSource{name: "gdi", log: &h.log, mu: &h.mu}
	h.dd = &stubSource{name: "dd", log: &h.log, mu: &h.mu, frame: &Frame{Texture: 1, Width: 640, Height: 360, Dirty: true}}
	h.createdAt = time.Now()
	h.src = newSwitchSource(h.gdi, func() (Source, bool) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.tries = append(h.tries, time.Now())
		if !h.ok {
			return nil, false
		}
		return h.dd, true
	}, every, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.log = append(h.log, "close device")
	})
	return h
}

func TestSwitchSourceServesGDIUntilADuplicationWorksThenResets(t *testing.T) {
	const every = 40 * time.Millisecond
	h := newSwitchHarness(every)
	ctx := context.Background()
	for range 6 {
		if _, err := h.src.Next(ctx, 25*time.Millisecond); !errors.Is(err, ErrNoNewFrame) {
			t.Fatalf("Next on the GDI source: %v", err)
		}
	}
	h.mu.Lock()
	tries := append([]time.Time(nil), h.tries...)
	h.ok = true
	h.mu.Unlock()
	if len(tries) < 2 || len(tries) > 4 {
		t.Fatalf("%d tries of Desktop Duplication in about %v at an interval of %v", len(tries), 6*25*time.Millisecond, every)
	}
	if first := tries[0].Sub(h.createdAt); first < every-5*time.Millisecond {
		t.Fatalf("the first try came %v after the source was made, before the interval of %v", first, every)
	}
	for i := 1; i < len(tries); i++ {
		if gap := tries[i].Sub(tries[i-1]); gap < every-5*time.Millisecond {
			t.Fatalf("tries %d and %d came %v apart, within the interval of %v", i-1, i, gap, every)
		}
	}
	start := time.Now()
	_, err := h.src.Next(ctx, time.Second)
	if !errors.Is(err, ErrReset) {
		t.Fatalf("Next once the duplication works: %v, want ErrReset", err)
	}
	if took := time.Since(start); took > every+30*time.Millisecond {
		t.Fatalf("ErrReset came %v into a Next of 1 s; the try is due within %v", took, every)
	}
	if !h.gdi.closed || h.dd.closed {
		t.Fatalf("after the switch the GDI source is closed %v and the duplication %v", h.gdi.closed, h.dd.closed)
	}
	for range 3 {
		f, err := h.src.Next(ctx, 25*time.Millisecond)
		if err != nil || f.Texture != 1 {
			t.Fatalf("Next after the switch: %+v, %v", f, err)
		}
	}
	h.mu.Lock()
	triesAfter := len(h.tries)
	h.mu.Unlock()
	if triesAfter != len(tries)+1 || h.dd.nexts != 3 {
		t.Fatalf("%d tries after the switch, %d calls of the duplication's Next", triesAfter-len(tries)-1, h.dd.nexts)
	}
	h.src.Close()
	if want := []string{"close gdi", "close dd", "close device"}; !slices.Equal(h.log, want) {
		t.Fatalf("closes %v, want %v", h.log, want)
	}
}

func TestSwitchSourceClosesGDIAndThenTheDevice(t *testing.T) {
	h := newSwitchHarness(time.Hour)
	if _, err := h.src.Next(context.Background(), 0); !errors.Is(err, ErrNoNewFrame) {
		t.Fatalf("Next: %v", err)
	}
	h.src.Close()
	if want := []string{"close gdi", "close device"}; !slices.Equal(h.log, want) {
		t.Fatalf("closes %v, want %v", h.log, want)
	}
	if len(h.tries) != 0 {
		t.Fatalf("%d tries before the first interval passed", len(h.tries))
	}
}
