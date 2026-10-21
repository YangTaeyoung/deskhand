package com

import "fmt"

// HRESULT is the result of a COM method. Its Error names the known codes.
type HRESULT uint32

// HRESULT values of COM, DXGI and Media Foundation, named as in the Windows
// SDK.
const (
	S_OK                                HRESULT = 0x00000000
	S_FALSE                             HRESULT = 0x00000001
	E_NOTIMPL                           HRESULT = 0x80004001
	E_NOINTERFACE                       HRESULT = 0x80004002
	E_POINTER                           HRESULT = 0x80004003
	E_FAIL                              HRESULT = 0x80004005
	E_UNEXPECTED                        HRESULT = 0x8000FFFF
	E_ACCESSDENIED                      HRESULT = 0x80070005
	E_OUTOFMEMORY                       HRESULT = 0x8007000E
	E_INVALIDARG                        HRESULT = 0x80070057
	DXGI_ERROR_INVALID_CALL             HRESULT = 0x887A0001
	DXGI_ERROR_NOT_FOUND                HRESULT = 0x887A0002
	DXGI_ERROR_UNSUPPORTED              HRESULT = 0x887A0004
	DXGI_ERROR_DEVICE_REMOVED           HRESULT = 0x887A0005
	DXGI_ERROR_DEVICE_HUNG              HRESULT = 0x887A0006
	DXGI_ERROR_DEVICE_RESET             HRESULT = 0x887A0007
	DXGI_ERROR_NOT_CURRENTLY_AVAILABLE  HRESULT = 0x887A0022
	DXGI_ERROR_ACCESS_LOST              HRESULT = 0x887A0026
	DXGI_ERROR_WAIT_TIMEOUT             HRESULT = 0x887A0027
	DXGI_ERROR_SESSION_DISCONNECTED     HRESULT = 0x887A0028
	DXGI_ERROR_ACCESS_DENIED            HRESULT = 0x887A002B
	MF_E_INVALIDREQUEST                 HRESULT = 0xC00D36B2
	MF_E_INVALIDMEDIATYPE               HRESULT = 0xC00D36B4
	MF_E_NOTACCEPTING                   HRESULT = 0xC00D36B5
	MF_E_ATTRIBUTENOTFOUND              HRESULT = 0xC00D36E6
	MF_E_TRANSFORM_TYPE_NOT_SET         HRESULT = 0xC00D6D60
	MF_E_TRANSFORM_STREAM_CHANGE        HRESULT = 0xC00D6D61
	MF_E_TRANSFORM_NEED_MORE_INPUT      HRESULT = 0xC00D6D72
	MF_E_UNSUPPORTED_D3D_TYPE           HRESULT = 0xC00D6D76
	MF_E_TRANSFORM_ASYNC_LOCKED         HRESULT = 0xC00D6D77
	RPC_E_CHANGED_MODE                  HRESULT = 0x80010106
	DXGI_STATUS_OCCLUDED                HRESULT = 0x087A0001
	DXGI_STATUS_MODE_CHANGED            HRESULT = 0x087A0007
	DXGI_STATUS_MODE_CHANGE_IN_PROGRESS HRESULT = 0x087A0008
)

var names = map[HRESULT]string{
	S_OK:                                "S_OK",
	S_FALSE:                             "S_FALSE",
	E_NOTIMPL:                           "E_NOTIMPL",
	E_NOINTERFACE:                       "E_NOINTERFACE",
	E_POINTER:                           "E_POINTER",
	E_FAIL:                              "E_FAIL",
	E_UNEXPECTED:                        "E_UNEXPECTED",
	E_ACCESSDENIED:                      "E_ACCESSDENIED",
	E_OUTOFMEMORY:                       "E_OUTOFMEMORY",
	E_INVALIDARG:                        "E_INVALIDARG",
	DXGI_ERROR_INVALID_CALL:             "DXGI_ERROR_INVALID_CALL",
	DXGI_ERROR_NOT_FOUND:                "DXGI_ERROR_NOT_FOUND",
	DXGI_ERROR_UNSUPPORTED:              "DXGI_ERROR_UNSUPPORTED",
	DXGI_ERROR_DEVICE_REMOVED:           "DXGI_ERROR_DEVICE_REMOVED",
	DXGI_ERROR_DEVICE_HUNG:              "DXGI_ERROR_DEVICE_HUNG",
	DXGI_ERROR_DEVICE_RESET:             "DXGI_ERROR_DEVICE_RESET",
	DXGI_ERROR_NOT_CURRENTLY_AVAILABLE:  "DXGI_ERROR_NOT_CURRENTLY_AVAILABLE",
	DXGI_ERROR_ACCESS_LOST:              "DXGI_ERROR_ACCESS_LOST",
	DXGI_ERROR_WAIT_TIMEOUT:             "DXGI_ERROR_WAIT_TIMEOUT",
	DXGI_ERROR_SESSION_DISCONNECTED:     "DXGI_ERROR_SESSION_DISCONNECTED",
	DXGI_ERROR_ACCESS_DENIED:            "DXGI_ERROR_ACCESS_DENIED",
	MF_E_INVALIDREQUEST:                 "MF_E_INVALIDREQUEST",
	MF_E_INVALIDMEDIATYPE:               "MF_E_INVALIDMEDIATYPE",
	MF_E_NOTACCEPTING:                   "MF_E_NOTACCEPTING",
	MF_E_ATTRIBUTENOTFOUND:              "MF_E_ATTRIBUTENOTFOUND",
	MF_E_TRANSFORM_TYPE_NOT_SET:         "MF_E_TRANSFORM_TYPE_NOT_SET",
	MF_E_TRANSFORM_STREAM_CHANGE:        "MF_E_TRANSFORM_STREAM_CHANGE",
	MF_E_TRANSFORM_NEED_MORE_INPUT:      "MF_E_TRANSFORM_NEED_MORE_INPUT",
	MF_E_UNSUPPORTED_D3D_TYPE:           "MF_E_UNSUPPORTED_D3D_TYPE",
	MF_E_TRANSFORM_ASYNC_LOCKED:         "MF_E_TRANSFORM_ASYNC_LOCKED",
	RPC_E_CHANGED_MODE:                  "RPC_E_CHANGED_MODE",
	DXGI_STATUS_OCCLUDED:                "DXGI_STATUS_OCCLUDED",
	DXGI_STATUS_MODE_CHANGED:            "DXGI_STATUS_MODE_CHANGED",
	DXGI_STATUS_MODE_CHANGE_IN_PROGRESS: "DXGI_STATUS_MODE_CHANGE_IN_PROGRESS",
}

// Error writes h as "NAME (0xXXXXXXXX)" for a known code and as
// "HRESULT 0xXXXXXXXX" otherwise.
func (h HRESULT) Error() string {
	if n, ok := names[h]; ok {
		return fmt.Sprintf("%s (0x%08X)", n, uint32(h))
	}
	return fmt.Sprintf("HRESULT 0x%08X", uint32(h))
}

// Failed reports whether the severity bit of h is set.
func (h HRESULT) Failed() bool { return int32(h) < 0 }

// Succeeded reports whether the severity bit of h is clear.
func (h HRESULT) Succeeded() bool { return !h.Failed() }

// Err returns h as an error when it failed and nil when it succeeded.
func (h HRESULT) Err() error {
	if h.Failed() {
		return h
	}
	return nil
}
