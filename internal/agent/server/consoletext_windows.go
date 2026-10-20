//go:build windows

package server

import (
	"syscall"
	"unsafe"
)

var (
	procGetOEMCP            = syscall.NewLazyDLL("kernel32.dll").NewProc("GetOEMCP")
	procIsDBCSLeadByteEx    = syscall.NewLazyDLL("kernel32.dll").NewProc("IsDBCSLeadByteEx")
	procMultiByteToWideChar = syscall.NewLazyDLL("kernel32.dll").NewProc("MultiByteToWideChar")
)

// codePageUTF8 is the UTF-8 code page.
const codePageUTF8 = 65001

// consoleDecoder returns the decoder of the OEM code page, the code page a
// new console starts with, or nil when that code page is UTF-8 or unknown.
func consoleDecoder() func([]byte) ([]byte, int) {
	cp, _, _ := procGetOEMCP.Call()
	if cp == 0 || cp == codePageUTF8 {
		return nil
	}
	return func(b []byte) ([]byte, int) { return decodeCodePage(uint32(cp), b) }
}

// decodeCodePage converts the longest prefix of b that holds whole
// characters of code page cp to UTF-8 with decodeDBCS, IsDBCSLeadByteEx and
// MultiByteToWideChar, and returns it with the prefix length.
func decodeCodePage(cp uint32, b []byte) ([]byte, int) {
	lead := func(c byte) bool {
		r, _, _ := procIsDBCSLeadByteEx.Call(uintptr(cp), uintptr(c))
		return r != 0
	}
	convert := func(p []byte) ([]uint16, bool) {
		wide := make([]uint16, len(p))
		k, _, _ := procMultiByteToWideChar.Call(uintptr(cp), 0, uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)),
			uintptr(unsafe.Pointer(&wide[0])), uintptr(len(wide)))
		return wide[:k], k != 0
	}
	return decodeDBCS(b, lead, convert)
}
