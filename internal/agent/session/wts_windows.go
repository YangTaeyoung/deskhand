//go:build windows

package session

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	wtsapi32 = windows.NewLazySystemDLL("wtsapi32.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")

	procWTSQuerySessionInformationW = wtsapi32.NewProc("WTSQuerySessionInformationW")
	procWTSFreeMemory               = wtsapi32.NewProc("WTSFreeMemory")
	procOpenInputDesktop            = user32.NewProc("OpenInputDesktop")
	procGetUserObjectInformationW   = user32.NewProc("GetUserObjectInformationW")
	procCloseDesktop                = user32.NewProc("CloseDesktop")
)

// WTS_INFO_CLASS values.
const (
	wtsConnectState       = 8
	wtsClientProtocolType = 16
	wtsSessionInfoEx      = 25
)

const (
	// desktopReadObjects is DESKTOP_READOBJECTS and uoiName UOI_NAME.
	desktopReadObjects = 0x0001
	uoiName            = 2
	// wtsSessionStateLock is WTS_SESSIONSTATE_LOCK of
	// WTSINFOEX_LEVEL1.SessionFlags.
	wtsSessionStateLock = 0
)

// connectStates maps WTS_CONNECTSTATE_CLASS to Info.ConnectState: WTSActive
// and WTSShadow are active; WTSConnected, WTSConnectQuery and WTSInit are
// connected; WTSDisconnected, WTSIdle, WTSListen, WTSReset and WTSDown are
// disconnected.
var connectStates = map[uint32]string{
	0: ConnectActive, 3: ConnectActive,
	1: ConnectConnected, 2: ConnectConnected, 9: ConnectConnected,
	4: ConnectDisconnected, 5: ConnectDisconnected, 6: ConnectDisconnected, 7: ConnectDisconnected, 8: ConnectDisconnected,
}

// Current describes the session of the agent process. Protocol is rdp when
// the session's client protocol is not the console's; Locked follows the
// session's lock state; InputDesktop names the desktop that receives input,
// Winlogon also when the agent may not open it, and unknown when the
// session has none (Session 0).
func Current() (Info, error) {
	var id uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &id); err != nil {
		return Info{}, fmt.Errorf("ProcessIdToSessionId: %w", err)
	}
	b, err := querySession(id, wtsConnectState)
	if err != nil {
		return Info{}, err
	}
	if len(b) < 4 {
		return Info{}, fmt.Errorf("WTSQuerySessionInformation(WTSConnectState) returned %d bytes", len(b))
	}
	state, ok := connectStates[binary.LittleEndian.Uint32(b)]
	if !ok {
		state = ConnectDisconnected
	}
	a := account()
	info := Info{SessionID: id, User: a.user, SID: a.sid, ConnectState: state, Protocol: ProtocolConsole,
		Locked: locked(id), InputDesktop: inputDesktop(), Integrity: a.integrity}
	if b, err := querySession(id, wtsClientProtocolType); err == nil && len(b) >= 2 && binary.LittleEndian.Uint16(b) != 0 {
		info.Protocol = ProtocolRDP
	}
	return info, nil
}

// querySession returns a copy of the WTSQuerySessionInformation buffer of
// class for session id.
func querySession(id, class uint32) ([]byte, error) {
	var buf *byte
	var n uint32
	r, _, err := procWTSQuerySessionInformationW.Call(0, uintptr(id), uintptr(class), uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return nil, fmt.Errorf("WTSQuerySessionInformation(%d) for session %d: %w", class, id, err)
	}
	defer procWTSFreeMemory.Call(uintptr(unsafe.Pointer(buf)))
	if buf == nil {
		return nil, nil
	}
	return slices.Clone(unsafe.Slice(buf, n)), nil
}

// infoEx is the start of WTSINFOEX_LEVEL1 for one session.
type infoEx struct {
	SessionID, SessionState uint32
	SessionFlags            int32
}

// sessionInfoEx reads WTSINFOEX level 1: Level at offset 0, then the
// 8-aligned union with SessionId, SessionState and SessionFlags at offsets
// 8, 12 and 16.
func sessionInfoEx(id uint32) (infoEx, bool) {
	b, err := querySession(id, wtsSessionInfoEx)
	if err != nil || len(b) < 20 || binary.LittleEndian.Uint32(b) != 1 {
		return infoEx{}, false
	}
	return infoEx{SessionID: binary.LittleEndian.Uint32(b[8:]), SessionState: binary.LittleEndian.Uint32(b[12:]),
		SessionFlags: int32(binary.LittleEndian.Uint32(b[16:]))}, true
}

// locked reports whether the session flags of session id say it is locked.
func locked(id uint32) bool {
	ex, ok := sessionInfoEx(id)
	return ok && ex.SessionFlags == wtsSessionStateLock
}

// InputDesktop names the desktop that receives input in the agent's
// session as Current reports it in Info.InputDesktop, without the rest of
// Current's queries.
func InputDesktop() string { return inputDesktop() }

// inputDesktop names the desktop that receives input in the agent's
// session.
func inputDesktop() string {
	h, _, err := procOpenInputDesktop.Call(0, 0, desktopReadObjects)
	if h == 0 {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return DesktopWinlogon
		}
		return DesktopUnknown
	}
	defer procCloseDesktop.Call(h)
	var name [64]uint16
	var need uint32
	if r, _, _ := procGetUserObjectInformationW.Call(h, uoiName, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)*2), uintptr(unsafe.Pointer(&need))); r == 0 {
		return DesktopUnknown
	}
	switch n := windows.UTF16ToString(name[:]); {
	case strings.EqualFold(n, DesktopDefault):
		return DesktopDefault
	case strings.EqualFold(n, DesktopWinlogon):
		return DesktopWinlogon
	}
	return DesktopUnknown
}

type processAccount struct{ user, sid, integrity string }

// account describes the token of the agent process once: its user as
// DOMAIN\name, the user's SID and the token's integrity level.
var account = sync.OnceValue(func() processAccount {
	tok := windows.GetCurrentProcessToken()
	var a processAccount
	if tu, err := tok.GetTokenUser(); err == nil {
		a.sid = tu.User.Sid.String()
		if name, domain, _, err := tu.User.Sid.LookupAccount(""); err == nil {
			a.user = domain + `\` + name
		}
	}
	a.integrity = integrityLevel(tok)
	return a
})

// integrityLevel names the mandatory label of tok by its RID.
func integrityLevel(tok windows.Token) string {
	rid, ok := TokenIntegrityRID(tok)
	if !ok {
		return ""
	}
	return integrityName(rid)
}

// TokenIntegrityRID returns the RID of the mandatory label of tok, such as
// 0x2000 for medium and 0x3000 for high integrity; ok is false when the
// label cannot be read.
func TokenIntegrityRID(tok windows.Token) (rid uint32, ok bool) {
	var n uint32
	_ = windows.GetTokenInformation(tok, windows.TokenIntegrityLevel, nil, 0, &n)
	if n == 0 {
		return 0, false
	}
	buf := make([]byte, n)
	if err := windows.GetTokenInformation(tok, windows.TokenIntegrityLevel, &buf[0], n, &n); err != nil {
		return 0, false
	}
	sid := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buf[0])).Label.Sid
	count := sid.SubAuthorityCount()
	if count == 0 {
		return 0, false
	}
	return sid.SubAuthority(uint32(count) - 1), true
}

// integrityName names an integrity RID: below 0x1000 untrusted, then low,
// medium (0x2000, with medium plus), high (0x3000), system (0x4000) and
// protected (0x5000 and above).
func integrityName(rid uint32) string {
	switch {
	case rid < 0x1000:
		return "untrusted"
	case rid < 0x2000:
		return "low"
	case rid < 0x3000:
		return "medium"
	case rid < 0x4000:
		return "high"
	case rid < 0x5000:
		return "system"
	}
	return "protected"
}
