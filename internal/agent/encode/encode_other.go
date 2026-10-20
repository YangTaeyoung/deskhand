//go:build !windows

package encode

import "github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"

// New returns ErrUnsupported.
func New(*d3d.Device, int, int, int, int, int, int) (Encoder, error) { return nil, ErrUnsupported }

// NewSoftware returns ErrUnsupported.
func NewSoftware(*d3d.Device, int, int, int, int, int, int) (Encoder, error) {
	return nil, ErrUnsupported
}

// Probe returns ErrUnsupported.
func Probe() error { return ErrUnsupported }

// InitThread does nothing.
func InitThread() (func(), error) { return func() {}, nil }
