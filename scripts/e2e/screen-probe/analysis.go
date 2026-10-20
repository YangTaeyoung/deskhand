package main

import (
	"math"
	"slices"
)

// syncSample is one time.sync round trip: the probe clock when the request
// was written and when the answer was read, and the answer's mono_us.
type syncSample struct {
	SentUS  int64  `json:"sent_us"`
	RecvUS  int64  `json:"recv_us"`
	AgentUS uint64 `json:"agent_us"`
}

// rtt is the round trip of s in microseconds.
func (s syncSample) rtt() int64 { return s.RecvUS - s.SentUS }

// offset is the agent clock minus the probe clock at the middle of s.
func (s syncSample) offset() float64 {
	return float64(s.AgentUS) - float64(s.SentUS+s.RecvUS)/2
}

// lowRTTSamples is how many of the fastest round trips clockOffset.Spread
// covers.
const lowRTTSamples = 8

// clockOffset maps the agent's mono_us to the probe clock: probe = agent −
// OffsetUS.
type clockOffset struct {
	// OffsetUS is the offset of the round trip with the smallest RTT.
	OffsetUS float64 `json:"offset_us"`
	// RTTUS is that round trip's RTT.
	RTTUS int64 `json:"rtt_us"`
	// MedianLowUS is the median offset of the lowRTTSamples fastest round
	// trips and SpreadUS the range of their offsets.
	MedianLowUS float64 `json:"median_low_us"`
	SpreadUS    float64 `json:"spread_us"`
	// RTTP50US is the median RTT of all round trips.
	RTTP50US float64 `json:"rtt_p50_us"`
	Samples  int     `json:"samples"`
}

// estimateOffset returns the offset of the round trip with the smallest RTT
// among samples, and false for no samples.
func estimateOffset(samples []syncSample) (clockOffset, bool) {
	if len(samples) == 0 {
		return clockOffset{}, false
	}
	byRTT := slices.Clone(samples)
	slices.SortStableFunc(byRTT, func(a, b syncSample) int { return int(a.rtt() - b.rtt()) })
	low := byRTT[:min(lowRTTSamples, len(byRTT))]
	offsets := make([]float64, len(low))
	for i, s := range low {
		offsets[i] = s.offset()
	}
	rtts := make([]float64, len(byRTT))
	for i, s := range byRTT {
		rtts[i] = float64(s.rtt())
	}
	sorted := slices.Sorted(slices.Values(offsets))
	return clockOffset{
		OffsetUS:    byRTT[0].offset(),
		RTTUS:       byRTT[0].rtt(),
		MedianLowUS: percentile(sorted, 50),
		SpreadUS:    sorted[len(sorted)-1] - sorted[0],
		RTTP50US:    percentile(rtts, 50),
		Samples:     len(samples),
	}, true
}

// percentile returns the p-th percentile (0–100) of v by the nearest-rank
// method, or NaN for an empty v. v need not be sorted.
func percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	s := slices.Sorted(slices.Values(v))
	rank := int(math.Ceil(p / 100 * float64(len(s))))
	return s[min(max(rank-1, 0), len(s)-1)]
}

// summary is the distribution of a set of values.
type summary struct {
	N    int     `json:"n"`
	Min  float64 `json:"min"`
	P50  float64 `json:"p50"`
	P95  float64 `json:"p95"`
	Max  float64 `json:"max"`
	Mean float64 `json:"mean"`
}

// summarize returns the summary of v, the zero summary for an empty v.
func summarize(v []float64) summary {
	if len(v) == 0 {
		return summary{}
	}
	sum := 0.0
	for _, x := range v {
		sum += x
	}
	return summary{N: len(v), Min: slices.Min(v), P50: percentile(v, 50), P95: percentile(v, 95), Max: slices.Max(v), Mean: sum / float64(len(v))}
}

// changedPixels counts the positions where a and b differ by at least
// delta; positions past the shorter of them are not counted.
func changedPixels(a, b []byte, delta int) int {
	n := 0
	for i := range min(len(a), len(b)) {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		if d >= delta {
			n++
		}
	}
	return n
}

// changeFinder finds for each event the first frame after the event's
// reference frame, and no later than its last frame, whose crop has at least
// minPixels pixels that differ from the reference crop by at least delta.
// Frames are given in order through frame.
type changeFinder struct {
	refs, last       []int
	found, pixels    []int
	below            []int
	kept             map[int][]byte
	delta, minPixels int
}

// newChangeFinder returns a finder for events whose reference frame indexes
// are refs (−1: none) and whose last frame indexes are last.
func newChangeFinder(refs, last []int, delta, minPixels int) *changeFinder {
	f := &changeFinder{refs: refs, last: last, found: make([]int, len(refs)), pixels: make([]int, len(refs)),
		below: make([]int, len(refs)), kept: map[int][]byte{}, delta: delta, minPixels: minPixels}
	for i := range f.found {
		f.found[i] = -1
	}
	return f
}

// frame takes the crop of frame k.
func (f *changeFinder) frame(k int, crop []byte) {
	for _, r := range f.refs {
		if r == k && f.kept[k] == nil {
			f.kept[k] = slices.Clone(crop)
		}
	}
	for i, r := range f.refs {
		if r < 0 || f.found[i] >= 0 || k <= r || k > f.last[i] {
			continue
		}
		ref := f.kept[r]
		if ref == nil {
			continue
		}
		n := changedPixels(ref, crop, f.delta)
		if n >= f.minPixels {
			f.found[i], f.pixels[i] = k, n
		} else {
			f.below[i] = max(f.below[i], n)
		}
	}
	for r := range f.kept {
		if !f.needs(r, k) {
			delete(f.kept, r)
		}
	}
}

// needs reports whether an unresolved event with reference frame r can
// still match a frame after k.
func (f *changeFinder) needs(r, k int) bool {
	for i, ref := range f.refs {
		if ref == r && f.found[i] < 0 && f.last[i] > k {
			return true
		}
	}
	return false
}

// result returns the matched frame index of event i (−1: none), its count
// of changed pixels and the largest count of the frames before it that
// stayed below minPixels.
func (f *changeFinder) result(i int) (index, pixels, below int) {
	return f.found[i], f.pixels[i], f.below[i]
}
