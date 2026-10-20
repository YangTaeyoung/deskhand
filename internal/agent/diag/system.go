package diag

import (
	"fmt"
	"math"
	"strings"
)

// windows11Build is the first build of Windows 11.
const windows11Build = 22000

// productName returns the registry's ProductName of a Windows build with
// "Windows 10" read as "Windows 11" from windows11Build on; an empty name is
// "Windows".
func productName(name string, build uint32) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Windows"
	}
	if build >= windows11Build && strings.HasPrefix(name, "Windows 10") {
		return "Windows 11" + strings.TrimPrefix(name, "Windows 10")
	}
	return name
}

// osBuild returns the build number with the update revision ubr when known.
func osBuild(build, ubr uint32, known bool) string {
	if !known {
		return fmt.Sprint(build)
	}
	return fmt.Sprintf("%d.%d", build, ubr)
}

// cpuSample is one GetSystemTimes reading in 100 ns units; kernel includes
// idle.
type cpuSample struct{ idle, kernel, user uint64 }

// cpuUsage returns the busy share of the processors between two samples,
// 0–100 to one decimal, and 0 without elapsed time.
func cpuUsage(a, b cpuSample) float64 {
	total := (b.kernel - a.kernel) + (b.user - a.user)
	idle := b.idle - a.idle
	if total == 0 || idle > total {
		return 0
	}
	return round1(100 * float64(total-idle) / float64(total))
}

// round1 rounds v to one decimal.
func round1(v float64) float64 { return math.Round(v*10) / 10 }

// driverVersion formats the user-mode driver version that
// IDXGIAdapter::CheckInterfaceSupport returns as its four 16-bit parts.
func driverVersion(umd int64) string {
	u := uint64(umd)
	return fmt.Sprintf("%d.%d.%d.%d", u>>48, u>>32&0xffff, u>>16&0xffff, u&0xffff)
}
