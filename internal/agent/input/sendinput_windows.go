//go:build windows

package input

import (
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/YangTaeyoung/deskhand/internal/agent/session"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")

	procSendInput                = user32.NewProc("SendInput")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	procMapVirtualKeyW           = user32.NewProc("MapVirtualKeyW")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79

	// mapvkVKToVSCEx is MAPVK_VK_TO_VSC_EX: the scan code with 0xE0 or
	// 0xE1 in the high byte for an extended key.
	mapvkVKToVSCEx = 4

	// threadWaiting is KTHREAD_STATE Waiting and waitUserRequest
	// KWAIT_REASON WrUserRequest, the state of a thread blocked in
	// GetMessage.
	threadWaiting   = 5
	waitUserRequest = 13
	// drainPoll is the time between two looks at the foreground thread.
	drainPoll = 2 * time.Millisecond
)

// mouseInput is MOUSEINPUT.
type mouseInput struct {
	dx, dy    int32
	mouseData uint32
	flags     uint32
	time      uint32
	extraInfo uintptr
}

// keybdInput is KEYBDINPUT, which shares the union of INPUT with
// MOUSEINPUT.
type keybdInput struct {
	vk, scan  uint16
	flags     uint32
	time      uint32
	extraInfo uintptr
}

// winInput is INPUT: the type, then the union aligned for its pointer
// field.
type winInput struct {
	typ uint32
	mi  mouseInput
}

// windowsSystem injects through SendInput into the input desktop of the
// agent's session.
type windowsSystem struct{}

func platformSystem() system { return windowsSystem{} }

func (windowsSystem) send(in []raw) (int, error) {
	if len(in) == 0 {
		return 0, nil
	}
	buf := make([]winInput, len(in))
	for i, r := range in {
		if r.mouse {
			buf[i] = winInput{typ: inputMouse, mi: mouseInput{dx: r.dx, dy: r.dy, mouseData: r.data, flags: r.flags}}
			continue
		}
		buf[i].typ = inputKeyboard
		*(*keybdInput)(unsafe.Pointer(&buf[i].mi)) = keybdInput{vk: r.vk, scan: r.scan, flags: r.flags}
	}
	n, _, err := procSendInput.Call(uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])), unsafe.Sizeof(buf[0]))
	if int(n) < len(in) {
		return int(n), err
	}
	return int(n), nil
}

func (windowsSystem) virtualScreen() rect {
	metric := func(i uintptr) int {
		v, _, _ := procGetSystemMetrics.Call(i)
		return int(int32(v))
	}
	return rect{x: metric(smXVirtualScreen), y: metric(smYVirtualScreen), w: metric(smCXVirtualScreen), h: metric(smCYVirtualScreen)}
}

func (windowsSystem) keyScan(vk uint16) (uint16, bool) {
	sc, _, _ := procMapVirtualKeyW.Call(uintptr(vk), mapvkVKToVSCEx)
	prefix := sc >> 8 & 0xFF
	return uint16(sc & 0xFF), prefix == 0xE0 || prefix == 0xE1
}

// foregroundAbove compares the integrity level of the foreground window's
// process with the agent's; a process the agent may not inspect counts as
// higher.
func (windowsSystem) foregroundAbove() bool {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return false
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(h)
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer tok.Close()
	theirs, ok := session.TokenIntegrityRID(tok)
	if !ok {
		return false
	}
	ours, ok := session.TokenIntegrityRID(windows.GetCurrentProcessToken())
	return ok && theirs > ours
}

func (windowsSystem) clipboard() clipboardAccess { return windowsClipboard{} }

// drain waits until the thread of the foreground window is seen blocked
// waiting for messages (Waiting, WrUserRequest) on two looks drainPoll
// apart. A window whose thread cannot be found does not wait.
func (windowsSystem) drain(timeout time.Duration) error {
	fg, _, _ := procGetForegroundWindow.Call()
	if fg == 0 {
		return nil
	}
	var pid uint32
	tid, _, _ := procGetWindowThreadProcessId.Call(fg, uintptr(unsafe.Pointer(&pid)))
	if tid == 0 {
		return nil
	}
	deadline := time.Now().Add(timeout)
	idle := 0
	for {
		state, reason, found := threadState(pid, uint32(tid))
		switch {
		case !found:
			return nil
		case state == threadWaiting && reason == waitUserRequest:
			idle++
		default:
			idle = 0
		}
		if idle == 2 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("thread %d of process %d is still busy, in thread state %d with wait reason %d", tid, pid, state, reason)
		}
		time.Sleep(drainPoll)
	}
}

// systemThreadInformation is SYSTEM_THREAD_INFORMATION, which follows its
// SYSTEM_PROCESS_INFORMATION once per thread.
type systemThreadInformation struct {
	kernelTime, userTime, createTime int64
	waitTime                         uint32
	startAddress                     uintptr
	uniqueProcess, uniqueThread      uintptr
	priority, basePriority           int32
	contextSwitches                  uint32
	threadState                      uint32
	waitReason                       uint32
}

// processBuffers holds buffers for NtQuerySystemInformation.
var processBuffers = sync.Pool{New: func() any { b := make([]byte, 1<<20); return &b }}

// threadState returns the KTHREAD_STATE and KWAIT_REASON of thread tid of
// process pid; found is false when the thread is not in the list of
// processes.
func threadState(pid, tid uint32) (state, reason uint32, found bool) {
	bp := processBuffers.Get().(*[]byte)
	defer processBuffers.Put(bp)
	for {
		var need uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&(*bp)[0]), uint32(len(*bp)), &need)
		if err == windows.STATUS_INFO_LENGTH_MISMATCH && int(need) < 256<<20 {
			*bp = make([]byte, max(int(need)+64<<10, 2*len(*bp)))
			continue
		}
		if err != nil {
			return 0, 0, false
		}
		break
	}
	buf := *bp
	procSize := int(unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{}))
	threadSize := int(unsafe.Sizeof(systemThreadInformation{}))
	for off := 0; off+procSize <= len(buf); {
		p := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[off]))
		if uint32(p.UniqueProcessID) == pid {
			n := int(p.NumberOfThreads)
			if off+procSize+n*threadSize > len(buf) {
				return 0, 0, false
			}
			for i := range n {
				t := (*systemThreadInformation)(unsafe.Pointer(&buf[off+procSize+i*threadSize]))
				if uint32(t.uniqueThread) == tid {
					return t.threadState, t.waitReason, true
				}
			}
			return 0, 0, false
		}
		if p.NextEntryOffset == 0 {
			return 0, 0, false
		}
		off += int(p.NextEntryOffset)
	}
	return 0, 0, false
}
