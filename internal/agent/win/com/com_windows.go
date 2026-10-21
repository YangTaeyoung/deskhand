//go:build windows

package com

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// object is the memory layout of a COM interface pointer's target: a
// pointer to its table of method addresses.
type object struct {
	vtbl *[1 << 12]uintptr
}

// method returns the address of method index of the interface at obj.
func method(obj uintptr, index int) uintptr {
	o := *(**object)(unsafe.Pointer(&obj))
	return o.vtbl[index]
}

// Call calls method index of the interface at obj with obj as its this
// pointer and args after it, and returns the method's result as an HRESULT.
//
//go:uintptrescapes
func Call(obj uintptr, index int, args ...uintptr) HRESULT {
	return HRESULT(uint32(CallRaw(obj, index, args...)))
}

// CallRaw is Call for methods whose result is not an HRESULT, such as
// AddRef, Release and methods returning void; it returns the raw result
// register.
//
//go:uintptrescapes
func CallRaw(obj uintptr, index int, args ...uintptr) uintptr {
	all := make([]uintptr, 0, len(args)+1)
	all = append(all, obj)
	all = append(all, args...)
	r, _, _ := syscall.SyscallN(method(obj, index), all...)
	return r
}

// QueryInterface asks the interface at obj for the interface iid and
// returns it with its own reference.
func QueryInterface(obj uintptr, iid *GUID) (uintptr, HRESULT) {
	var out uintptr
	hr := Call(obj, 0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	if hr.Failed() {
		return 0, hr
	}
	return out, hr
}

// AddRef adds a reference to the interface at obj, which is not 0.
func AddRef(obj uintptr) {
	CallRaw(obj, 1)
}

// Release drops one reference to the interface at obj; it does nothing
// for 0.
func Release(obj uintptr) {
	if obj != 0 {
		CallRaw(obj, 2)
	}
}

var (
	ole32              = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx = ole32.NewProc("CoInitializeEx")
	procCoUninitialize = ole32.NewProc("CoUninitialize")
	procCoTaskMemFree  = ole32.NewProc("CoTaskMemFree")
)

// coinitMultithreaded is COINIT_MULTITHREADED.
const coinitMultithreaded = 0

// InitMTA makes the calling thread a member of the multithreaded
// apartment and returns the function that takes it out again. A thread in
// a single-threaded apartment already stays there, and leave does
// nothing. Call both on one goroutine locked to its thread.
func InitMTA() (leave func(), err error) {
	r, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded)
	switch hr := HRESULT(uint32(r)); {
	case hr == RPC_E_CHANGED_MODE:
		return func() {}, nil
	case hr.Failed():
		return nil, fmt.Errorf("CoInitializeEx: %w", hr)
	}
	return func() { procCoUninitialize.Call() }, nil
}

// TaskMemFree frees memory a COM method allocated for its caller; it does
// nothing for 0.
func TaskMemFree(p uintptr) {
	if p != 0 {
		procCoTaskMemFree.Call(p)
	}
}
