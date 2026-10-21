package monoclock

import (
	"testing"
	"time"
)

func TestNowUSAdvancesWithTheMonotonicClock(t *testing.T) {
	a := NowUS()
	time.Sleep(2 * time.Millisecond)
	b := NowUS()
	if b < a+2000 {
		t.Fatalf("NowUS went from %d to %d over 2 ms", a, b)
	}
	now := time.Now()
	if got := AtUS(now); got < b || got > NowUS() {
		t.Fatalf("AtUS(now) %d outside [%d, now]", got, b)
	}
	if got := AtUS(start.Add(-time.Second)); got != 0 {
		t.Fatalf("AtUS before the start is %d", got)
	}
}
