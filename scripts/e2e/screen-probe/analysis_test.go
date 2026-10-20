package main

import (
	"math"
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
