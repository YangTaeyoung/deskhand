//go:build !windows

package main

import "time"

// clockStart is the origin of nowUS.
var clockStart = time.Now()

// nowUS returns the microseconds since the probe started on Go's monotonic
// clock.
func nowUS() int64 { return int64(time.Since(clockStart) / time.Microsecond) }

// clockSource names the clock of nowUS.
func clockSource() string { return "go-monotonic" }

// timerResolutionMS returns 0: the system timer resolution is read only on
// Windows.
func timerResolutionMS() float64 { return 0 }
