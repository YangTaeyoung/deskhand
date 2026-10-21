//go:build !windows

package capture

// Open returns ErrUnsupported.
func Open(int) (Source, error) { return nil, ErrUnsupported }

// CaptureStill returns ErrUnsupported.
func CaptureStill(int, string, int, int) (StillResult, error) { return StillResult{}, ErrUnsupported }

// Probe returns ErrUnsupported.
func Probe() error { return ErrUnsupported }
