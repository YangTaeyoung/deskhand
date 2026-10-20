//go:build windows

package diag

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procCreateDXGIFactory1 = windows.NewLazySystemDLL("dxgi.dll").NewProc("CreateDXGIFactory1")

var (
	// iidIDXGIFactory1 is IID_IDXGIFactory1.
	iidIDXGIFactory1 = windows.GUID{Data1: 0x770aae78, Data2: 0xf26f, Data3: 0x4dba, Data4: [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87}}
	// iidIDXGIDevice is IID_IDXGIDevice, whose CheckInterfaceSupport answer
	// carries the user-mode driver version.
	iidIDXGIDevice = windows.GUID{Data1: 0x54ec77fa, Data2: 0x1377, Data3: 0x44e6, Data4: [8]byte{0x8c, 0x32, 0x88, 0xfd, 0x5f, 0x44, 0xc8, 0x4c}}
)

// Vtable slots of the DXGI interfaces.
const (
	slotRelease               = 2
	slotEnumAdapters1         = 12
	slotCheckInterfaceSupport = 9
	slotGetDesc1              = 10
)

// dxgiAdapterFlagSoftware is DXGI_ADAPTER_FLAG_SOFTWARE.
const dxgiAdapterFlagSoftware = 2

// comObject is a COM interface pointer.
type comObject struct{ vtbl *[16]uintptr }

// call calls the method in slot with the object and args and returns its
// HRESULT. Pointers converted to uintptr in its arguments stay on the heap
// and alive until it returns.
//
//go:uintptrescapes
func (o *comObject) call(slot int, args ...uintptr) int32 {
	r, _, _ := syscall.SyscallN(o.vtbl[slot], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return int32(r)
}

func (o *comObject) release() { o.call(slotRelease) }

// adapterDesc1 is DXGI_ADAPTER_DESC1.
type adapterDesc1 struct {
	Description           [128]uint16
	VendorID              uint32
	DeviceID              uint32
	SubSysID              uint32
	Revision              uint32
	DedicatedVideoMemory  uintptr
	DedicatedSystemMemory uintptr
	SharedSystemMemory    uintptr
	AdapterLuid           windows.LUID
	Flags                 uint32
}

// readGPUs lists the hardware display adapters DXGI enumerates, until
// EnumAdapters1 fails (DXGI_ERROR_NOT_FOUND after the last), with their
// dedicated video memory and user-mode driver version where known.
func readGPUs() []GPU {
	out := []GPU{}
	if procCreateDXGIFactory1.Find() != nil {
		return out
	}
	var factory *comObject
	if hr, _, _ := procCreateDXGIFactory1.Call(uintptr(unsafe.Pointer(&iidIDXGIFactory1)), uintptr(unsafe.Pointer(&factory))); int32(hr) < 0 || factory == nil {
		return out
	}
	defer factory.release()
	for i := uintptr(0); ; i++ {
		var adapter *comObject
		if hr := factory.call(slotEnumAdapters1, i, uintptr(unsafe.Pointer(&adapter))); hr < 0 || adapter == nil {
			return out
		}
		var desc adapterDesc1
		if adapter.call(slotGetDesc1, uintptr(unsafe.Pointer(&desc))) >= 0 && desc.Flags&dxgiAdapterFlagSoftware == 0 {
			g := GPU{Name: windows.UTF16ToString(desc.Description[:]), MemoryBytes: uint64(desc.DedicatedVideoMemory)}
			var umd int64
			if adapter.call(slotCheckInterfaceSupport, uintptr(unsafe.Pointer(&iidIDXGIDevice)), uintptr(unsafe.Pointer(&umd))) >= 0 && umd != 0 {
				g.DriverVersion = driverVersion(umd)
			}
			out = append(out, g)
		}
		adapter.release()
	}
}
