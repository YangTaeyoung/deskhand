//go:build unix

package flock

import (
	"errors"
	"os"
	"syscall"
)

// Lock is a held lock; each Lock has its own open file, so two Locks of one
// process exclude each other.
type Lock struct{ f *os.File }

// TryLock takes the exclusive lock on path, creating the file with mode
// 0600, or returns ErrLocked without waiting.
func TryLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &Lock{f: f}, nil
}

// SetContent replaces the content of the locked file with b.
func (l *Lock) SetContent(b []byte) error {
	if err := l.f.Truncate(0); err != nil {
		return err
	}
	_, err := l.f.WriteAt(b, 0)
	return err
}

// Release gives the lock up.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	err := l.f.Close()
	l.f = nil
	return err
}
