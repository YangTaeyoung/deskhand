package capture

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
)

func TestOutageReportsAccessLostAfterItsGrace(t *testing.T) {
	t0 := time.Unix(1000, 0)
	var o outage
	o.begin(t0)
	if !o.active() || o.nextTry != t0 {
		t.Fatalf("outage %+v", o)
	}
	lost := &Unavailable{Reason: ReasonAccessLost}
	o.failed(t0, com.E_ACCESSDENIED, lost)
	if err := o.err(t0.Add(accessLostGrace-time.Millisecond), "x"); err != nil {
		t.Fatalf("within the grace: %v", err)
	}
	o.begin(t0.Add(time.Second))
	if o.since != t0 {
		t.Fatalf("begin restarted the outage at %v", o.since)
	}
	var u *Unavailable
	if err := o.err(t0.Add(accessLostGrace), "x"); !errors.As(err, &u) || u != lost {
		t.Fatalf("after the grace: %v", err)
	}
}

func TestOutageReportsAReasonForThePersonAtOnce(t *testing.T) {
	t0 := time.Unix(1000, 0)
	for _, reason := range []string{ReasonSecureDesktop, ReasonSessionLocked, ReasonRDPOutputSuppressed, ReasonSession0, ReasonNoOutput} {
		var o outage
		o.begin(t0)
		o.failed(t0, com.E_ACCESSDENIED, &Unavailable{Reason: reason})
		var u *Unavailable
		if err := o.err(t0, "x"); !errors.As(err, &u) || u.Reason != reason {
			t.Errorf("%s: %v", reason, err)
		}
	}
}

func TestOutageWithoutAReasonBecomesAnErrorAfterTheGrace(t *testing.T) {
	t0 := time.Unix(1000, 0)
	var o outage
	o.begin(t0)
	o.failed(t0, com.DXGI_ERROR_DEVICE_REMOVED, nil)
	if err := o.err(t0.Add(time.Second), "DuplicateOutput"); err != nil {
		t.Fatalf("within the grace: %v", err)
	}
	err := o.err(t0.Add(accessLostGrace), "DuplicateOutput")
	var u *Unavailable
	if err == nil || errors.As(err, &u) || !strings.Contains(err.Error(), "DXGI_ERROR_DEVICE_REMOVED") || !strings.Contains(err.Error(), "open the capture again") {
		t.Fatalf("after the grace: %v", err)
	}
}

func TestOutageRetriesBackOffToOneSecond(t *testing.T) {
	t0 := time.Unix(1000, 0)
	var o outage
	o.begin(t0)
	now := t0
	var gaps []time.Duration
	for range 7 {
		o.failed(now, com.E_ACCESSDENIED, nil)
		gaps = append(gaps, o.nextTry.Sub(now))
		now = o.nextTry
	}
	want := []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond,
		800 * time.Millisecond, time.Second, time.Second}
	for i := range want {
		if gaps[i] != want[i] {
			t.Fatalf("retry gaps %v, want %v", gaps, want)
		}
	}
}

func TestSleepUntilEndsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := sleepUntil(ctx, start.Add(time.Hour)); !errors.Is(err, context.Canceled) || time.Since(start) > time.Second {
		t.Fatalf("sleepUntil: %v after %v", err, time.Since(start))
	}
	if err := sleepUntil(context.Background(), time.Now().Add(5*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
}
