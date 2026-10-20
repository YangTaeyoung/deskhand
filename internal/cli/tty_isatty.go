//go:build darwin || linux

package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

// stdinIsTerminal reports whether stdin is a terminal device.
func stdinIsTerminal() bool { return fileIsTerminal(os.Stdin) }

// fileIsTerminal reports whether f is a terminal device.
func fileIsTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), ioctlReadTermios)
	return err == nil
}
