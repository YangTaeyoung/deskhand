package input

import (
	"testing"
	"time"
)

const (
	stateRunning = 2
	stateReady   = 1
	reasonOther  = 7
)

func TestDrainStepReachesDoneWhenIdleInGetMessage(t *testing.T) {
	idle := 0
	var since time.Time
	now := time.Now()
	if a := drainStep(threadWaiting, waitUserRequest, &idle, &since, now, parkG, runG); a != drainWait {
		t.Fatalf("first idle sample: %v", a)
	}
	if a := drainStep(threadWaiting, waitUserRequest, &idle, &since, now, parkG, runG); a != drainDone {
		t.Fatalf("second idle sample: %v", a)
	}
}

func TestDrainStepKeepsTypingForARunningThread(t *testing.T) {
	idle := 0
	var since time.Time
	start := time.Now()
	if a := drainStep(stateRunning, reasonOther, &idle, &since, start, parkG, runG); a != drainWait {
		t.Fatalf("running, first sample: %v", a)
	}
	if a := drainStep(stateRunning, reasonOther, &idle, &since, start.Add(parkG), parkG, runG); a != drainWait {
		t.Fatalf("running before runningGrace: %v", a)
	}
	if a := drainStep(stateRunning, reasonOther, &idle, &since, start.Add(runG), parkG, runG); a != drainProceed {
		t.Fatalf("running at runningGrace: %v", a)
	}
}

func TestDrainStepProceedsForAThreadParkedOutsideGetMessage(t *testing.T) {
	idle := 0
	var since time.Time
	start := time.Now()
	if a := drainStep(threadWaiting, reasonOther, &idle, &since, start, parkG, runG); a != drainWait {
		t.Fatalf("parked, first sample: %v", a)
	}
	if a := drainStep(threadWaiting, reasonOther, &idle, &since, start.Add(parkG), parkG, runG); a != drainProceed {
		t.Fatalf("parked at parkGrace: %v", a)
	}
}

func TestDrainStepResetsWhenAThreadReturnsToTheMessageLoop(t *testing.T) {
	idle := 0
	var since time.Time
	start := time.Now()
	drainStep(threadWaiting, waitUserRequest, &idle, &since, start, parkG, runG)
	if a := drainStep(stateReady, reasonOther, &idle, &since, start.Add(time.Millisecond), parkG, runG); a != drainWait || idle != 0 {
		t.Fatalf("after one idle then running: %v idle %d", a, idle)
	}
	if a := drainStep(threadWaiting, waitUserRequest, &idle, &since, start.Add(2*time.Millisecond), parkG, runG); a != drainWait {
		t.Fatalf("one idle again after a reset: %v", a)
	}
	if a := drainStep(threadWaiting, waitUserRequest, &idle, &since, start.Add(3*time.Millisecond), parkG, runG); a != drainDone {
		t.Fatalf("two idle in a row: %v", a)
	}
}

const (
	parkG = 100 * time.Millisecond
	runG  = 500 * time.Millisecond
)
