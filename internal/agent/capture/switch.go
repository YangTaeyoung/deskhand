package capture

import (
	"context"
	"errors"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
)

// Retries of a display that Desktop Duplication refuses.
const (
	// dupRetryFor is how long Open keeps duplicating the output again,
	// every dupRetryEvery, while DuplicateOutput fails with E_INVALIDARG.
	dupRetryFor   = 2 * time.Second
	dupRetryEvery = 100 * time.Millisecond
	// ddRetryEvery is the interval at which a GDI source that Open made
	// after a passing refusal duplicates the output again.
	ddRetryEvery = 2 * time.Second
)

// retryDuplicate reports whether Open duplicates the output again after a
// DuplicateOutput that failed with hr, waited after the first one: for
// E_INVALIDARG, the output duplicated already in this process, while
// another try fits within dupRetryFor.
func retryDuplicate(hr com.HRESULT, waited time.Duration) bool {
	return hr == com.E_INVALIDARG && waited+dupRetryEvery <= dupRetryFor
}

// passingRefusal reports whether a GDI source opened after DuplicateOutput
// failed with hr tries Desktop Duplication again: for E_INVALIDARG,
// E_ACCESSDENIED and DXGI_ERROR_NOT_CURRENTLY_AVAILABLE.
func passingRefusal(hr com.HRESULT) bool {
	switch hr {
	case com.E_INVALIDARG, com.E_ACCESSDENIED, com.DXGI_ERROR_NOT_CURRENTLY_AVAILABLE:
		return true
	}
	return false
}

// switchSource serves a fallback source until a Desktop Duplication source
// that redup makes, tried every interval, works; from then on it serves
// that one. Both use one device, which done closes after the source
// served last.
type switchSource struct {
	cur      Source
	switched bool
	// redup makes the Desktop Duplication source, or reports false while
	// the output refuses to be duplicated.
	redup func() (Source, bool)
	every time.Duration
	// next is when redup is tried next.
	next time.Time
	done func()
}

// newSwitchSource serves fallback and tries redup first every after now.
func newSwitchSource(fallback Source, redup func() (Source, bool), every time.Duration, done func()) *switchSource {
	return &switchSource{cur: fallback, redup: redup, every: every, next: time.Now().Add(every), done: done}
}

// Next returns what the fallback's Next does, waiting for it no longer than
// until the next try of redup. Once a try works it closes the fallback and
// returns ErrReset; the later calls go to the new source.
func (s *switchSource) Next(ctx context.Context, timeout time.Duration) (Frame, error) {
	if s.switched {
		return s.cur.Next(ctx, timeout)
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return Frame{}, err
		}
		now := time.Now()
		if !now.Before(s.next) {
			if src, ok := s.redup(); ok {
				s.cur.Close()
				s.cur, s.switched = src, true
				return Frame{}, ErrReset
			}
			s.next = now.Add(s.every)
		}
		f, err := s.cur.Next(ctx, max(min(deadline.Sub(now), s.next.Sub(now)), 0))
		if !errors.Is(err, ErrNoNewFrame) || !time.Now().Before(deadline) {
			return f, err
		}
	}
}

func (s *switchSource) Device() *d3d.Device { return s.cur.Device() }

func (s *switchSource) Size() (int, int) { return s.cur.Size() }

func (s *switchSource) Close() {
	s.cur.Close()
	if s.done != nil {
		s.done()
	}
}
