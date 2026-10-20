package session

import (
	"os"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// wtsSessionStateUnlock is WTS_SESSIONSTATE_UNLOCK.
const wtsSessionStateUnlock = 1

func checkValues(t *testing.T, info Info) {
	t.Helper()
	if !slices.Contains([]string{ConnectActive, ConnectConnected, ConnectDisconnected}, info.ConnectState) {
		t.Errorf("connect state %q", info.ConnectState)
	}
	if !slices.Contains([]string{ProtocolConsole, ProtocolRDP}, info.Protocol) {
		t.Errorf("protocol %q", info.Protocol)
	}
	if !slices.Contains([]string{DesktopDefault, DesktopWinlogon, DesktopUnknown}, info.InputDesktop) {
		t.Errorf("input desktop %q", info.InputDesktop)
	}
	if !slices.Contains([]string{"untrusted", "low", "medium", "high", "system", "protected"}, info.Integrity) {
		t.Errorf("integrity %q", info.Integrity)
	}
	if !strings.Contains(info.User, `\`) || !strings.HasPrefix(info.SID, "S-1-") {
		t.Errorf("user %q sid %q", info.User, info.SID)
	}
	ex, ok := sessionInfoEx(info.SessionID)
	if !ok || ex.SessionID != info.SessionID || connectStates[ex.SessionState] != info.ConnectState {
		t.Errorf("WTSINFOEX %+v for session %d in state %q", ex, info.SessionID, info.ConnectState)
	}
	if info.InputDesktop == DesktopDefault && ex.SessionFlags != wtsSessionStateUnlock {
		t.Errorf("session flags %d on the Default desktop", ex.SessionFlags)
	}
	elevated := windows.GetCurrentProcessToken().IsElevated()
	if high := info.Integrity == "high" || info.Integrity == "system"; elevated != high {
		t.Errorf("integrity %q for a process whose token elevation is %v", info.Integrity, elevated)
	}
}

func TestSessionCurrentInUserSession(t *testing.T) {
	if os.Getenv("DESKHAND_WINTEST") == "" {
		t.Skip("needs the signed-in user's session; run through scripts/wintest.sh")
	}
	info, err := Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	t.Logf("session %+v", info)
	checkValues(t, info)
	if info.SessionID < 1 {
		t.Fatalf("session id %d in the user's session", info.SessionID)
	}
	console := windows.WTSGetActiveConsoleSessionId()
	if (info.Protocol == ProtocolConsole) != (info.SessionID == console) {
		t.Errorf("protocol %q for session %d while the console session is %d", info.Protocol, info.SessionID, console)
	}
	if info.InputDesktop == DesktopDefault && info.Locked {
		t.Error("a session whose input desktop is Default reports locked")
	}
}

func TestSessionZeroDetected(t *testing.T) {
	if os.Getenv("SSH_CONNECTION") == "" {
		t.Skip("needs a process started by sshd, which runs in Session 0; run scripts/wintest.sh --ssh")
	}
	info, err := Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	t.Logf("session %+v", info)
	checkValues(t, info)
	if info.SessionID != 0 {
		t.Fatalf("session id %d over SSH", info.SessionID)
	}
	if info.InputDesktop != DesktopUnknown {
		t.Errorf("input desktop %q in Session 0", info.InputDesktop)
	}
}
