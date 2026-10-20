//go:build windows

package server

import (
	"context"
	"errors"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// systemProcess is one entry of SystemProcessInformation: its creation time
// in 100 ns since 1601 (0 when the system gives none), its user and kernel
// time in 100 ns and its working set in bytes.
type systemProcess struct {
	pid, ppid  int
	name       string
	created    int64
	cpu        int64
	workingSet uint64
}

// systemProcesses reads every process from SystemProcessInformation.
func systemProcesses() ([]systemProcess, error) {
	size := uint32(1 << 20)
	for range 8 {
		buf := make([]uint64, size/8)
		var ret uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&buf[0]), size, &ret)
		if errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) {
			size = max(ret, size) + 256<<10
			continue
		}
		if err != nil {
			return nil, err
		}
		raw := unsafe.Slice((*byte)(unsafe.Pointer(&buf[0])), size)
		var out []systemProcess
		for off := uint32(0); off < size; {
			e := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&raw[off]))
			p := systemProcess{pid: int(e.UniqueProcessID), ppid: int(e.InheritedFromUniqueProcessID), created: e.CreateTime,
				cpu: e.UserTime + e.KernelTime, workingSet: uint64(e.WorkingSetSize)}
			if e.ImageName.Length > 0 && e.ImageName.Buffer != nil {
				p.name = windows.UTF16ToString(unsafe.Slice(e.ImageName.Buffer, e.ImageName.Length/2))
			}
			out = append(out, p)
			if e.NextEntryOffset == 0 {
				break
			}
			off += e.NextEntryOffset
		}
		return out, nil
	}
	return nil, errors.New("the process list kept growing")
}

// idleProcessName is the name proc.list gives the process of pid 0.
const idleProcessName = "System Idle Process"

// sampleProcesses lists the processes of SystemProcessInformation with
// the processor share each used between two readings gap apart, the
// program path and command line where the agent can open the process, and
// the account of its token. A process without a creation time (pid 0) is
// listed as created at boot.
func sampleProcesses(ctx context.Context, gap time.Duration) ([]procInfo, error) {
	first, err := systemProcesses()
	if err != nil {
		return nil, err
	}
	began := time.Now()
	t := time.NewTimer(gap)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	second, err := systemProcesses()
	if err != nil {
		return nil, err
	}
	elapsed := time.Since(began)
	type key struct {
		pid     int
		created int64
	}
	used := make(map[key]int64, len(first))
	for _, p := range first {
		used[key{p.pid, p.created}] = p.cpu
	}
	cpus := int(windows.GetActiveProcessorCount(windowsAllProcessorGroups))
	boot := time.Now().Add(-windows.DurationSinceBoot())
	owners := processOwners()
	accounts := map[string]string{}
	scratch := newProcessScratch()
	out := make([]procInfo, 0, len(second))
	for _, p := range second {
		info := procInfo{PID: p.pid, PPID: p.ppid, Name: p.name, MemKB: int(p.workingSet / 1024), Started: boot}
		if p.pid == 0 {
			info.Name = idleProcessName
		}
		if p.created > 0 {
			info.Started = time.Unix(0, (p.created-unixEpochFiletime)*100)
		}
		info.CPUPct = cpuPercent(time.Duration(p.cpu-used[key{p.pid, p.created}])*100, elapsed, cpus)
		info.Path, info.Cmd = scratch.details(p.pid)
		if sid, ok := owners[p.pid]; ok {
			info.User = accountName(accounts, sid)
		}
		out = append(out, info)
	}
	return out, nil
}

const (
	// windowsAllProcessorGroups is ALL_PROCESSOR_GROUPS.
	windowsAllProcessorGroups = 0xffff
	// unixEpochFiletime is 1970-01-01 as a FILETIME count.
	unixEpochFiletime = 116444736000000000
)

// processScratch holds the buffers details reuses.
type processScratch struct {
	path []uint16
	cmd  []uint64
}

func newProcessScratch() *processScratch {
	return &processScratch{path: make([]uint16, windows.MAX_LONG_PATH), cmd: make([]uint64, 1024)}
}

// details returns the program path and command line of pid, "" for what
// cannot be read.
func (s *processScratch) details(pid int) (path, cmd string) {
	if pid == 0 {
		return "", ""
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", ""
	}
	defer windows.CloseHandle(h)
	n := uint32(len(s.path))
	if windows.QueryFullProcessImageName(h, 0, &s.path[0], &n) == nil {
		path = windows.UTF16ToString(s.path[:n])
	}
	for range 3 {
		var ret uint32
		size := uint32(8 * len(s.cmd))
		err := windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, unsafe.Pointer(&s.cmd[0]), size, &ret)
		if err == nil {
			us := (*windows.NTUnicodeString)(unsafe.Pointer(&s.cmd[0]))
			if us.Length > 0 && us.Buffer != nil {
				cmd = windows.UTF16ToString(unsafe.Slice(us.Buffer, us.Length/2))
			}
			break
		}
		if !errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) || ret <= size {
			break
		}
		s.cmd = make([]uint64, ret/8+1)
	}
	return path, cmd
}

var (
	procWTSEnumerateProcessesExW = wtsapi32.NewProc("WTSEnumerateProcessesExW")
	procWTSFreeMemoryExW         = wtsapi32.NewProc("WTSFreeMemoryExW")
)

const (
	// wtsAnySession is WTS_ANY_SESSION.
	wtsAnySession = 0xfffffffe
	// wtsTypeProcessInfoLevel1 is WTSTypeProcessInfoLevel1.
	wtsTypeProcessInfoLevel1 = 1
)

// wtsProcessInfoEx is WTS_PROCESS_INFO_EX.
type wtsProcessInfoEx struct {
	SessionID          uint32
	ProcessID          uint32
	ProcessName        *uint16
	UserSid            *windows.SID
	NumberOfThreads    uint32
	HandleCount        uint32
	PagefileUsage      uint32
	PeakPagefileUsage  uint32
	WorkingSetSize     uint32
	PeakWorkingSetSize uint32
	UserTime           int64
	KernelTime         int64
}

// processOwners maps each process to the SID string of its token's user,
// as WTSEnumerateProcessesExW lists them; nil when it cannot list them.
func processOwners() map[int]string {
	level := uint32(1)
	var info *wtsProcessInfoEx
	var count uint32
	if r, _, _ := procWTSEnumerateProcessesExW.Call(0, uintptr(unsafe.Pointer(&level)), wtsAnySession, uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&count))); r == 0 || info == nil {
		return nil
	}
	defer procWTSFreeMemoryExW.Call(wtsTypeProcessInfoLevel1, uintptr(unsafe.Pointer(info)), uintptr(count))
	out := make(map[int]string, count)
	for _, p := range unsafe.Slice(info, count) {
		if p.UserSid != nil {
			out[int(p.ProcessID)] = p.UserSid.String()
		}
	}
	return out
}

// accountName returns DOMAIN\name of the account of the SID string sid,
// the SID string when it cannot be looked up, through the cache names.
func accountName(names map[string]string, sid string) string {
	if n, ok := names[sid]; ok {
		return n
	}
	name := sid
	if s, err := windows.StringToSid(sid); err == nil {
		if account, domain, _, err := s.LookupAccount(""); err == nil {
			name = account
			if domain != "" {
				name = domain + `\` + account
			}
		}
	}
	names[sid] = name
	return name
}

// pidCreation returns the creation time of pid, and false when the process
// cannot be opened.
func pidCreation(pid int) (time.Time, bool) {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return time.Time{}, false
	}
	defer syscall.CloseHandle(h)
	return processCreation(h)
}

// processStarted returns the creation time of the running process that has
// pid now, as proc.list lists it, and false when no running process has
// it. A process that ended but is still open elsewhere does not run.
func processStarted(pid int) (time.Time, bool) {
	if h, err := syscall.OpenProcess(processQueryLimitedInformation|syscall.SYNCHRONIZE, false, uint32(pid)); err == nil {
		defer syscall.CloseHandle(h)
		if ev, _ := syscall.WaitForSingleObject(h, 0); ev != uint32(syscall.WAIT_TIMEOUT) {
			return time.Time{}, false
		}
		return processCreation(h)
	}
	procs, err := systemProcesses()
	if err != nil {
		return time.Time{}, false
	}
	for _, p := range procs {
		if p.pid == pid {
			if p.created <= 0 {
				return time.Now().Add(-windows.DurationSinceBoot()), true
			}
			return time.Unix(0, (p.created-unixEpochFiletime)*100), true
		}
	}
	return time.Time{}, false
}
