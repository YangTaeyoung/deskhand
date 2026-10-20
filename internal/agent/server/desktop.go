package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// screenImplemented and inputImplemented report whether this agent version
// captures the screen and injects input.
const (
	screenImplemented = false
	inputImplemented  = false
)

// reasonSession0 is the reason of a capability that needs a desktop while
// the agent runs in Session 0.
const reasonSession0 = "session0"

// errSession0Displays is the error of display.list in Session 0.
const errSession0Displays = "the agent runs in Session 0, which has no displays; start deskhand-agent through its scheduled task in the signed-in user's session"

// registerDesktop adds session.info and display.list.
func (s *Server) registerDesktop() {
	s.register("session.info", false, s.sessionInfoOp)
	s.register("display.list", false, s.displayListOp)
}

// sessionFields are the docs/protocol.md §3.1 session.info keys of info;
// sid and integrity are left out when empty.
func sessionFields(info session.Info) proto.Message {
	m := proto.Message{"session_id": info.SessionID, "user": info.User, "connect_state": info.ConnectState,
		"protocol": info.Protocol, "locked": info.Locked, "input_desktop": info.InputDesktop}
	if info.SID != "" {
		m["sid"] = info.SID
	}
	if info.Integrity != "" {
		m["integrity"] = info.Integrity
	}
	return m
}

// sessionInfoOp answers session.info with the Windows session, or, on a
// system without one, with the desktop session of Options.Session when it
// is set.
func (s *Server) sessionInfoOp(context.Context, proto.Message) (proto.Message, error) {
	info, err := s.opt.SessionInfo()
	switch {
	case err == nil:
		return sessionFields(info), nil
	case errors.Is(err, session.ErrUnsupported) && s.opt.Session != nil:
		return s.desktopSessionInfo()
	case errors.Is(err, session.ErrUnsupported):
		return nil, coded(wire.CodeCapabilityUnsupported, "session.info needs a Windows agent; ask a Windows PC", false)
	}
	return nil, coded(wire.CodeInternal, fmt.Sprintf("the agent's Windows session cannot be read (%v); try again, and restart deskhand-agent if it persists", err), true)
}

// displayListOp answers display.list.
func (s *Server) displayListOp(context.Context, proto.Message) (proto.Message, error) {
	list, err := s.opt.Displays()
	switch {
	case err == nil:
		if list == nil {
			list = []display.Info{}
		}
		return proto.Message{"displays": list}, nil
	case errors.Is(err, display.ErrUnsupported):
		return nil, coded(wire.CodeCapabilityUnsupported, "display.list needs a Windows agent; ask a Windows PC", false)
	}
	if info, ierr := s.opt.SessionInfo(); ierr == nil && info.SessionID == 0 {
		return nil, coded(wire.CodeDisplayUnavailable, errSession0Displays, false)
	}
	return nil, coded(wire.CodeDisplayUnavailable, fmt.Sprintf("%v; check that the PC's display is on and its user is signed in, then try again", err), true)
}

// helloSession is the hello's session: the session.info fields of the
// Windows session, else the desktop session of Options.Session.
func (s *Server) helloSession() map[string]any {
	if info, err := s.opt.SessionInfo(); err == nil {
		return sessionFields(info)
	}
	return s.desktopSession()
}

// helloDisplays is the hello's displays: the display.list displays, or
// none when they cannot be read.
func (s *Server) helloDisplays() any {
	list, err := s.opt.Displays()
	if err != nil || list == nil {
		return []any{}
	}
	return list
}

// sessionCap is the state of a capability that needs the desktop of the
// agent's session: unsupported while not implemented or without Windows
// session information, unavailable when the session cannot be read or is
// Session 0, and ready otherwise.
func sessionCap(implemented bool, info session.Info, err error) capState {
	switch {
	case !implemented:
		return capState{State: capUnsupported, Reason: notImplemented}
	case errors.Is(err, session.ErrUnsupported):
		return capState{State: capUnsupported, Reason: err.Error()}
	case err != nil:
		return capState{State: capUnavailable, Reason: err.Error()}
	case info.SessionID == 0:
		return capState{State: capUnavailable, Reason: reasonSession0}
	}
	return capState{State: capReady}
}
