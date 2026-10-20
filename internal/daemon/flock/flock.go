// Package flock takes exclusive advisory locks on files.
package flock

import (
	"context"
	"errors"
	"time"
)

// ErrLocked is returned by TryLock when another holder has the lock.
var ErrLocked = errors.New("the file is locked by another process")

// pollInterval is the time between two attempts of Acquire.
const pollInterval = 20 * time.Millisecond

// Acquire waits until it holds the lock on path or ctx ends.
func Acquire(ctx context.Context, path string) (*Lock, error) {
	for {
		l, err := TryLock(path)
		if !errors.Is(err, ErrLocked) {
			return l, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
