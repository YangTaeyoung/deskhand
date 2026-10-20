//go:build windows

package server

import (
	"os"
	"os/user"
	"sync"
	"syscall"
	"unsafe"
)

var (
	procProcessIdToSessionId        = syscall.NewLazyDLL("kernel32.dll").NewProc("ProcessIdToSessionId")
	wtsapi32                        = syscall.NewLazyDLL("wtsapi32.dll")
	procWTSQuerySessionInformationW = wtsapi32.NewProc("WTSQuerySessionInformationW")
	procWTSFreeMemory               = wtsapi32.NewProc("WTSFreeMemory")
)

// wtsConnectState is WTSConnectState of WTS_INFO_CLASS.
const wtsConnectState = 8

// connectStates names the WTS_CONNECTSTATE_CLASS values that docs/protocol.md
// §3.1 lists; the others are "other".
var connectStates = map[uint32]string{0: "active", 1: "connected", 4: "disconnected"}

// agentUser is the account the agent runs as, looked up once.
var agentUser = sync.OnceValue(func() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
})

// helloSession describes the Windows session of the agent process: its id,
// the agent's account and the session's connect state, leaving out what
// cannot be read.
func (s *Server) helloSession() map[string]any {
	out := map[string]any{}
	if u := agentUser(); u != "" {
		out["user"] = u
	}
	var id uint32
	if r, _, _ := procProcessIdToSessionId.Call(uintptr(os.Getpid()), uintptr(unsafe.Pointer(&id))); r == 0 {
		return out
	}
	out["session_id"] = id
	var buf *uint32
	var n uint32
	if r, _, _ := procWTSQuerySessionInformationW.Call(0, uintptr(id), wtsConnectState, uintptr(unsafe.Pointer(&buf)),
		uintptr(unsafe.Pointer(&n))); r == 0 || buf == nil {
		return out
	}
	defer procWTSFreeMemory.Call(uintptr(unsafe.Pointer(buf)))
	if n >= 4 {
		state := *buf
		if name, ok := connectStates[state]; ok {
			out["connect_state"] = name
		} else {
			out["connect_state"] = "other"
		}
	}
	return out
}
