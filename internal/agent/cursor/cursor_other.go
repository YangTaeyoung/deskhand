//go:build !windows

package cursor

// platformSource is nil: the cursor stream needs Windows.
func platformSource() source { return nil }
