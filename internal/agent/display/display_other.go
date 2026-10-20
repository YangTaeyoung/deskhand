//go:build !windows

package display

// List returns ErrUnsupported.
func List() ([]Info, error) { return nil, ErrUnsupported }

// EnsureDPIAware does nothing on this system.
func EnsureDPIAware() {}
