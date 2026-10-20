package session

import "errors"

// Info describes the Windows session the agent runs in, with the keys of
// docs/protocol.md §3.1 session.info.
type Info struct {
	SessionID uint32 `json:"session_id"`
	// User is the agent's account as DOMAIN\name and SID its security id.
	User string `json:"user"`
	SID  string `json:"sid,omitempty"`
	// ConnectState is ConnectActive, ConnectConnected or
	// ConnectDisconnected.
	ConnectState string `json:"connect_state"`
	// Protocol is ProtocolConsole or ProtocolRDP.
	Protocol string `json:"protocol"`
	// Locked reports whether the session is locked; nil when its lock
	// state cannot be read.
	Locked *bool `json:"locked,omitempty"`
	// InputDesktop is DesktopDefault, DesktopWinlogon or DesktopUnknown.
	InputDesktop string `json:"input_desktop"`
	// Integrity is the integrity level of the agent process: untrusted,
	// low, medium, high, system or protected.
	Integrity string `json:"integrity,omitempty"`
}

// IsLocked reports whether Locked says the session is locked.
func (i Info) IsLocked() bool { return i.Locked != nil && *i.Locked }

// Values of WTSINFOEX_LEVEL1.SessionFlags.
const (
	wtsSessionStateLock   = 0
	wtsSessionStateUnlock = 1
)

// lockState is the lock state the session flags say, nil when they were
// not read or name neither a lock nor an unlock.
func lockState(flags int32, read bool) *bool {
	if !read || (flags != wtsSessionStateLock && flags != wtsSessionStateUnlock) {
		return nil
	}
	locked := flags == wtsSessionStateLock
	return &locked
}

// Values of Info.ConnectState.
const (
	ConnectActive       = "active"
	ConnectConnected    = "connected"
	ConnectDisconnected = "disconnected"
)

// Values of Info.Protocol.
const (
	ProtocolConsole = "console"
	ProtocolRDP     = "rdp"
)

// Values of Info.InputDesktop.
const (
	DesktopDefault  = "Default"
	DesktopWinlogon = "Winlogon"
	DesktopUnknown  = "unknown"
)

// ErrUnsupported is the error of Current on a system other than Windows.
var ErrUnsupported = errors.New("Windows session information needs a Windows agent")
