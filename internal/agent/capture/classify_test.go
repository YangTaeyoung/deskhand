package capture

import (
	"strings"
	"testing"

	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
)

func TestAccessDeniedClassified(t *testing.T) {
	console := session.Info{SessionID: 1, ConnectState: session.ConnectActive, Protocol: session.ProtocolConsole,
		InputDesktop: session.DesktopDefault}
	with := func(f func(*session.Info)) session.Info {
		s := console
		f(&s)
		return s
	}
	session0 := with(func(s *session.Info) {
		s.SessionID, s.ConnectState, s.InputDesktop = 0, session.ConnectDisconnected, session.DesktopUnknown
	})
	winlogon := with(func(s *session.Info) { s.InputDesktop = session.DesktopWinlogon })
	locked := with(func(s *session.Info) { s.InputDesktop, s.Locked = session.DesktopWinlogon, true })
	disconnected := with(func(s *session.Info) { s.ConnectState = session.ConnectDisconnected })
	rdp := with(func(s *session.Info) { s.Protocol = session.ProtocolRDP })

	for _, c := range []struct {
		name string
		hr   com.HRESULT
		s    session.Info
		want string
	}{
		{"session 0", com.DXGI_ERROR_NOT_CURRENTLY_AVAILABLE, session0, ReasonSession0},
		{"session 0 without a failure", com.S_OK, session0, ReasonSession0},
		{"secure desktop", com.E_ACCESSDENIED, winlogon, ReasonSecureDesktop},
		{"locked", com.E_ACCESSDENIED, locked, ReasonSessionLocked},
		{"locked on the default desktop", com.E_ACCESSDENIED, with(func(s *session.Info) { s.Locked = true }), ReasonSessionLocked},
		{"disconnected", com.E_ACCESSDENIED, disconnected, ReasonSessionDisconnected},
		{"session disconnected result", com.DXGI_ERROR_SESSION_DISCONNECTED, console, ReasonSessionDisconnected},
		{"rdp access denied", com.E_ACCESSDENIED, rdp, ReasonRDPOutputSuppressed},
		{"rdp dxgi access denied", com.DXGI_ERROR_ACCESS_DENIED, rdp, ReasonRDPOutputSuppressed},
		{"console access denied", com.E_ACCESSDENIED, console, ReasonAccessLost},
		{"console access lost", com.DXGI_ERROR_ACCESS_LOST, console, ReasonAccessLost},
		{"no output", com.DXGI_ERROR_NOT_FOUND, console, ReasonNoOutput},
		{"no output available", com.DXGI_ERROR_NOT_CURRENTLY_AVAILABLE, console, ReasonNoOutput},
		{"success", com.S_OK, console, ""},
		{"rdp success", com.S_OK, rdp, ""},
		{"other failure", com.E_OUTOFMEMORY, console, ""},
	} {
		got := Classify(c.hr, c.s)
		switch {
		case c.want == "" && got != nil:
			t.Errorf("%s: %+v, want nil", c.name, got)
		case c.want != "" && got == nil:
			t.Errorf("%s: nil, want %s", c.name, c.want)
		case c.want != "" && got.Reason != c.want:
			t.Errorf("%s: reason %s, want %s", c.name, got.Reason, c.want)
		case c.want != "" && c.hr.Failed() && !strings.Contains(got.Detail, c.hr.Error()):
			t.Errorf("%s: detail %q lacks %q", c.name, got.Detail, c.hr.Error())
		}
	}
}

func TestUnavailableNeedsUserForTheReasonsAPersonClears(t *testing.T) {
	for reason, want := range map[string]bool{
		ReasonSecureDesktop: true, ReasonSessionLocked: true, ReasonRDPOutputSuppressed: true,
		ReasonSession0: false, ReasonSessionDisconnected: false, ReasonAccessLost: false, ReasonNoOutput: false,
	} {
		u := &Unavailable{Reason: reason, Detail: "E_ACCESSDENIED (0x80070005)"}
		if u.NeedsUser() != want {
			t.Errorf("%s: NeedsUser %v", reason, !want)
		}
		if msg := u.Error(); !strings.Contains(msg, "("+reason+": E_ACCESSDENIED (0x80070005))") || !strings.Contains(msg, "; ") {
			t.Errorf("%s: error %q", reason, msg)
		}
	}
}
