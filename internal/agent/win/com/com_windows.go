//go:build windows

package com

import (
	"syscall"
	"unsafe"
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
