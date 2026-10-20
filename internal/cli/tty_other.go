//go:build !darwin && !linux

package cli

// stdinIsTerminal is false where the terminal check is not implemented.
func stdinIsTerminal() bool { return false }
