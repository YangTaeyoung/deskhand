//go:build windows

package server

// desktopSession is empty: on Windows the hello's session comes from
// Options.SessionInfo.
func (s *Server) desktopSession() map[string]any { return map[string]any{} }
