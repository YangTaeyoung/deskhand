package conn

import (
	"context"
	"math/rand/v2"
	"time"
)

// BackoffConfig sets the waits between reconnect attempts: Initial doubles
// after every failed attempt up to Max, and each wait varies by up to
// ±Jitter of itself without going above Max. Zero fields take the defaults
// 0.5 s, 10 s and 0.2; a negative Jitter turns jitter off.
type BackoffConfig struct {
	Initial time.Duration
	Max     time.Duration
	Jitter  float64
}

const (
	defaultBackoffInitial = 500 * time.Millisecond
	defaultBackoffMax     = 10 * time.Second
	defaultBackoffJitter  = 0.2
)

func (c BackoffConfig) withDefaults() BackoffConfig {
	if c.Initial <= 0 {
		c.Initial = defaultBackoffInitial
	}
	if c.Max <= 0 {
		c.Max = defaultBackoffMax
	}
	switch {
	case c.Jitter == 0:
		c.Jitter = defaultBackoffJitter
	case c.Jitter < 0:
		c.Jitter = 0
	}
	return c
}

// backoff returns the successive waits of a BackoffConfig.
type backoff struct {
	cfg  BackoffConfig
	next time.Duration
}

func newBackoff(cfg BackoffConfig) *backoff {
	cfg = cfg.withDefaults()
	return &backoff{cfg: cfg, next: cfg.Initial}
}

// Next returns the next wait.
func (b *backoff) Next() time.Duration {
	base := b.next
	b.next = min(2*b.next, b.cfg.Max)
	d := base
	if b.cfg.Jitter > 0 {
		d = time.Duration(float64(base) * (1 + b.cfg.Jitter*(2*rand.Float64()-1)))
	}
	return min(d, b.cfg.Max)
}

// Reset starts the waits again from Initial.
func (b *backoff) Reset() { b.next = b.cfg.Initial }

// sleepCtx waits d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
