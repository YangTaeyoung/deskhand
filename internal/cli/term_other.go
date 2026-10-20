//go:build !darwin && !linux

package cli

import "errors"

// stdinTerminal is the terminal of the process's stdin, which this system
// does not report.
type stdinTerminal struct{}

func (stdinTerminal) Size() (int, int, bool) { return 0, 0, false }

func (stdinTerminal) Raw() (func(), error) { return nil, errors.New("raw mode is not supported here") }

func (stdinTerminal) Resizes() (<-chan struct{}, func()) { return nil, func() {} }
