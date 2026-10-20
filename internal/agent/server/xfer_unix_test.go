//go:build !windows

package server

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// diskFullError is the error of a write to a full volume.
func diskFullError() error { return syscall.ENOSPC }

// replaceDenials are the ways a destination refuses its replacement: its
// folder is read-only.
func replaceDenials() []denial {
	return []denial{{
		name: "read-only folder",
		apply: func(t *testing.T, path string) func() {
			if os.Geteuid() == 0 {
				t.Skip("root writes into read-only folders")
			}
			dir := filepath.Dir(path)
			if err := os.Chmod(dir, 0o555); err != nil {
				t.Fatal(err)
			}
			undo := func() { os.Chmod(dir, 0o755) }
			t.Cleanup(undo)
			return undo
		},
	}}
}
