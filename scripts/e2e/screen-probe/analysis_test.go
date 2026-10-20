package main

import (
	"maps"
	"math"
	"slices"
	"testing"
)

func TestEstimateOffsetTakesTheFastestRoundTrip(t *testing.T) {
	samples := []syncSample{
		{SentUS: 1000, RecvUS: 1900, AgentUS: 6000},
		{SentUS: 2000, RecvUS: 2200, AgentUS: 7150},
		{SentUS: 3000, RecvUS: 3600, AgentUS: 8100},
	}
	got, ok := estimateOffset(samples)
	if !ok {
		t.Fatal("no estimate for three samples")
	}
	if got.OffsetUS != 5050 || got.RTTUS != 200 || got.Samples != 3 {
		t.Fatalf("estimate %+v, expected offset 5050 at RTT 200 from 3 samples", got)
	}
	if got.SpreadUS != 5050-4550 {
		t.Fatalf("spread %v over offsets 4550, 5050, 4800", got.SpreadUS)
	}
	if got.MedianLowUS != 4800 || got.RTTP50US != 600 {
		t.Fatalf("median offset %v, median RTT %v", got.MedianLowUS, got.RTTP50US)
	}
	if _, ok := estimateOffset(nil); ok {
		t.Fatal("an estimate without samples")
	}
}

func TestPercentileIsTheNearestRank(t *testing.T) {
	v := []float64{5, 1, 4, 2, 3, 10, 9, 8, 7, 6}
	for _, c := range []struct{ p, want float64 }{{50, 5}, {95, 10}, {90, 9}, {0, 1}, {100, 10}} {
		if got := percentile(v, c.p); got != c.want {
			t.Errorf("p%v = %v, expected %v", c.p, got, c.want)
		}
	}
	if !math.IsNaN(percentile(nil, 50)) {
		t.Error("the percentile of nothing is a number")
	}
	if s := summarize([]float64{1, 2, 3, 4}); s.N != 4 || s.Min != 1 || s.Max != 4 || s.Mean != 2.5 || s.P50 != 2 || s.P95 != 4 {
		t.Errorf("summary %+v", s)
	}
}

func TestChangeFinderMatchesTheFirstFrameThatDiffersFromTheReference(t *testing.T) {
	base := make([]byte, 100)
	small := make([]byte, 100)
	copy(small, base)
	for i := range 3 {
		small[i] = 200
	}
	big := make([]byte, 100)
	for i := range 40 {
		big[i] = 200
	}
	faint := make([]byte, 100)
	for i := range faint {
		faint[i] = 5
	}
	frames := [][]byte{base, base, small, faint, big, big, base}
	f := newChangeFinder([]int{1, 1, 5, -1}, []int{6, 3, 6, 6}, 10, 20)
	for k, fr := range frames {
		f.frame(k, fr)
	}
	for i, want := range []struct{ index, pixels, below int }{{4, 40, 3}, {-1, 0, 3}, {6, 40, 0}, {-1, 0, 0}} {
		if idx, px, below := f.result(i); idx != want.index || px != want.pixels || below != want.below {
			t.Errorf("event %d matched frame %d with %d pixels (%d below), expected %d with %d (%d below)", i, idx, px, below, want.index, want.pixels, want.below)
		}
	}
	if len(f.kept) != 0 {
		t.Errorf("%d reference crops kept after the last frame", len(f.kept))
	}
}

func TestVideoSummaryMapsTheAgentClockByTheOffset(t *testing.T) {
	v := &videoRecord{OpenedUS: 1000, ClosedUS: 1_001_000, AUs: []auRecord{
		{Seq: 1, Key: true, CaptureUS: 6000, EncodeDoneUS: 6500, RecvUS: 2000, Size: 100},
		{Seq: 2, CaptureUS: 10_000, EncodeDoneUS: 12_000, RecvUS: 7500, Size: 200},
		{Seq: 3, CaptureUS: 20_000, EncodeDoneUS: 20_500, RecvUS: 16_000, Size: 300},
		{Seq: 4, CaptureUS: 30_000, EncodeDoneUS: 34_000, RecvUS: 30_000, Size: 400},
	}}
	const offset = 5000
	for _, c := range []struct {
		name           string
		repeats        map[int]bool
		n              int
		c2rMin, c2rMax float64
		c2eMin, e2rMax float64
		repeated       int
	}{
		{"repeats left out", map[int]bool{2: true}, 2, 2.5, 5.0, 2.0, 1.0, 1},
		{"no repeats", nil, 3, 1.0, 5.0, 0.5, 1.0, 0},
	} {
		r := videoSummary(v, offset, c.repeats)
		if r.CaptureToRecv.N != c.n || r.CaptureToRecv.Min != c.c2rMin || r.CaptureToRecv.Max != c.c2rMax {
			t.Errorf("%s: capture→recv %+v, expected %d values from %v to %v ms", c.name, r.CaptureToRecv, c.n, c.c2rMin, c.c2rMax)
		}
		if r.CaptureToEncoded.Min != c.c2eMin || r.EncodedToRecv.Max != c.e2rMax {
			t.Errorf("%s: capture→encoded min %v, encoded→recv max %v, expected %v and %v ms", c.name, r.CaptureToEncoded.Min, r.EncodedToRecv.Max, c.c2eMin, c.e2rMax)
		}
		if r.Repeats != c.repeated {
			t.Errorf("%s: repeats %d", c.name, r.Repeats)
		}
		if r.FirstAUMS != 1.0 || r.FirstAUBytes != 100 || r.AUs != 4 || r.Keyframes != 1 || r.FPS != 4 || r.BitrateBPS != 8000 {
			t.Errorf("%s: first AU %v ms %d B, %d AUs, %d keys, %v fps, %v bps", c.name, r.FirstAUMS, r.FirstAUBytes, r.AUs, r.Keyframes, r.FPS, r.BitrateBPS)
		}
	}
}

func TestInputTimingsMapTheAgentClockByTheOffset(t *testing.T) {
	aus := []auRecord{
		{Seq: 1, CaptureUS: 43_000, RecvUS: 39_000},
		{Seq: 2, CaptureUS: 47_000, RecvUS: 43_500},
		{Seq: 3, CaptureUS: 60_000, RecvUS: 1_100_000},
	}
	inputs := []inputRecord{
		{Kind: "move", SentUS: 30_000, AckUS: 30_200, DoneUS: 35_100, OK: true},
		{Kind: "click_down_warmup", SentUS: 35_000, AckUS: 35_500, DoneUS: 40_100, OK: true},
		{Kind: "click_down", SentUS: 40_000, AckUS: 40_600, DoneUS: 45_300, OK: true},
		{Kind: "key_down", SentUS: 41_000, AckUS: 41_100, OK: false, Code: "SESSION_LOCKED"},
		{Kind: "click_up", SentUS: 42_000, AckUS: 42_400, DoneUS: 47_200, OK: true},
	}
	events, refs, last := measuredEvents(inputs, aus, measuredKinds, 1000)
	if len(events) != 3 || events[0].Kind != "click_down" || events[1].Kind != "key_down" || events[2].Kind != "click_up" {
		t.Fatalf("measured events %+v", events)
	}
	if want := []int{0, -1, 0}; !slices.Equal(refs, want) {
		t.Errorf("reference AUs %v, expected %v", refs, want)
	}
	if want := []int{1, 1, 1}; !slices.Equal(last, want) {
		t.Errorf("last AUs %v, expected %v", last, want)
	}
	const offset = 5000
	kinds := inputTimings(events, []change{{1, 900, 3}, {-1, 0, 0}, {-1, 0, 7}}, aus, measuredKinds, offset)
	down := kinds["click_down"]
	if down.Events != 1 || down.Matched != 1 || down.MaxBelow != 3 || down.Pixels.Max != 900 {
		t.Errorf("click_down %+v", down)
	}
	for name, c := range map[string]struct{ got, want float64 }{
		"ack round trip":       {down.AckRTT.P50, 0.6},
		"send→done":            {down.SendToDone.P50, 0.3},
		"send→change received": {down.ToChange.P50, 3.5},
		"done→change captured": {down.DoneToCap.P50, 1.7},
	} {
		if math.Abs(c.got-c.want) > 1e-9 {
			t.Errorf("click_down %s %v ms, expected %v", name, c.got, c.want)
		}
	}
	if key := kinds["key_down"]; key.Events != 1 || key.Failed != 1 || key.AckRTT.N != 0 {
		t.Errorf("key_down %+v", key)
	}
	if up := kinds["click_up"]; up.Events != 1 || up.Matched != 0 || up.MaxBelow != 7 || math.Abs(up.SendToDone.P50-0.2) > 1e-9 {
		t.Errorf("click_up %+v", up)
	}
}

func TestKeepaliveRepeatsAreTheAUsTenSecondsAfterTheOneBefore(t *testing.T) {
	aus := []auRecord{
		{Key: true, CaptureUS: 1_000_000},
		{CaptureUS: 1_500_000},
		{CaptureUS: 11_500_000},
		{CaptureUS: 21_550_001},
		{CaptureUS: 31_560_000},
		{CaptureUS: 41_570_000},
		{Key: true, CaptureUS: 51_570_000},
		{CaptureUS: 51_600_000},
	}
	if got, want := keepaliveRepeats(aus), map[int]bool{2: true, 4: true, 5: true}; !maps.Equal(got, want) {
		t.Fatalf("repeats %v, expected %v", got, want)
	}
}

func TestKeyFrameComparison(t *testing.T) {
	if got := meanAbsDiff([]byte{0, 10, 20}, []byte{3, 10, 14}); got != 3 {
		t.Errorf("mean absolute difference %v, expected 3", got)
	}
	if !math.IsNaN(meanAbsDiff(nil, []byte{1})) {
		t.Error("the difference of nothing is a number")
	}
	if mean, lit := brightness([]byte{0, 0, 30, 90}, 24); mean != 30 || lit != 0.5 {
		t.Errorf("brightness %v, %v", mean, lit)
	}
	for _, c := range []struct {
		lit, mad float64
		want     bool
	}{{0.5, 2, true}, {0.5, 8, true}, {0.5, 8.1, false}, {0.005, 1, false}, {0.5, math.NaN(), false}} {
		if got := keyPasses(c.lit, c.mad, 0.01, 8); got != c.want {
			t.Errorf("keyPasses(%v, %v) = %v", c.lit, c.mad, got)
		}
	}
	rec := &record{Fixture: &fixtureInfo{X: 400, Y: 200, W: 1000, H: 600}, Display: &displayInfo{X: 0, Y: 0, Width: 3840, Height: 2160}}
	if got, want := stillCrop(rec, 1920, 1080), [4]int{200, 100, 500, 300}; got != want {
		t.Errorf("crop %v, expected %v", got, want)
	}
	img := []byte{
		1, 1, 1, 2, 2, 2, 3, 3, 3,
		4, 4, 4, 5, 5, 5, 6, 6, 6,
	}
	if got := cropRGB(img, 3, [4]int{1, 1, 2, 1}); !slices.Equal(got, []byte{5, 5, 5, 6, 6, 6}) {
		t.Errorf("crop of the RGB image %v", got)
	}
}
