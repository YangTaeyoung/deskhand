//go:build !windows

package input

// platformSystem is nil: input injection needs Windows.
func platformSystem() system { return nil }
