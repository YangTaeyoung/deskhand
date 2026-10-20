//go:build !windows

package server

import (
	"github.com/YangTaeyoung/deskhand/internal/agent/pty"
)

// shProgram is the shell of the ptys outside Windows.
const shProgram = "/bin/sh"

// ptyShells lists the shells a pty can start: sh when /bin/sh is a file.
func ptyShells() []string {
	if isFile(shProgram) {
		return []string{"sh"}
	}
	return nil
}

// knownPTYShell reports whether name is a shell this agent can start when
// it is installed.
func knownPTYShell(name string) bool { return name == "sh" }

// ptyShellSpec returns the program of the shell name with env, to which
// TERM=xterm-256color is added when env has no TERM.
func ptyShellSpec(name string, env []string) (pty.Spec, error) {
	if envValue(env, "TERM") == "" {
		env = append(env, "TERM=xterm-256color")
	}
	return pty.Spec{Path: shProgram, Args: []string{"sh"}, Env: env}, nil
}
