package capture

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
)

func TestOutageReportsAccessLostAfterItsGrace(t *testing.T) {
	t0 := time.Unix(1000, 0)
	var o outage
	o.begin(t0, com.DXGI_ERROR_ACCESS_LOST, nil)
	if !o.active() || o.nextTry != t0 {
		t.Fatalf("outage %+v", o)
	}
	lost := &Unavailable{Reason: ReasonAccessLost}
	o.failed(t0, com.E_ACCESSDENIED, lost)
	if err := o.err(t0.Add(accessLostGrace-time.Millisecond), "x"); err != nil {
		t.Fatalf("within the grace: %v", err)
	}
	next := o.nextTry
	o.begin(t0.Add(time.Second), com.DXGI_ERROR_ACCESS_LOST, lost)
	if o.since != t0 || o.nextTry != next {
		t.Fatalf("begin restarted the outage: since %v, next try %v", o.since, o.nextTry)
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
		o.begin(t0, com.DXGI_ERROR_ACCESS_LOST, nil)
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
	o.begin(t0, com.DXGI_ERROR_ACCESS_LOST, nil)
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
	o.begin(t0, com.DXGI_ERROR_ACCESS_LOST, nil)
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

func TestOutageOfADuplicationLostAgainBeforeAnImage(t *testing.T) {
	t0 := time.Unix(1000, 0)
	lost := &Unavailable{Reason: ReasonAccessLost, Detail: com.DXGI_ERROR_ACCESS_LOST.Error()}
	var o outage
	o.begin(t0, com.DXGI_ERROR_ACCESS_LOST, lost)
	now := t0
	var gaps []time.Duration
	for now.Sub(t0) < 3*time.Second {
		if !o.due(now) {
			t.Fatalf("no retry due at %v; next at %v", now.Sub(t0), o.nextTry.Sub(t0))
		}
		o.retried(now)
		gaps = append(gaps, o.nextTry.Sub(now))
		lostAt := now.Add(time.Millisecond)
		o.begin(lostAt, com.DXGI_ERROR_ACCESS_LOST, lost)
		if o.due(lostAt) {
			t.Fatalf("a retry is due at once after losing the duplication again at %v", lostAt.Sub(t0))
		}
		err := o.err(lostAt, "x")
		var u *Unavailable
		switch {
		case lostAt.Sub(t0) < accessLostGrace && err != nil:
			t.Fatalf("at %v within the grace: %v", lostAt.Sub(t0), err)
		case lostAt.Sub(t0) >= accessLostGrace && (!errors.As(err, &u) || u.Reason != ReasonAccessLost):
			t.Fatalf("at %v after the grace: %v", lostAt.Sub(t0), err)
		}
		now = o.nextTry
	}
	want := []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond,
		800 * time.Millisecond, time.Second, time.Second}
	if len(gaps) < len(want) {
		t.Fatalf("retry gaps %v", gaps)
	}
	for i := range want {
		if gaps[i] != want[i] {
			t.Fatalf("retry gaps %v, want %v first", gaps, want)
		}
	}
	if o.since != t0 {
		t.Fatalf("the outage restarted at %v", o.since)
	}
}

func TestAsUnavailableGivesEveryOutageAFixedReason(t *testing.T) {
	locked := &Unavailable{Reason: ReasonSessionLocked}
	for _, c := range []struct {
		err  error
		want string
	}{
		{nil, ""},
		{locked, ReasonSessionLocked},
		{fmt.Errorf("x: %w", locked), ReasonSessionLocked},
		{&noDisplay{}, ReasonNoOutput},
		{errors.New("IDXGIOutput1::DuplicateOutput fails for 2s (DXGI_ERROR_DEVICE_REMOVED)"), ReasonAccessLost},
	} {
		u := asUnavailable(c.err)
		switch {
		case c.want == "" && u != nil, c.want != "" && (u == nil || u.Reason != c.want):
			t.Errorf("%v: %+v, want %q", c.err, u, c.want)
		case c.want == ReasonAccessLost && u.Detail != c.err.Error():
			t.Errorf("%v: detail %q", c.err, u.Detail)
		}
	}
}

// noDisplay is an error that is ErrNoDisplay.
type noDisplay struct{}

func (*noDisplay) Error() string        { return "display 3 does not exist" }
func (*noDisplay) Is(target error) bool { return target == ErrNoDisplay }

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
