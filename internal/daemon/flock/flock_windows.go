//go:build windows

package flock

import "errors"

// Lock is a held lock.
type Lock struct{}

// TryLock reports that the daemon's locks exist only on macOS.
func TryLock(string) (*Lock, error) {
	return nil, errors.New("Deskhand 데몬은 macOS에서 실행됩니다. Mac에서 deskhand를 실행하세요")
}

// SetContent does nothing.
func (l *Lock) SetContent([]byte) error { return nil }

// Release gives the lock up.
func (l *Lock) Release() error { return nil }
