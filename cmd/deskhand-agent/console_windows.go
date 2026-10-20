//go:build windows

package main

import (
	"os"
	"syscall"
)

var procAttachConsole = syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole")

// attachParentProcess is ATTACH_PARENT_PROCESS of AttachConsole.
const attachParentProcess = ^uintptr(0)

// attachConsole points os.Stdout and os.Stderr that have no standard handle
// at the console of the parent process, when the parent has one.
func attachConsole() {
	out, _ := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	errOut, _ := syscall.GetStdHandle(syscall.STD_ERROR_HANDLE)
	if validHandle(out) && validHandle(errOut) {
		return
	}
	if r, _, _ := procAttachConsole.Call(attachParentProcess); r == 0 {
		return
	}
	con, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return
	}
	if !validHandle(out) {
		os.Stdout = con
	}
	if !validHandle(errOut) {
		os.Stderr = con
	}
}

func validHandle(h syscall.Handle) bool { return h != 0 && h != syscall.InvalidHandle }
