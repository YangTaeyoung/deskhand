//go:build windows

package diag

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/YangTaeyoung/deskhand/internal/wire"
)

var (
	kernel32                             = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemTimes                   = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx             = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetLogicalProcessorInformationEx = kernel32.NewProc("GetLogicalProcessorInformationEx")
)

const (
	// currentVersionKey holds the name, version and edition of Windows.
	currentVersionKey = `SOFTWARE\Microsoft\Windows NT\CurrentVersion`
	// processorKey holds the name of the first processor.
	processorKey = `HARDWARE\DESCRIPTION\System\CentralProcessor\0`
	// allProcessorGroups is ALL_PROCESSOR_GROUPS.
	allProcessorGroups = 0xffff
	// relationProcessorCore is RelationProcessorCore of
	// LOGICAL_PROCESSOR_RELATIONSHIP.
	relationProcessorCore = 0
)

// readSystem returns the system summary. The CPU usage is measured over
// SampleGap while the rest is read.
func readSystem(ctx context.Context) (System, error) {
	first, err := readCPUSample()
	if err != nil {
		return System{}, fmt.Errorf("the processor times could not be read (%v); retry", err)
	}
	began := time.Now()
	s := System{OS: readOS(), CPU: CPU{Name: registryString(processorKey, "ProcessorNameString"), Cores: processorCores(),
		Threads: int(windows.GetActiveProcessorCount(allProcessorGroups))}, Disks: readDisks(), GPUs: readGPUs()}
	s.ComputerName, _ = os.Hostname()
	mem, err := readMemory()
	if err != nil {
		return System{}, fmt.Errorf("the memory status could not be read (%v); retry", err)
	}
	s.Memory = mem
	up := windows.DurationSinceBoot()
	now := time.Now()
	s.BootTime, s.UptimeS = wire.FormatDiagTime(now.Add(-up)), int64(up/time.Second)
	if err := sleep(ctx, SampleGap-time.Since(began)); err != nil {
		return System{}, err
	}
	second, err := readCPUSample()
	if err != nil {
		return System{}, fmt.Errorf("the processor times could not be read (%v); retry", err)
	}
	s.CPU.UsagePct = cpuUsage(first, second)
	return s, nil
}

// readCPUSample reads GetSystemTimes.
func readCPUSample() (cpuSample, error) {
	var idle, kernel, user windows.Filetime
	r, _, e := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if r == 0 {
		return cpuSample{}, e
	}
	return cpuSample{idle: filetimeCount(idle), kernel: filetimeCount(kernel), user: filetimeCount(user)}, nil
}

// filetimeCount returns f as one count of 100 ns.
func filetimeCount(f windows.Filetime) uint64 {
	return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime)
}

// memoryStatusEx is MEMORYSTATUSEX.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// readMemory reads the physical memory with GlobalMemoryStatusEx; used is
// the memory that is not available.
func readMemory() (Memory, error) {
	m := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if r, _, e := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r == 0 {
		return Memory{}, e
	}
	return Memory{TotalBytes: m.TotalPhys, UsedBytes: m.TotalPhys - m.AvailPhys}, nil
}

// processorCores counts the processor cores, 0 when they cannot be read.
func processorCores() int {
	var size uint32
	procGetLogicalProcessorInformationEx.Call(relationProcessorCore, 0, uintptr(unsafe.Pointer(&size)))
	if size == 0 {
		return 0
	}
	buf := make([]byte, size)
	if r, _, _ := procGetLogicalProcessorInformationEx.Call(relationProcessorCore, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size))); r == 0 {
		return 0
	}
	n := 0
	for off := uint32(0); off+8 <= size; n++ {
		rec := *(*uint32)(unsafe.Pointer(&buf[off+4]))
		if rec == 0 {
			break
		}
		off += rec
	}
	return n
}

// readOS reads the product name, display version, build and edition of
// Windows.
func readOS() OS {
	v := windows.RtlGetVersion()
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, currentVersionKey, registry.QUERY_VALUE)
	if err != nil {
		return OS{Name: productName("", v.BuildNumber), Build: osBuild(v.BuildNumber, 0, false)}
	}
	defer k.Close()
	str := func(name string) string {
		s, _, _ := k.GetStringValue(name)
		return strings.TrimSpace(s)
	}
	o := OS{Name: productName(str("ProductName"), v.BuildNumber), Version: str("DisplayVersion"), Edition: str("EditionID")}
	if o.Version == "" {
		o.Version = str("ReleaseId")
	}
	ubr, _, err := k.GetIntegerValue("UBR")
	o.Build = osBuild(v.BuildNumber, uint32(ubr), err == nil)
	return o
}

// registryString returns a string value of an HKLM key, "" when it cannot
// be read.
func registryString(key, name string) string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	s, _, _ := k.GetStringValue(name)
	return strings.TrimSpace(s)
}

// readDisks returns the fixed, removable and RAM drives that have a volume,
// in drive letter order.
func readDisks() []Disk {
	out := []Disk{}
	n, err := windows.GetLogicalDriveStrings(0, nil)
	if err != nil || n == 0 {
		return out
	}
	buf := make([]uint16, n+1)
	n, err = windows.GetLogicalDriveStrings(uint32(len(buf)), &buf[0])
	if err != nil {
		return out
	}
	for _, root := range strings.Split(windows.UTF16ToString(buf[:n]), "\x00") {
		if d, ok := readDisk(root); ok {
			out = append(out, d)
		}
	}
	return out
}

// readDisk returns the volume of a drive root, and false for a drive that
// is not fixed, removable or a RAM drive or that has no readable volume.
func readDisk(root string) (Disk, bool) {
	if root == "" {
		return Disk{}, false
	}
	p, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return Disk{}, false
	}
	switch windows.GetDriveType(p) {
	case windows.DRIVE_FIXED, windows.DRIVE_REMOVABLE, windows.DRIVE_RAMDISK:
	default:
		return Disk{}, false
	}
	var avail, total, free uint64
	if windows.GetDiskFreeSpaceEx(p, &avail, &total, &free) != nil {
		return Disk{}, false
	}
	label := make([]uint16, windows.MAX_PATH+1)
	fs := make([]uint16, windows.MAX_PATH+1)
	d := Disk{Root: root, TotalBytes: total, FreeBytes: free}
	if windows.GetVolumeInformation(p, &label[0], uint32(len(label)), nil, nil, nil, &fs[0], uint32(len(fs))) == nil {
		d.Label, d.FS = windows.UTF16ToString(label), windows.UTF16ToString(fs)
	}
	return d, true
}
