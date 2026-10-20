//go:build windows

package server

import (
	"errors"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// replaceFile renames the file from to the path to, replacing the file
// there (MoveFileExW with MOVEFILE_REPLACE_EXISTING, written through to
// disk). A denied access while another program holds to open without
// sharing its deletion is returned as ERROR_SHARING_VIOLATION.
func replaceFile(from, to string) error {
	f, err := windows.UTF16PtrFromString(extendedPath(from))
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(extendedPath(to))
	if err != nil {
		return err
	}
	err = windows.MoveFileEx(f, t, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) && heldWithoutDeleteSharing(t) {
		err = windows.ERROR_SHARING_VIOLATION
	}
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}

// heldWithoutDeleteSharing reports whether opening the entry at path for
// deletion, sharing everything, fails with ERROR_SHARING_VIOLATION.
func heldWithoutDeleteSharing(path *uint16) bool {
	h, err := windows.CreateFile(path, windows.DELETE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err == nil {
		windows.CloseHandle(h)
	}
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}

// setHidden sets or clears the hidden attribute of path.
func setHidden(path string, hidden bool) error {
	p, err := windows.UTF16PtrFromString(extendedPath(path))
	if err != nil {
		return err
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return &os.PathError{Op: "GetFileAttributes", Path: path, Err: err}
	}
	want := attrs &^ windows.FILE_ATTRIBUTE_HIDDEN
	if hidden {
		want |= windows.FILE_ATTRIBUTE_HIDDEN
	}
	if want == attrs {
		return nil
	}
	if want == 0 {
		want = windows.FILE_ATTRIBUTE_NORMAL
	}
	if err := windows.SetFileAttributes(p, want); err != nil {
		return &os.PathError{Op: "SetFileAttributes", Path: path, Err: err}
	}
	return nil
}

// platformVolumeFree returns the bytes the agent's account may still write
// on the volume of the folder dir (GetDiskFreeSpaceExW).
func platformVolumeFree(dir string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(strings.TrimSuffix(extendedPath(dir), `\`) + `\`)
	if err != nil {
		return 0, err
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, nil, nil); err != nil {
		return 0, &os.PathError{Op: "GetDiskFreeSpaceEx", Path: dir, Err: err}
	}
	return free, nil
}
