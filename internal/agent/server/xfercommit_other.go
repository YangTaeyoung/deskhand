//go:build !windows

package server

import (
	"os"
	"syscall"
)

// replaceFile renames the file from to the path to, replacing the file
// there.
func replaceFile(from, to string) error { return os.Rename(from, to) }

// setHidden does nothing outside Windows.
func setHidden(string, bool) error { return nil }

// platformVolumeFree returns the bytes an unprivileged user may still write
// on the volume of the folder dir.
func platformVolumeFree(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
