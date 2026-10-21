package screen

import (
	"context"
	"math"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/daemon/apierr"
	"github.com/YangTaeyoung/deskhand/internal/daemon/conn"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

const (
	// syncSamples is how many time.sync round trips a connection's clock
	// offset is measured from.
	syncSamples = 5
	// syncTimeout bounds one time.sync call.
	syncTimeout = 3 * time.Second
)

// hostClock is the offset of a host agent's monotonic clock from the
// daemon's on one connection: agent µs = daemon µs + offsetUS.
type hostClock struct {
	connGen   uint64
	offsetUS  int64
	ok        bool
	measuring bool
}

// offset returns the clock offset of hostID's agent on connection connGen,
// once it has been measured.
func (h *Hub) offset(hostID string, connGen uint64) (int64, bool) {
	h.clockMu.Lock()
	defer h.clockMu.Unlock()
	c := h.clocks[hostID]
	if c == nil || c.connGen != connGen || !c.ok {
		return 0, false
	}
	return c.offsetUS, true
}

// measureClock starts measuring the clock offset of hostID's agent a on
// connection connGen unless it is measured or being measured.
func (h *Hub) measureClock(hostID string, a Agent, connGen uint64) {
	h.clockMu.Lock()
	c := h.clocks[hostID]
	if c != nil && c.connGen == connGen && (c.ok || c.measuring) {
		h.clockMu.Unlock()
		return
	}
	h.clocks[hostID] = &hostClock{connGen: connGen, measuring: true}
	h.clockMu.Unlock()
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		off, ok := h.sampleClock(a, connGen)
		h.clockMu.Lock()
		defer h.clockMu.Unlock()
		if c := h.clocks[hostID]; c != nil && c.connGen == connGen {
			c.offsetUS, c.ok, c.measuring = off, ok, false
		}
		if !ok {
			h.lg.Printf("screen: %s: the agent's clock could not be read with time.sync; no latency notices on this connection", hostID)
		}
	}()
}

// sampleClock calls time.sync syncSamples times on connection connGen of a
// and returns the offset of the round trip that took the least time: the
// agent's reading minus the daemon's clock halfway through the call.
func (h *Hub) sampleClock(a Agent, connGen uint64) (int64, bool) {
	bestRTT := int64(math.MaxInt64)
	var best int64
	found := false
	for range syncSamples {
		if h.base.Err() != nil || a.Generation() != connGen {
			return 0, false
		}
		ctx, cancel := context.WithTimeout(h.base, syncTimeout)
		t0 := h.clock()
		res, err := a.Call(ctx, "time.sync", nil, conn.CallOpts{})
		t1 := h.clock()
		cancel()
		if apierr.CodeOf(err) == wire.CodeCapabilityUnsupported {
			return 0, false
		}
		if err != nil {
			continue
		}
		mono, ok := res["mono_us"].(float64)
		if !ok || mono < 0 {
			continue
		}
		if rtt := t1 - t0; rtt < bestRTT {
			bestRTT, best, found = rtt, int64(mono)-(t0+t1)/2, true
		}
	}
	if a.Generation() != connGen {
		return 0, false
	}
	return best, found
}
