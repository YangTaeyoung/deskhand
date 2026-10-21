package display

import (
	"math"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// skipInSessionZero skips a test that needs the desktop of a user session.
func skipInSessionZero(t *testing.T) {
	t.Helper()
	var id uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &id); err != nil {
		t.Fatalf("ProcessIdToSessionId: %v", err)
	}
	if id == 0 {
		t.Skip("Session 0 has no displays; run this test in the user's session through scripts/wintest.sh")
	}
}

func TestDisplayListHasPrimary(t *testing.T) {
	skipInSessionZero(t)
	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("List returned no displays")
	}
	primaries := 0
	for i, d := range list {
		t.Logf("display %+v", d)
		if d.Index != i {
			t.Errorf("display %d has index %d", i, d.Index)
		}
		if d.Width <= 0 || d.Height <= 0 {
			t.Errorf("display %d is %dx%d", i, d.Width, d.Height)
		}
		if d.DPI <= 0 || math.Abs(d.Scale-float64(d.DPI)/96) > 1e-9 {
			t.Errorf("display %d has dpi %d and scale %v", i, d.DPI, d.Scale)
		}
		if !strings.HasPrefix(d.Name, `\\.\`) || d.Adapter == "" {
			t.Errorf("display %d is named %q on adapter %q", i, d.Name, d.Adapter)
		}
		if d.Primary {
			primaries++
			if d.X != 0 || d.Y != 0 {
				t.Errorf("the primary display is at (%d,%d)", d.X, d.Y)
			}
		}
	}
	if primaries != 1 || !list[0].Primary {
		t.Fatalf("%d primary displays, first primary %v", primaries, list[0].Primary)
	}
}

func TestDevModeIsTheSizeOfDEVMODEW(t *testing.T) {
	if n := unsafe.Sizeof(devMode{}); n != 220 {
		t.Fatalf("devMode is %d bytes", n)
	}
	if n := unsafe.Sizeof(monitorInfo{}); n != 40 {
		t.Fatalf("monitorInfo is %d bytes", n)
	}
}
