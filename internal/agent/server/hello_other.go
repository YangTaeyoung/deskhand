//go:build !windows

package server

// helloSession describes the desktop session the agent serves, or nothing
// without one.
func (s *Server) helloSession() map[string]any {
	sess, ok := s.session()
	if !ok {
		return map[string]any{}
	}
	return map[string]any{"session_id": sess.ID, "user": sess.User}
}
