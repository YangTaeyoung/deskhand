package input

import "time"

// KTHREAD_STATE and KWAIT_REASON values of the foreground thread: Waiting
// with WrUserRequest is a thread idle in GetMessage.
const (
	threadWaiting   = 5
	waitUserRequest = 13
)

// drainAction is what a drain does after one sample of the foreground
// thread.
type drainAction int

const (
	// drainWait keeps sampling.
	drainWait drainAction = iota
	// drainDone proceeds when the thread idles in GetMessage.
	drainDone
	// drainProceed proceeds when the thread keeps working or stays parked
	// outside GetMessage, subject to a not-responding check.
	drainProceed
)

// drainStep decides a drain from one sample. A thread idle in GetMessage
// (Waiting, WrUserRequest) seen twice is drained; a thread parked outside
// GetMessage that stays so for parkGrace, and a running thread that keeps
// running for runningGrace, let the drain proceed; otherwise it keeps
// waiting. idle and nonIdleSince carry state between samples.
func drainStep(state, reason uint32, idle *int, nonIdleSince *time.Time, now time.Time, parkGrace, runningGrace time.Duration) drainAction {
	if state == threadWaiting && reason == waitUserRequest {
		*idle++
		*nonIdleSince = time.Time{}
		if *idle >= 2 {
			return drainDone
		}
		return drainWait
	}
	*idle = 0
	if nonIdleSince.IsZero() {
		*nonIdleSince = now
	}
	grace := parkGrace
	if state != threadWaiting {
		grace = runningGrace
	}
	if now.Sub(*nonIdleSince) >= grace {
		return drainProceed
	}
	return drainWait
}
