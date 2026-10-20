//go:build windows

package d3d

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
)

var (
	dxgiDLL  = windows.NewLazySystemDLL("dxgi.dll")
	d3d11DLL = windows.NewLazySystemDLL("d3d11.dll")

	procCreateDXGIFactory1 = dxgiDLL.NewProc("CreateDXGIFactory1")
	procD3D11CreateDevice  = d3d11DLL.NewProc("D3D11CreateDevice")
)

var (
	IID_IDXGIFactory1     = com.MustGUID("770aae78-f26f-4dba-a829-253c83d1b387")
	IID_IDXGIOutput1      = com.MustGUID("00cddea8-939b-4b83-a340-a685226666cc")
	IID_ID3D11Texture2D   = com.MustGUID("6f15aaf2-d208-4e89-9ab4-489535d34f9c")
	IID_ID3D10Multithread = com.MustGUID("9b7e4e00-342c-4106-a19f-4f2704f689f0")
)

// vtable indices (docs/adr/0001-screen-pipeline.md).
const (
	factory1EnumAdapters1 = 12
	adapterEnumOutputs    = 7
	adapter1GetDesc1      = 10
	outputGetDesc         = 7
	multithreadSetProtect = 5
)

// D3D11CreateDevice arguments.
const (
	driverTypeUnknown  = 0
	createBGRASupport  = 0x20
	createVideoSupport = 0x800
	sdkVersion         = 7
)

// featureLevels are the levels D3D11CreateDevice may choose from: 11_1,
// 11_0, 10_1 and 10_0.
var featureLevels = [...]uint32{0xb100, 0xb000, 0xa100, 0xa000}

// adapterDesc1 is DXGI_ADAPTER_DESC1.
type adapterDesc1 struct {
	Description                                                     [128]uint16
	VendorID, DeviceID, SubSysID, Revision                          uint32
	DedicatedVideoMemory, DedicatedSystemMemory, SharedSystemMemory uintptr
	LUIDLow                                                         uint32
	LUIDHigh                                                        int32
	Flags                                                           uint32
}

// outputDesc is DXGI_OUTPUT_DESC.
type outputDesc struct {
	DeviceName               [32]uint16
	Left, Top, Right, Bottom int32
	AttachedToDesktop        int32
	Rotation                 uint32
	Monitor                  uintptr
}

// Output is one DXGI output of an adapter.
type Output struct {
	// Name is the GDI device name, such as \\.\DISPLAY1.
	Name string
	// Adapter is the adapter's description, such as "NVIDIA GeForce RTX
	// 2070", and AdapterLUID its locally unique id.
	Adapter     string
	AdapterLUID uint64
	// Left, Top, Right and Bottom bound the output on the virtual desktop,
	// in physical pixels when the process is per-monitor DPI aware
	// (display.EnsureDPIAware) and in DPI-scaled pixels otherwise.
	Left, Top, Right, Bottom int
	// Attached reports whether the output is part of the desktop.
	Attached bool
	// Rotation is DXGI_MODE_ROTATION: 1 identity, 2 90°, 3 180°, 4 270°.
	Rotation int
	// Monitor is the output's HMONITOR.
	Monitor uintptr
}

// eachOutput calls fn with every output of every adapter, in DXGI order;
// adapter and output stay referenced only during fn.
func eachOutput(fn func(adapter, output uintptr, a *adapterDesc1, o *outputDesc) (stop bool)) error {
	var factory uintptr
	r, _, _ := procCreateDXGIFactory1.Call(uintptr(unsafe.Pointer(&IID_IDXGIFactory1)), uintptr(unsafe.Pointer(&factory)))
	if hr := com.HRESULT(uint32(r)); hr.Failed() {
		return fmt.Errorf("CreateDXGIFactory1: %w", hr)
	}
	defer com.Release(factory)
	for i := 0; ; i++ {
		var adapter uintptr
		hr := com.Call(factory, factory1EnumAdapters1, uintptr(i), uintptr(unsafe.Pointer(&adapter)))
		if hr == com.DXGI_ERROR_NOT_FOUND {
			return nil
		}
		if hr.Failed() {
			return fmt.Errorf("IDXGIFactory1::EnumAdapters1(%d): %w", i, hr)
		}
		stop, err := eachAdapterOutput(adapter, fn)
		com.Release(adapter)
		if err != nil || stop {
			return err
		}
	}
}

// eachAdapterOutput calls fn with every output of adapter.
func eachAdapterOutput(adapter uintptr, fn func(adapter, output uintptr, a *adapterDesc1, o *outputDesc) bool) (bool, error) {
	var ad adapterDesc1
	if hr := com.Call(adapter, adapter1GetDesc1, uintptr(unsafe.Pointer(&ad))); hr.Failed() {
		return false, fmt.Errorf("IDXGIAdapter1::GetDesc1: %w", hr)
	}
	for j := 0; ; j++ {
		var output uintptr
		hr := com.Call(adapter, adapterEnumOutputs, uintptr(j), uintptr(unsafe.Pointer(&output)))
		if hr == com.DXGI_ERROR_NOT_FOUND {
			return false, nil
		}
		if hr.Failed() {
			return false, fmt.Errorf("IDXGIAdapter::EnumOutputs(%d) of %q: %w", j, windows.UTF16ToString(ad.Description[:]), hr)
		}
		var od outputDesc
		hr = com.Call(output, outputGetDesc, uintptr(unsafe.Pointer(&od)))
		if hr.Failed() {
			com.Release(output)
			return false, fmt.Errorf("IDXGIOutput::GetDesc: %w", hr)
		}
		stop := fn(adapter, output, &ad, &od)
		com.Release(output)
		if stop {
			return true, nil
		}
	}
}

func luid(a *adapterDesc1) uint64 { return uint64(uint32(a.LUIDHigh))<<32 | uint64(a.LUIDLow) }

// Outputs returns the DXGI outputs of all adapters in DXGI order. In a
// session without a desktop, such as Session 0, the error wraps
// com.DXGI_ERROR_NOT_CURRENTLY_AVAILABLE.
func Outputs() ([]Output, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var outs []Output
	err := eachOutput(func(_, _ uintptr, a *adapterDesc1, o *outputDesc) bool {
		outs = append(outs, Output{
			Name: windows.UTF16ToString(o.DeviceName[:]), Adapter: windows.UTF16ToString(a.Description[:]), AdapterLUID: luid(a),
			Left: int(o.Left), Top: int(o.Top), Right: int(o.Right), Bottom: int(o.Bottom),
			Attached: o.AttachedToDesktop != 0, Rotation: int(o.Rotation), Monitor: o.Monitor,
		})
		return false
	})
	if err != nil {
		return nil, err
	}
	return outs, nil
}

// NewDeviceForOutput creates a D3D11 device on the adapter that has the
// output named outputName (such as \\.\DISPLAY1), with BGRA and video
// support (BGRA only when the adapter refuses video support) and
// multithread protection, and keeps the output's IDXGIOutput1. Call it on
// a goroutine locked to its thread and Close the device there.
func NewDeviceForOutput(outputName string) (*Device, error) {
	var dev *Device
	var createErr error
	err := eachOutput(func(adapter, output uintptr, a *adapterDesc1, o *outputDesc) bool {
		if windows.UTF16ToString(o.DeviceName[:]) != outputName {
			return false
		}
		dev, createErr = newDevice(adapter, output)
		if dev != nil {
			dev.OutputName, dev.AdapterName, dev.AdapterLUID, dev.VendorID = outputName, windows.UTF16ToString(a.Description[:]), luid(a), a.VendorID
		}
		return true
	})
	switch {
	case err != nil:
		return nil, err
	case createErr != nil:
		return nil, createErr
	case dev == nil:
		return nil, fmt.Errorf("no DXGI output is named %q; list the displays again", outputName)
	}
	return dev, nil
}

// newDevice creates the device on adapter and takes output's IDXGIOutput1.
func newDevice(adapter, output uintptr) (*Device, error) {
	d := &Device{Video: true}
	hr := createDevice(adapter, createBGRASupport|createVideoSupport, d)
	if hr.Failed() {
		d.Video = false
		if hr2 := createDevice(adapter, createBGRASupport, d); hr2.Failed() {
			return nil, fmt.Errorf("D3D11CreateDevice: %w", hr2)
		}
	}
	mt, hr := com.QueryInterface(d.Device, &IID_ID3D10Multithread)
	if hr.Failed() {
		d.Close()
		return nil, fmt.Errorf("ID3D11Device QueryInterface(ID3D10Multithread): %w", hr)
	}
	com.CallRaw(mt, multithreadSetProtect, 1)
	com.Release(mt)
	o1, hr := com.QueryInterface(output, &IID_IDXGIOutput1)
	if hr.Failed() {
		d.Close()
		return nil, fmt.Errorf("IDXGIOutput QueryInterface(IDXGIOutput1): %w", hr)
	}
	d.Output = o1
	return d, nil
}

func createDevice(adapter uintptr, flags uintptr, d *Device) com.HRESULT {
	r, _, _ := procD3D11CreateDevice.Call(adapter, driverTypeUnknown, 0, flags,
		uintptr(unsafe.Pointer(&featureLevels[0])), uintptr(len(featureLevels)), sdkVersion,
		uintptr(unsafe.Pointer(&d.Device)), uintptr(unsafe.Pointer(&d.FeatureLevel)), uintptr(unsafe.Pointer(&d.Context)))
	return com.HRESULT(uint32(r))
}

// Close releases the output, the context and the device.
func (d *Device) Close() {
	com.Release(d.Output)
	com.Release(d.Context)
	com.Release(d.Device)
	d.Output, d.Context, d.Device = 0, 0, 0
}
