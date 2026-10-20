//go:build windows

package display

import (
	"fmt"
	"sort"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/YangTaeyoung/deskhand/internal/agent/win/d3d"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	shcore = windows.NewLazySystemDLL("shcore.dll")

	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procGetMonitorInfoW               = user32.NewProc("GetMonitorInfoW")
	procEnumDisplaySettingsW          = user32.NewProc("EnumDisplaySettingsW")
	procGetDpiForMonitor              = shcore.NewProc("GetDpiForMonitor")
)

const (
	// dpiAwarenessPerMonitorV2 is DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2.
	dpiAwarenessPerMonitorV2 = ^uintptr(3)
	// mdtEffectiveDPI is MDT_EFFECTIVE_DPI.
	mdtEffectiveDPI = 0
	// monitorInfoPrimary is MONITORINFOF_PRIMARY.
	monitorInfoPrimary = 1
	// enumCurrentSettings is ENUM_CURRENT_SETTINGS.
	enumCurrentSettings = ^uintptr(0)
	baseDPI             = 96
)

// monitorInfo is MONITORINFO.
type monitorInfo struct {
	Size          uint32
	Monitor, Work [4]int32
	Flags         uint32
}

// devMode is DEVMODEW, 220 bytes, with the display fields of its unions.
type devMode struct {
	DeviceName                                        [32]uint16
	SpecVersion, DriverVersion, Size, DriverExtra     uint16
	Fields                                            uint32
	PositionX, PositionY                              int32
	DisplayOrientation, DisplayFixedOutput            uint32
	Color, Duplex, YResolution, TTOption, Collate     int16
	FormName                                          [32]uint16
	LogPixels                                         uint16
	BitsPerPel, PelsWidth, PelsHeight                 uint32
	DisplayFlags, DisplayFrequency                    uint32
	ICMMethod, ICMIntent, MediaType, DitherType       uint32
	Reserved1, Reserved2, PanningWidth, PanningHeight uint32
}

var dpiAware sync.Once

// EnsureDPIAware makes the process per-monitor DPI aware (v2) once, so that
// positions and sizes are physical pixels and the DPI of each monitor is
// its own. It does nothing when the process already has an awareness.
func EnsureDPIAware() {
	dpiAware.Do(func() {
		if procSetProcessDpiAwarenessContext.Find() == nil {
			_, _, _ = procSetProcessDpiAwarenessContext.Call(dpiAwarenessPerMonitorV2)
		}
	})
}

// List returns the displays attached to the desktop of the agent's
// session. In Session 0 the error wraps
// com.DXGI_ERROR_NOT_CURRENTLY_AVAILABLE.
func List() ([]Info, error) {
	EnsureDPIAware()
	outs, err := d3d.Outputs()
	if err != nil {
		return nil, fmt.Errorf("the displays of this session cannot be read: %w", err)
	}
	list := []Info{}
	for _, o := range outs {
		if !o.Attached {
			continue
		}
		d := Info{Name: o.Name, Adapter: o.Adapter, X: o.Left, Y: o.Top, Width: o.Right - o.Left, Height: o.Bottom - o.Top,
			DPI: monitorDPI(o.Monitor), RefreshHz: refreshHz(o.Name)}
		d.Scale = float64(d.DPI) / baseDPI
		d.Primary = isPrimary(o.Monitor, d.X, d.Y)
		list = append(list, d)
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Primary != b.Primary {
			return a.Primary
		}
		if a.X != b.X {
			return a.X < b.X
		}
		return a.Y < b.Y
	})
	for i := range list {
		list[i].Index = i
	}
	return list, nil
}

// monitorDPI returns the effective DPI of monitor, or 96 when it cannot be
// read.
func monitorDPI(monitor uintptr) int {
	if procGetDpiForMonitor.Find() != nil {
		return baseDPI
	}
	var x, y uint32
	if r, _, _ := procGetDpiForMonitor.Call(monitor, mdtEffectiveDPI, uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&y))); int32(r) < 0 || x == 0 {
		return baseDPI
	}
	return int(x)
}

// isPrimary reports whether monitor is the primary monitor; without its
// monitor information, whether the display is at the origin.
func isPrimary(monitor uintptr, x, y int) bool {
	mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	if r, _, _ := procGetMonitorInfoW.Call(monitor, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return x == 0 && y == 0
	}
	return mi.Flags&monitorInfoPrimary != 0
}

// refreshHz returns the refresh rate of the display device name, or 0 when
// it is unknown or the hardware default.
func refreshHz(name string) int {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0
	}
	dm := devMode{Size: uint16(unsafe.Sizeof(devMode{}))}
	if r, _, _ := procEnumDisplaySettingsW.Call(uintptr(unsafe.Pointer(p)), enumCurrentSettings, uintptr(unsafe.Pointer(&dm))); r == 0 || dm.DisplayFrequency <= 1 {
		return 0
	}
	return int(dm.DisplayFrequency)
}
