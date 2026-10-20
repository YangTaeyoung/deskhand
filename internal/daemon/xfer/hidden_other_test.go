//go:build !darwin

package xfer_test

// hidden reports true: hidden part files are a macOS flag.
func hidden(string) bool { return true }
