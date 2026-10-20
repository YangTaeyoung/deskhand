//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                      = windows.NewLazySystemDLL("kernel32.dll")
	ntdll                         = windows.NewLazySystemDLL("ntdll.dll")
	procQueryPerformanceCounter   = kernel32.NewProc("QueryPerformanceCounter")
	procQueryPerformanceFrequency = kernel32.NewProc("QueryPerformanceFrequency")
	procNtQueryTimerResolution    = ntdll.NewProc("NtQueryTimerResolution")

	qpcFrequency, qpcStart = qpcInit()
)

// qpcInit returns the performance counter frequency and its reading now.
func qpcInit() (freq, start int64) {
	procQueryPerformanceFrequency.Call(uintptr(unsafe.Pointer(&freq)))
	procQueryPerformanceCounter.Call(uintptr(unsafe.Pointer(&start)))
	if freq <= 0 {
		freq = 1
	}
	return freq, start
}

// nowUS returns the microseconds since the probe started on the
// performance counter.
func nowUS() int64 {
	var c int64
	procQueryPerformanceCounter.Call(uintptr(unsafe.Pointer(&c)))
	d := c - qpcStart
	return d/qpcFrequency*1_000_000 + d%qpcFrequency*1_000_000/qpcFrequency
}

// clockSource names the clock of nowUS.
func clockSource() string { return "qpc" }

// timerResolutionMS returns the current system timer resolution in
// milliseconds, or 0 when it cannot be read.
func timerResolutionMS() float64 {
	var lowest, highest, current uint32
	if r, _, _ := procNtQueryTimerResolution.Call(uintptr(unsafe.Pointer(&lowest)), uintptr(unsafe.Pointer(&highest)), uintptr(unsafe.Pointer(&current))); r != 0 {
		return 0
	}
	return float64(current) / 1e4
}
