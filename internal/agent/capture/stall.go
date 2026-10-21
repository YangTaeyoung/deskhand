package capture

import (
	"context"
	"errors"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
)

// Detection of a Desktop Duplication source that presents no more images
// while the screen changes.
const (
	// stallAfter is how long a Desktop Duplication source gives no image
	// before stallSource compares the screen with GDI.
	stallAfter = time.Second
	// stallProbeEvery is the time between two GDI captures of the comparison.
	stallProbeEvery = 500 * time.Millisecond
	// stallConfirmWait is how long the duplication gets to present the image
	// that GDI found changed before the source counts it as stalled.
	stallConfirmWait = 200 * time.Millisecond
)

// stallSource serves a Desktop Duplication source until it stalls: it gives
// no image for stallAfter and a GDI capture of the screen differs from the
// one before it, which the duplication does not present within
// stallConfirmWait either. From then on it serves the GDI source that saw
// the change, and closes the duplication; the GDI source keeps serving until
// the source closes. Both use one device, which done closes after the
// source served last.
type stallSource struct {
	dd Source
	// openGDI makes the GDI source whose first capture is the baseline of the
	// comparison.
	openGDI func() (Source, error)
	done    func()

	// cur is the source that serves; it is dd until the stall.
	cur       Source
	lastImage time.Time
	// probe is the GDI source that compares the screen, nil while the
	// duplication presents or the silence is shorter than stallAfter.
	probe   Source
	probeAt time.Time
	stalled bool
}

// newStallSource serves dd and compares its silence with GDI sources made
// by openGDI.
func newStallSource(dd Source, openGDI func() (Source, error), done func()) *stallSource {
	return &stallSource{dd: dd, cur: dd, openGDI: openGDI, done: done, lastImage: time.Now()}
}

// Next returns the next frame of the serving source. Before a stall it waits
// for the duplication up to timeout, in slices of stallProbeEvery while the
// silence lasts for stallAfter, and compares the screen with GDI between the
// slices.
func (s *stallSource) Next(ctx context.Context, timeout time.Duration) (Frame, error) {
	if s.stalled {
		return s.cur.Next(ctx, timeout)
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return Frame{}, err
		}
		now := time.Now()
		f, err := s.dd.Next(ctx, s.wait(now, deadline))
		switch {
		case err == nil:
			if f.Dirty {
				s.presented()
			}
			return f, nil
		case !errors.Is(err, ErrNoNewFrame):
			s.presented()
			return Frame{}, err
		}
		if g, gerr, ok := s.compare(ctx); ok {
			return g, gerr
		}
		if !time.Now().Before(deadline) {
			return Frame{}, ErrNoNewFrame
		}
	}
}

// wait is how long the duplication may be waited for at now: until the
// deadline, and until the silence reaches stallAfter or the next comparison
// is due.
func (s *stallSource) wait(now, deadline time.Time) time.Duration {
	until := deadline
	next := s.lastImage.Add(stallAfter)
	if !now.Before(next) {
		next = s.probeAt
	}
	if next.Before(until) {
		until = next
	}
	return max(until.Sub(now), 0)
}

// presented records an image of the duplication, which ends the comparison.
func (s *stallSource) presented() {
	s.lastImage = time.Now()
	s.dropProbe()
}

func (s *stallSource) dropProbe() {
	if s.probe != nil {
		s.probe.Close()
		s.probe = nil
	}
	s.probeAt = time.Time{}
}

// compare takes a GDI capture when one is due and the duplication has been
// silent for stallAfter. ok reports that f and err are to be returned: the
// frame of the GDI source once the source counts as stalled, or what the
// duplication gave while it was waited for to confirm the change.
func (s *stallSource) compare(ctx context.Context) (f Frame, err error, ok bool) {
	now := time.Now()
	if now.Sub(s.lastImage) < stallAfter {
		return Frame{}, nil, false
	}
	if s.probe != nil && now.Before(s.probeAt) {
		return Frame{}, nil, false
	}
	s.probeAt = now.Add(stallProbeEvery)
	if s.probe == nil {
		g, err := s.openGDI()
		if err != nil {
			return Frame{}, nil, false
		}
		// The first capture is the baseline.
		if _, err := g.Next(ctx, 0); err != nil {
			g.Close()
			return Frame{}, nil, false
		}
		s.probe = g
		return Frame{}, nil, false
	}
	g, err := s.probe.Next(ctx, 0)
	if err != nil {
		if !errors.Is(err, ErrNoNewFrame) {
			s.dropProbe()
		}
		return Frame{}, nil, false
	}
	df, err := s.dd.Next(ctx, stallConfirmWait)
	if !errors.Is(err, ErrNoNewFrame) {
		if err != nil || df.Dirty {
			s.presented()
		}
		return df, err, true
	}
	s.cur, s.probe, s.stalled = s.probe, nil, true
	s.dd.Close()
	return g, nil, true
}

func (s *stallSource) Device() *d3d.Device { return s.cur.Device() }

func (s *stallSource) Size() (int, int) { return s.cur.Size() }

func (s *stallSource) Close() {
	s.dropProbe()
	s.cur.Close()
	if s.done != nil {
		s.done()
	}
}
