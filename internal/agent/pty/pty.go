// Package pty runs a program on a pseudo-terminal: a ConPTY pseudo console
// on Windows (conpty_windows.go) and a POSIX pseudo-terminal on macOS and
// Linux (pty_other.go).
//
// A Console's Read returns what the program writes to its terminal and
// Write sends keyboard input to it. Once Wait returns, Shutdown ends the
// terminal so that Read reaches the end of the output, and Close releases
// the terminal's handles.
package pty

import (
	"errors"
	"fmt"
)

// MaxSize bounds the columns and rows of a terminal.
const MaxSize = 1000

// Spec describes the program a Console runs.
type Spec struct {
	// Path is the program file and Args its arguments, Args[0] included.
	Path string
	Args []string
	// CmdLine, when set, is the command line Windows passes to the program
	// as it is instead of one composed from Args.
	CmdLine string
	// Dir is the working folder and Env the whole environment.
	Dir string
	Env []string
	// Cols and Rows are the size of the terminal.
	Cols, Rows int
}

// ErrUnsupported is returned by Start on a system without pseudo-terminals.
var ErrUnsupported = errors.New("this system has no pseudo-terminals")

// CheckSize returns an error unless cols and rows are both between 1 and
// MaxSize.
func CheckSize(cols, rows int) error {
	if cols < 1 || rows < 1 || cols > MaxSize || rows > MaxSize {
		return fmt.Errorf("cols %d and rows %d must be between 1 and %d", cols, rows, MaxSize)
	}
	return nil
}
