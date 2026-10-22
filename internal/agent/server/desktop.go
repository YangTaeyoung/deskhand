package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/YangTaeyoung/deskhand/internal/agent/display"
	"github.com/YangTaeyoung/deskhand/internal/agent/session"
	"github.com/YangTaeyoung/deskhand/internal/proto"
	"github.com/YangTaeyoung/deskhand/internal/wire"
)

// errSession0Displays is the error of display.list in Session 0.
const errSession0Displays = "the agent runs in Session 0, which has no displays; start deskhand-agent through its scheduled task in the signed-in user's session"

// registerDesktop adds session.info and display.list.
func (s *Server) registerDesktop() {
	s.register("session.info", false, s.sessionInfoOp)
	s.register("session.rdp", false, s.sessionRDPOp)
	s.register("display.list", false, s.displayListOp)
}

// sessionFields are the docs/protocol.md §3.1 session.info keys of info;
// sid and integrity are left out when empty, locked when unknown.
func sessionFields(info session.Info) proto.Message {
	m := proto.Message{"session_id": info.SessionID, "user": info.User, "connect_state": info.ConnectState,
		"protocol": info.Protocol, "input_desktop": info.InputDesktop}
	if info.Locked != nil {
		m["locked"] = *info.Locked
	}
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
	if m, ok := s.sessionNow(); ok {
		return m
	}
	return s.desktopSession()
}

// sessionNow is the hello's session as it is now: the session.info fields
// of the Windows session, the desktop session of Options.Session on a
// system without one, and false when the Windows session cannot be read.
func (s *Server) sessionNow() (map[string]any, bool) {
	info, err := s.opt.SessionInfo()
	switch {
	case err == nil:
		return sessionFields(info), true
	case errors.Is(err, session.ErrUnsupported):
		return s.desktopSession(), true
	}
	return nil, false
}

// helloDisplays is the hello's displays: the display.list displays, or
// none when they cannot be read.
func (s *Server) helloDisplays() any {
	if list, ok := s.displaysNow(); ok {
		return list
	}
	return []any{}
}

// displaysNow is the display.list displays as they are now, false when
// they cannot be read.
func (s *Server) displaysNow() ([]display.Info, bool) {
	list, err := s.opt.Displays()
	if err != nil {
		return nil, false
	}
	if list == nil {
		list = []display.Info{}
	}
	return list, true
}

// sendDesktopChanges sends to c display.changed{displays} when displays,
// read when displaysOK, differ from the displays sent on c last, and
// session.changed with the fields of sess, read when sessOK, when it
// differs from the session sent last.
func sendDesktopChanges(c *conn, displays []display.Info, displaysOK bool, sess map[string]any, sessOK bool) {
	if displaysOK && c.replaceDisplays(jsonOf(displays)) {
		c.send(proto.Message{"t": "ev", "name": "display.changed", "displays": displays})
	}
	if sessOK && c.replaceSession(jsonOf(sess)) {
		ev := proto.Message{}
		for k, v := range sess {
			ev[k] = v
		}
		ev["t"], ev["name"] = "ev", "session.changed"
		c.send(ev)
	}
}

// jsonOf is the JSON of v, nil when it has none.
func jsonOf(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
