//go:build !windows

package server

// desktopSession describes the desktop session the agent serves, or
// nothing without one.
func (s *Server) desktopSession() map[string]any {
	sess, ok := s.session()
	if !ok {
		return map[string]any{}
	}
	return map[string]any{"session_id": sess.ID, "user": sess.User}
}
