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

// repeatFinder marks the frames whose image equals the frame before it: no
// pixel differs from it by delta or more. Frames are given in order
// through frame.
type repeatFinder struct {
	prev    []byte
	delta   int
	repeats map[int]bool
}

func newRepeatFinder(delta int) *repeatFinder {
	return &repeatFinder{delta: delta, repeats: map[int]bool{}}
}

// frame takes the image of frame k.
func (r *repeatFinder) frame(k int, img []byte) {
	if r.prev != nil && len(r.prev) == len(img) && changedPixels(r.prev, img, r.delta) == 0 {
		r.repeats[k] = true
	}
	r.prev = append(r.prev[:0], img...)
}

// measuredEvents returns the inputs of kinds with, for each, the index of
// the last access unit received before it was sent (−1 for none, and for
// an input that failed) and of the last one received within windowMS after
// it was sent.
func measuredEvents(inputs []inputRecord, aus []auRecord, kinds []string, windowMS int) (events []inputRecord, refs, last []int) {
	for _, in := range inputs {
		if !slices.Contains(kinds, in.Kind) {
			continue
		}
		ref, end := -1, -1
		for k, au := range aus {
			if au.RecvUS < in.SentUS {
				ref = k
			}
			if au.RecvUS <= in.SentUS+int64(windowMS)*1000 {
				end = k
			}
		}
		if !in.OK {
			ref = -1
		}
		events = append(events, in)
		refs = append(refs, ref)
		last = append(last, end)
	}
	return events, refs, last
}

// change is the access unit an input's screen change was found in: index
// −1 for none, its count of changed pixels and the largest count of the
// frames before it that stayed below the threshold.
type change struct{ index, pixels, below int }

// inputTimings summarizes events of kinds in milliseconds, with the agent
// clock mapped to the probe clock by offset (probe = agent − offset):
// ack round trip and send→change received on the probe clock, send→done
// across the clocks, done→change captured on the agent clock.
func inputTimings(events []inputRecord, changes []change, aus []auRecord, kinds []string, offset float64) map[string]*inputKinds {
	out := map[string]*inputKinds{}
	acc := map[string]*struct{ ack, done, change, cap, pixels []float64 }{}
	for _, k := range kinds {
		out[k] = &inputKinds{}
		acc[k] = &struct{ ack, done, change, cap, pixels []float64 }{}
	}
	for i, in := range events {
		kind, a := out[in.Kind], acc[in.Kind]
		if kind == nil {
			continue
		}
		kind.Events++
		if !in.OK {
			kind.Failed++
			continue
		}
		a.ack = append(a.ack, ms(float64(in.AckUS-in.SentUS)))
		a.done = append(a.done, ms(float64(in.DoneUS)-offset-float64(in.SentUS)))
		c := changes[i]
		kind.MaxBelow = max(kind.MaxBelow, c.below)
		if c.index < 0 {
			continue
		}
		kind.Matched++
		au := aus[c.index]
		a.change = append(a.change, ms(float64(au.RecvUS-in.SentUS)))
		a.cap = append(a.cap, ms(float64(au.CaptureUS)-float64(in.DoneUS)))
		a.pixels = append(a.pixels, float64(c.pixels))
	}
	for _, k := range kinds {
		kind, a := out[k], acc[k]
		kind.AckRTT, kind.SendToDone, kind.ToChange, kind.DoneToCap, kind.Pixels =
			summarize(a.ack), summarize(a.done), summarize(a.change), summarize(a.cap), summarize(a.pixels)
	}
	return out
}

// meanAbsDiff returns the mean absolute difference of a and b over their
// common length, NaN when it is 0.
func meanAbsDiff(a, b []byte) float64 {
	n := min(len(a), len(b))
	if n == 0 {
		return math.NaN()
	}
	sum := 0
	for i := range n {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		sum += d
	}
	return float64(sum) / float64(n)
}

// brightness returns the mean of img and the share of its values above
// level, NaN and 0 for an empty img.
func brightness(img []byte, level byte) (mean, above float64) {
	if len(img) == 0 {
		return math.NaN(), 0
	}
	sum, n := 0, 0
	for _, v := range img {
		sum += int(v)
		if v > level {
			n++
		}
	}
	return float64(sum) / float64(len(img)), float64(n) / float64(len(img))
}
