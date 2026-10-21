//go:build !windows

package capture

import (
	"errors"
	"testing"
)

func TestCaptureIsUnsupportedOffWindows(t *testing.T) {
	if _, err := Open(0); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Open: %v", err)
	}
	if _, err := CaptureStill(0, FormatPNG, 0, 0); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("CaptureStill: %v", err)
	}
	if err := Probe(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Probe: %v", err)
	}
}
