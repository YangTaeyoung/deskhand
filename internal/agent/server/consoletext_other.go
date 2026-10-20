//go:build !windows

package server

// consoleDecoder returns nil: command output is taken as UTF-8.
func consoleDecoder() func([]byte) ([]byte, int) { return nil }
