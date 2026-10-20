//go:build darwin

package xfer

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// setHidden sets or clears the hidden flag (UF_HIDDEN) of the entry path.
func setHidden(path string, hidden bool) error {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return err
	}
	flags := int(st.Flags)
	if hidden {
		flags |= unix.UF_HIDDEN
	} else {
		flags &^= unix.UF_HIDDEN
	}
	return unix.Chflags(path, flags)
}

// renameNoReplace renames from to the path to, which must not exist; when
// to exists the error matches fs.ErrExist. A volume without exclusive
// renames links the file to its new name and removes the old one.
func renameNoReplace(from, to string) error {
	err := unix.RenamexNp(from, to, unix.RENAME_EXCL)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL) {
		return linkRename(from, to)
	}
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}

// writable returns nil when this process may add and remove entries in the
// folder dir.
func writable(dir string) error {
	if err := unix.Access(dir, unix.W_OK|unix.X_OK); err != nil {
		return &os.PathError{Op: "access", Path: dir, Err: err}
	}
	return nil
}

// pcCaseSensitive is _PC_CASE_SENSITIVE of <sys/unistd.h>.
const pcCaseSensitive = 11

// caseInsensitive reports whether the volume of the folder dir takes names
// that differ only in letter case for one name.
func caseInsensitive(dir string) bool {
	v, err := unix.Pathconf(dir, pcCaseSensitive)
	return err == nil && v == 0
}
