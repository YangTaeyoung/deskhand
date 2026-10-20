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
	Locked   bool   `json:"locked"`
	// InputDesktop is DesktopDefault, DesktopWinlogon or DesktopUnknown.
	InputDesktop string `json:"input_desktop"`
	// Integrity is the integrity level of the agent process: untrusted,
	// low, medium, high, system or protected.
	Integrity string `json:"integrity,omitempty"`
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
