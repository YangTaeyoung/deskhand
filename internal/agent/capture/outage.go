package capture

import (
	"context"
	"fmt"
	"time"

	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
)

// accessLostGrace is how long an access_lost outage, or one without a
// reason, lasts before Next reports it.
const accessLostGrace = 2 * time.Second

// Retry intervals of an outage: the first retry comes after firstRetry and
// each next one after twice the interval before, up to maxRetry.
const (
	firstRetry = 50 * time.Millisecond
	maxRetry   = time.Second
)

// outage is a failure to capture from its first occurrence on, with the
// schedule of the retries.
type outage struct {
	since, nextTry time.Time
	backoff        time.Duration
	hr             com.HRESULT
	u              *Unavailable
}

func (o *outage) active() bool { return !o.since.IsZero() }

// begin starts the outage at now with a retry due at once, unless it has
// started already.
func (o *outage) begin(now time.Time) {
	if !o.active() {
		*o = outage{since: now, nextTry: now}
	}
}

// failed records a failed retry with its result and reason and schedules
// the next one.
func (o *outage) failed(now time.Time, hr com.HRESULT, u *Unavailable) {
	o.hr, o.u = hr, u
	o.backoff = min(max(2*o.backoff, firstRetry), maxRetry)
	o.nextTry = now.Add(o.backoff)
}

// err is the error the outage reports at now: its *Unavailable at once for
// a reason other than access_lost; after accessLostGrace the access_lost
// *Unavailable, or an error with the last failure when no reason was
// found; nil before.
func (o *outage) err(now time.Time, what string) error {
	switch {
	case o.u != nil && o.u.Reason != ReasonAccessLost:
		return o.u
	case now.Sub(o.since) < accessLostGrace:
		return nil
	case o.u != nil:
		return o.u
	}
	return fmt.Errorf("%s fails for %v (%v); open the capture again", what, now.Sub(o.since).Round(time.Millisecond), o.hr)
}

// sleepUntil waits until t or until ctx ends.
func sleepUntil(ctx context.Context, t time.Time) error {
	d := time.Until(t)
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
