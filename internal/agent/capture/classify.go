package capture

import (
	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/agent/win/com"
)

// Classify names why a capture that ended with hr in session s is
// unavailable, or returns nil when neither the session nor hr says it is.
// The session decides first: Session 0, a locked session, the secure
// desktop and a disconnected session make any capture unavailable. Then
// hr: access denied or lost is rdp_output_suppressed in a Remote Desktop
// session and access_lost on the console, and a missing output is
// no_output.
func Classify(hr com.HRESULT, s session.Info) *Unavailable {
	detail := ""
	if hr.Failed() {
		detail = hr.Error()
	}
	reason := ""
	switch {
	case s.SessionID == 0:
		reason = ReasonSession0
	case s.Locked:
		reason = ReasonSessionLocked
	case s.InputDesktop == session.DesktopWinlogon:
		reason = ReasonSecureDesktop
	case s.ConnectState == session.ConnectDisconnected, hr == com.DXGI_ERROR_SESSION_DISCONNECTED:
		reason = ReasonSessionDisconnected
	case accessDenied(hr) && s.Protocol == session.ProtocolRDP:
		reason = ReasonRDPOutputSuppressed
	case accessDenied(hr):
		reason = ReasonAccessLost
	case hr == com.DXGI_ERROR_NOT_FOUND, hr == com.DXGI_ERROR_NOT_CURRENTLY_AVAILABLE:
		reason = ReasonNoOutput
	default:
		return nil
	}
	return &Unavailable{Reason: reason, Detail: detail}
}

// accessDenied reports whether hr refuses or withdraws access to the
// desktop image.
func accessDenied(hr com.HRESULT) bool {
	switch hr {
	case com.E_ACCESSDENIED, com.DXGI_ERROR_ACCESS_DENIED, com.DXGI_ERROR_ACCESS_LOST:
		return true
	}
	return false
}
