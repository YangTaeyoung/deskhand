//go:build windows

package server

import (
	"strings"
	"syscall"
	"unicode/utf16"
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
// characters of code page cp to UTF-8 and returns it with the prefix length.
func decodeCodePage(cp uint32, b []byte) ([]byte, int) {
	n := 0
	for n < len(b) {
		if lead, _, _ := procIsDBCSLeadByteEx.Call(uintptr(cp), uintptr(b[n])); lead != 0 {
			if n+1 >= len(b) {
				break
			}
			n += 2
			continue
		}
		n++
	}
	if n == 0 {
		return nil, 0
	}
	wide := make([]uint16, n)
	k, _, _ := procMultiByteToWideChar.Call(uintptr(cp), 0, uintptr(unsafe.Pointer(&b[0])), uintptr(n),
		uintptr(unsafe.Pointer(&wide[0])), uintptr(len(wide)))
	if k == 0 {
		return []byte(strings.ToValidUTF8(string(b[:n]), "�")), n
	}
	return []byte(string(utf16.Decode(wide[:k]))), n
}
