//go:build !darwin && !linux

package cli

import "os"

// stdinIsTerminal is false where the terminal check is not implemented.
func stdinIsTerminal() bool { return false }

// fileIsTerminal is false where the terminal check is not implemented.
func fileIsTerminal(*os.File) bool { return false }
