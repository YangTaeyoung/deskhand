//go:build !windows

package session

// Current returns ErrUnsupported.
func Current() (Info, error) { return Info{}, ErrUnsupported }
