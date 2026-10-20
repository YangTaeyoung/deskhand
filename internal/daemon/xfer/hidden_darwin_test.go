package xfer_test

import "golang.org/x/sys/unix"

// hidden reports whether the entry path has the hidden flag.
func hidden(path string) bool {
	var st unix.Stat_t
	return unix.Lstat(path, &st) == nil && st.Flags&unix.UF_HIDDEN != 0
}
