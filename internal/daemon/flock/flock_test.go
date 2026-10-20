//go:build unix

package flock

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestTryLockExcludesSecondHolder(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	a, err := TryLock(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(p); !errors.Is(err, ErrLocked) {
		t.Fatalf("second TryLock: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := Acquire(ctx, p); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Acquire while held: %v", err)
	}
	if err := a.Release(); err != nil {
		t.Fatal(err)
	}
	b, err := Acquire(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	b.Release()
}
