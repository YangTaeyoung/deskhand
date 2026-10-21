package server

import (
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/agent/monoclock"
)

func TestTimeSyncAnswersTheMonoClockReadingWithoutExec(t *testing.T) {
	_, r, w, _ := startServerS(t, Options{Clipboard: &fakeClipboard{}, HomeDir: t.TempDir()})
	expect(t, r, "hello")
	c := &rpcClient{t: t, r: r, w: w}
	before := monoclock.NowUS()
	res := c.ok("time.sync", nil)
	after := monoclock.NowUS()
	f, ok := res["mono_us"].(float64)
	if !ok {
		t.Fatalf("mono_us is %T %v in %v", res["mono_us"], res["mono_us"], res)
	}
	if mono := uint64(f); mono < before || mono > after {
		t.Fatalf("mono_us %d outside the readings before (%d) and after (%d) the request", mono, before, after)
	}
}
