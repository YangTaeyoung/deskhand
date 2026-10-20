package screen

import (
	"sort"
	"sync"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// window gathers the figures of the stats frame of one interval: the
// access units sent with their bytes, encode times and capture times, and
// the frames the encoder had no room for.
type window struct {
	mu        sync.Mutex
	aus       int
	bytes     int
	dropped   int
	encodeMS  []float64
	captureMS []float64
	// submitted maps the capture reading of a frame given to the encoder
	// to the reading when it was given.
	submitted map[uint64]uint64
}

func newWindow() *window { return &window{submitted: map[uint64]uint64{}} }

// submit records that the frame captured at captureUS went to the
// encoder at nowUS.
func (w *window) submit(captureUS, nowUS uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.submitted[captureUS] = nowUS
}

// forget drops the record of a frame the encoder did not take.
func (w *window) forget(captureUS uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.submitted, captureUS)
}

// drop counts a frame the encoder had no room for.
func (w *window) drop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dropped++
}

// sent counts the access unit au: its encode time runs from the frame's
// submission to au.EncodeDoneMonoUS and its capture time from
// au.CaptureMonoUS to the submission.
func (w *window) sent(au wire.AU) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.aus++
	w.bytes += len(au.Data)
	at, ok := w.submitted[au.CaptureMonoUS]
	if !ok {
		return
	}
	delete(w.submitted, au.CaptureMonoUS)
	if au.EncodeDoneMonoUS >= at {
		w.encodeMS = append(w.encodeMS, float64(au.EncodeDoneMonoUS-at)/1000)
	}
	if at >= au.CaptureMonoUS {
		w.captureMS = append(w.captureMS, float64(at-au.CaptureMonoUS)/1000)
	}
}

// take returns the stats of the interval of elapsedUS that ends now and
// starts the next one; records of submissions older than staleUS before
// nowUS are dropped.
func (w *window) take(elapsedUS, nowUS, staleUS uint64) wire.VideoStats {
	w.mu.Lock()
	defer w.mu.Unlock()
	secs := float64(max(elapsedUS, 1)) / 1e6
	s := wire.VideoStats{
		FPS:          round2(float64(w.aus) / secs),
		Bitrate:      int(float64(w.bytes*8) / secs),
		Dropped:      w.dropped,
		EncodeMSP50:  percentile(w.encodeMS, 50),
		EncodeMSP95:  percentile(w.encodeMS, 95),
		CaptureMSP50: percentile(w.captureMS, 50),
	}
	w.aus, w.bytes, w.dropped = 0, 0, 0
	w.encodeMS, w.captureMS = w.encodeMS[:0], w.captureMS[:0]
	for capture, at := range w.submitted {
		if nowUS > at+staleUS {
			delete(w.submitted, capture)
		}
	}
	return s
}

// percentile returns the p-th percentile of v by the nearest rank, to two
// decimals, or 0 for no values.
func percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	i := int(float64(len(s)-1)*p/100 + 0.5)
	return round2(s[i])
}

func round2(x float64) float64 { return float64(int64(x*100+0.5)) / 100 }
