// Package monoclock is the agent's monotonic clock, the mono_us of
// docs/protocol.md: microseconds since the agent process started.
package monoclock

import "time"

// start is the origin of the clock.
var start = time.Now()

// NowUS returns the microseconds from the start of the process to now on
// the monotonic clock.
func NowUS() uint64 { return uint64(time.Since(start) / time.Microsecond) }

// AtUS returns the clock reading of t, a time with a monotonic reading
// taken in this process, or 0 for a time before the start.
func AtUS(t time.Time) uint64 {
	d := t.Sub(start)
	if d < 0 {
		return 0
	}
	return uint64(d / time.Microsecond)
}
